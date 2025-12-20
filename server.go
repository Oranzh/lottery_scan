package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sashabaranov/go-openai" // ★★★ 切换为社区版 SDK ★★★
)

// ==========================================
// CONFIG: 配置项
// ==========================================

// 阿里云百炼兼容接口地址
const DASHSCOPE_BASE_URL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

// 模型选择 (Qwen-VL)
const QWEN_MODEL = "qwen-vl-max"

// ==========================================
// 1. 数据结构定义 (保持不变)
// ==========================================

type LotteryData struct {
	Type    string       `json:"type"`
	Issue   string       `json:"issue"`
	Tickets []UserTicket `json:"tickets"`
}

type UserTicket struct {
	Red        []string `json:"red"`
	Blue       []string `json:"blue"`
	Multiplier int      `json:"multiplier"`
	Mode       string   `json:"mode"`
}

// 容错结构体
type RawLotteryData struct {
	Type    string `json:"type"`
	Issue   string `json:"issue"`
	Tickets []struct {
		Red        []interface{} `json:"red"`
		Blue       []interface{} `json:"blue"`
		Multiplier int           `json:"multiplier"`
		Mode       string        `json:"mode"`
	} `json:"tickets"`
}

type VerificationResult struct {
	TicketIndex int            `json:"ticket_index"`
	OCRData     LotteryData    `json:"ocr_data"`
	TotalPrize  int64          `json:"total_prize"`
	Details     []ResultDetail `json:"details"`
}

type ResultDetail struct {
	RowIndex int    `json:"row_index"`
	Level    int    `json:"level"`
	Prize    int64  `json:"prize"`
	Status   string `json:"status"`
}

type WinningNumbers struct {
	Red  []string
	Blue []string
}

// ==========================================
// 2. 核心算法服务 (Brain - 保持不变)
// ==========================================

func intersect(a, b []string) int {
	m := make(map[string]bool)
	for _, x := range b {
		m[x] = true
	}
	count := 0
	for _, x := range a {
		if m[x] {
			count++
		}
	}
	return count
}

func combinations(iterable []string, r int) [][]string {
	if r == 0 {
		return [][]string{{}}
	}
	if len(iterable) == 0 {
		return nil
	}
	head, tail := iterable[0], iterable[1:]
	withHead := combinations(tail, r-1)
	var result [][]string
	for _, comb := range withHead {
		result = append(result, append([]string{head}, comb...))
	}
	return append(result, combinations(tail, r)...)
}

type Verifier interface {
	Verify(t UserTicket, win WinningNumbers) (int, int64, string)
}

// --- 双色球验奖器 ---
type DoubleColorVerifier struct{}

func (v *DoubleColorVerifier) Verify(t UserTicket, win WinningNumbers) (int, int64, string) {
	redCombs := combinations(t.Red, 6)
	bestLevel, totalMoney := 0, int64(0)

	for _, redComb := range redCombs {
		for _, b := range t.Blue {
			redHits := intersect(redComb, win.Red)
			blueHits := 0
			if len(win.Blue) > 0 && b == win.Blue[0] {
				blueHits = 1
			}

			level, money := 0, int64(0)
			if redHits == 6 && blueHits == 1 {
				level, money = 1, 5000000
			} else if redHits == 6 && blueHits == 0 {
				level, money = 2, 100000
			} else if redHits == 5 && blueHits == 1 {
				level, money = 3, 3000
			} else if redHits == 5 && blueHits == 0 {
				level, money = 4, 200
			} else if redHits == 4 && blueHits == 1 {
				level, money = 4, 200
			} else if redHits == 4 && blueHits == 0 {
				level, money = 5, 10
			} else if redHits == 3 && blueHits == 1 {
				level, money = 5, 10
			} else if blueHits == 1 {
				level, money = 6, 5
			}

			if money > 0 {
				totalMoney += money
				if bestLevel == 0 || level < bestLevel {
					bestLevel = level
				}
			}
		}
	}
	status := "未中奖"
	if totalMoney > 0 {
		status = fmt.Sprintf("中奖: %d元", totalMoney)
	}
	return bestLevel, totalMoney, status
}

// --- 大乐透验奖器 ---
type LottoVerifier struct{}

func (v *LottoVerifier) Verify(t UserTicket, win WinningNumbers) (int, int64, string) {
	redHits := intersect(t.Red, win.Red)
	blueHits := intersect(t.Blue, win.Blue)
	level, money := 0, int64(0)

	if redHits == 5 && blueHits == 2 {
		level, money = 1, 10000000
	} else if redHits == 5 && blueHits == 1 {
		level, money = 2, 200000
	} else if redHits == 5 && blueHits == 0 {
		level, money = 3, 10000
	} else if redHits == 4 && blueHits == 2 {
		level, money = 4, 3000
	} else if redHits == 4 && blueHits == 1 {
		level, money = 5, 300
	} else if redHits == 3 && blueHits == 2 {
		level, money = 6, 200
	} else if redHits == 4 && blueHits == 0 {
		level, money = 7, 100
	} else if redHits == 3 && blueHits == 1 {
		level, money = 8, 15
	} else if redHits == 2 && blueHits == 2 {
		level, money = 8, 15
	} else if redHits == 3 && blueHits == 0 {
		level, money = 9, 5
	} else if redHits == 2 && blueHits == 1 {
		level, money = 9, 5
	} else if redHits == 1 && blueHits == 2 {
		level, money = 9, 5
	} else if redHits == 0 && blueHits == 2 {
		level, money = 9, 5
	}

	status := "未中奖"
	if money > 0 {
		status = fmt.Sprintf("中奖: %d元", money)
	}
	return level, money, status
}

// --- 排列5验奖器 ---
type Permutation5Verifier struct{}

func (v *Permutation5Verifier) Verify(t UserTicket, win WinningNumbers) (int, int64, string) {
	match := true
	if len(t.Red) != 5 || len(win.Red) != 5 {
		match = false
	} else {
		for i := 0; i < 5; i++ {
			if t.Red[i] != win.Red[i] {
				match = false
				break
			}
		}
	}
	if match {
		return 1, 100000, "一等奖"
	}
	return 0, 0, "未中奖"
}

// ==========================================
// 3. Qwen OCR 服务 (使用 sashabaranov/go-openai SDK)
// ==========================================

func anyToString(val interface{}) string {
	switch v := val.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return fmt.Sprintf("%02d", int(v))
	case int:
		return fmt.Sprintf("%02d", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func callQwenOCR(fileBytes []byte, apiKey string) ([]LotteryData, error) {
	ctx := context.Background()

	// 1. 初始化客户端 (Sashabaranov SDK 配置方式)
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = DASHSCOPE_BASE_URL // 切换到阿里云地址
	client := openai.NewClientWithConfig(config)

	// 2. 图片编码：转换为 Base64
	base64Str := base64.StdEncoding.EncodeToString(fileBytes)
	mimeType := http.DetectContentType(fileBytes)
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, base64Str)

	// 3. 构造 Prompt
	promptText := `
	你是一个专业OCR助手。请分析图片，识别其中出现的**所有**彩票。
	返回一个JSON数组（Array），每个元素代表一张票。
	字段说明：
	- type: 彩种名称 (例如 "双色球")
	- issue: 期号 (例如 "2025107")
	- tickets: 号码列表数组
	
	【重要格式要求】：
	1. tickets 中的 "red" 和 "blue" 数组里的号码，必须是字符串(例如 "01")。
	2. 请保留前导零。
	3. 请只输出纯 JSON 内容，不要包含 markdown 标记。
	`

	// 4. 调用 Chat Completion (MultiContent 模式)
	resp, err := client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: QWEN_MODEL,
			Messages: []openai.ChatCompletionMessage{
				{
					Role: openai.ChatMessageRoleUser,
					MultiContent: []openai.ChatMessagePart{
						{
							Type: openai.ChatMessagePartTypeText,
							Text: promptText,
						},
						{
							Type: openai.ChatMessagePartTypeImageURL,
							ImageURL: &openai.ChatMessageImageURL{
								URL:    dataURL,
								Detail: openai.ImageURLDetailHigh,
							},
						},
					},
				},
			},
			// 某些模型支持 JSON mode，如果报错可以注释掉下面三行
			// ResponseFormat: &openai.ChatCompletionResponseFormat{
			// 	Type: openai.ChatCompletionResponseFormatTypeJSONObject,
			// },
		},
	)

	if err != nil {
		return nil, fmt.Errorf("Qwen API调用失败: %v", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("未返回任何结果")
	}

	// 5. 获取结果文本
	jsonStr := resp.Choices[0].Message.Content

	// 6. 清洗数据
	jsonStr = strings.TrimPrefix(jsonStr, "```json")
	jsonStr = strings.TrimPrefix(jsonStr, "```")
	jsonStr = strings.TrimSuffix(jsonStr, "```")

	// 容错：提取 JSON 数组部分
	firstOpen := strings.Index(jsonStr, "[")
	lastClose := strings.LastIndex(jsonStr, "]")
	if firstOpen != -1 && lastClose != -1 && lastClose > firstOpen {
		jsonStr = jsonStr[firstOpen : lastClose+1]
	}

	// 7. 容错解析
	var rawDataList []RawLotteryData
	if err := json.Unmarshal([]byte(jsonStr), &rawDataList); err != nil {
		var singleRaw RawLotteryData
		if err2 := json.Unmarshal([]byte(jsonStr), &singleRaw); err2 == nil {
			rawDataList = []RawLotteryData{singleRaw}
		} else {
			fmt.Printf("JSON解析失败，原始文本: %s\n", jsonStr)
			return nil, err
		}
	}

	// 8. 转换为标准数据
	var finalData []LotteryData
	for _, raw := range rawDataList {
		cleanTickets := []UserTicket{}
		for _, t := range raw.Tickets {
			cleanRed := []string{}
			for _, r := range t.Red {
				cleanRed = append(cleanRed, anyToString(r))
			}
			cleanBlue := []string{}
			for _, b := range t.Blue {
				cleanBlue = append(cleanBlue, anyToString(b))
			}
			cleanTickets = append(cleanTickets, UserTicket{
				Red:        cleanRed,
				Blue:       cleanBlue,
				Multiplier: t.Multiplier,
				Mode:       t.Mode,
			})
		}
		finalData = append(finalData, LotteryData{
			Type:    raw.Type,
			Issue:   raw.Issue,
			Tickets: cleanTickets,
		})
	}

	return finalData, nil
}

// ==========================================
// 4. 模拟数据库 (Mock DB)
// ==========================================

func getMockWinningNumber(lotteryType, issue string) WinningNumbers {
	issue = strings.TrimSpace(issue)
	// 测试用：图片上的期号
	if strings.Contains(lotteryType, "双色球") && issue == "2025107" {
		return WinningNumbers{
			Red:  []string{"02", "11", "15", "21", "28", "33"},
			Blue: []string{"07"},
		}
	}
	return WinningNumbers{Red: []string{"00"}, Blue: []string{"00"}}
}

// ==========================================
// 5. API 控制器
// ==========================================

func verifyHandler(c *gin.Context) {
	file, _, err := c.Request.FormFile("image")
	if err != nil {
		c.JSON(400, gin.H{"error": "请上传名为 'image' 的文件"})
		return
	}
	fileBytes, _ := io.ReadAll(file)

	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		c.JSON(500, gin.H{"error": "服务端未配置 DASHSCOPE_API_KEY"})
		return
	}

	ocrResults, err := callQwenOCR(fileBytes, apiKey)
	if err != nil {
		c.JSON(500, gin.H{"error": "AI 识别失败: " + err.Error()})
		return
	}

	finalResponse := []VerificationResult{}

	for idx, lottery := range ocrResults {
		winNum := getMockWinningNumber(lottery.Type, lottery.Issue)

		var verifier Verifier
		if strings.Contains(lottery.Type, "双色球") {
			verifier = &DoubleColorVerifier{}
		} else if strings.Contains(lottery.Type, "大乐透") {
			verifier = &LottoVerifier{}
		} else if strings.Contains(lottery.Type, "排列5") {
			verifier = &Permutation5Verifier{}
		}

		res := VerificationResult{
			TicketIndex: idx + 1,
			OCRData:     lottery,
			TotalPrize:  0,
			Details:     []ResultDetail{},
		}

		if verifier != nil {
			for rowIdx, t := range lottery.Tickets {
				level, prize, status := verifier.Verify(t, winNum)
				total := prize * int64(t.Multiplier)

				res.TotalPrize += total
				res.Details = append(res.Details, ResultDetail{
					RowIndex: rowIdx + 1, Level: level, Prize: total, Status: status,
				})
			}
		} else {
			res.Details = append(res.Details, ResultDetail{Status: "暂不支持该彩种验奖"})
		}

		finalResponse = append(finalResponse, res)
	}

	c.JSON(200, finalResponse)
}

func main() {
	if os.Getenv("DASHSCOPE_API_KEY") == "" {
		log.Println("⚠️ 警告: 未检测到 DASHSCOPE_API_KEY 环境变量，请确保已设置。")
	}

	r := gin.Default()
	r.MaxMultipartMemory = 8 << 20

	r.POST("/api/v1/scan", verifyHandler)

	fmt.Printf("🚀 验奖机启动 (Powered by Qwen-VL)\n- SDK: sashabaranov/go-openai\n- Model: %s\n", QWEN_MODEL)
	r.Run(":8080")
}
