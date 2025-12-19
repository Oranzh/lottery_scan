package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/genai"
)

// ==========================================
// CONFIG: 配置项
// ==========================================
const GEMINI_MODEL = "gemini-2.5-flash"

// ==========================================
// 1. 数据结构定义 (Data Models)
// ==========================================

// 标准结构体（逻辑层使用，保持严格 String）
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

// ★★★ 新增：临时结构体，用于宽松解析 JSON (Middleware Struct) ★★★
// 这里的 Red/Blue 使用 []interface{}，既能接数字，也能接字符串
type RawLotteryData struct {
	Type    string `json:"type"`
	Issue   string `json:"issue"`
	Tickets []struct {
		Red        []interface{} `json:"red"`  // 容错关键点
		Blue       []interface{} `json:"blue"` // 容错关键点
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
// 2. 核心算法服务 (Brain)
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

// --- A. 双色球验奖器 ---
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

// --- B. 大乐透验奖器 ---
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

// --- C. 排列5验奖器 ---
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
// 3. Gemini OCR 服务 (Eyes - 增强容错版)
// ==========================================

// ★★★ 辅助函数：将任意类型(数字或字符串)统一转为 "01" 格式的字符串 ★★★
func anyToString(val interface{}) string {
	switch v := val.(type) {
	case string:
		// 如果已经是字符串，直接返回（假设AI给了 "02"）
		// 可以顺便处理一下去空格
		return strings.TrimSpace(v)
	case float64:
		// JSON 中的数字通常解析为 float64
		// 强制转为 int 并格式化为两位数，例如 2 -> "02", 11 -> "11"
		return fmt.Sprintf("%02d", int(v))
	case int:
		return fmt.Sprintf("%02d", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func callGeminiOCR(fileBytes []byte, apiKey string) ([]LotteryData, error) {
	ctx := context.Background()

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			// 这里替换为卖家的域名，末尾通常不需要加 /v1，SDK 会自动处理路径
			// 如果卖家给的地址是 https://api.proxy.com/v1，尝试只填 https://api.proxy.com
			BaseURL: "https://broad-heart-f0c3.oranzh-cc4761.workers.dev",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("创建客户端失败: %v", err)
	}

	// 提示词：依然要求返回字符串，但我们会在代码层做兜底
	promptText := `
	你是一个专业OCR助手。请分析图片，识别其中出现的**所有**彩票。
	返回一个JSON数组（Array），每个元素代表一张票。
	字段说明：
	- type: 彩种名称 (例如 "双色球")
	- issue: 期号 (例如 "2025107")
	- tickets: 号码列表数组
	
	【重要】：
	tickets 中的 "red" 和 "blue" 数组里的号码，请尽量输出为字符串(例如 "01")。
	如果无法确定，输出数字也可以，我会自行处理。
	`

	mimeType := http.DetectContentType(fileBytes)

	parts := []*genai.Part{
		{Text: promptText},
		{
			InlineData: &genai.Blob{
				Data:     fileBytes,
				MIMEType: mimeType,
			},
		},
	}

	contents := []*genai.Content{
		{
			Parts: parts,
			Role:  "user",
		},
	}

	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
	}

	resp, err := client.Models.GenerateContent(ctx, GEMINI_MODEL, contents, config)
	if err != nil {
		return nil, fmt.Errorf("API调用错误: %v (MIME: %s)", err, mimeType)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("无识别结果")
	}

	jsonStr := resp.Candidates[0].Content.Parts[0].Text
	jsonStr = strings.TrimPrefix(jsonStr, "```json")
	jsonStr = strings.TrimPrefix(jsonStr, "```")
	jsonStr = strings.TrimSuffix(jsonStr, "```")

	// ★★★ 核心修改：使用 RawLotteryData 进行宽松解析 ★★★
	var rawDataList []RawLotteryData

	// 1. 先尝试解析为数组
	if err := json.Unmarshal([]byte(jsonStr), &rawDataList); err != nil {
		// 2. 如果失败，尝试解析为单个对象并包装
		var singleRaw RawLotteryData
		if err2 := json.Unmarshal([]byte(jsonStr), &singleRaw); err2 == nil {
			rawDataList = []RawLotteryData{singleRaw}
		} else {
			fmt.Printf("JSON解析彻底失败: %v\n原始文本: %s\n", err, jsonStr)
			return nil, err
		}
	}

	// ★★★ 3. 数据清洗与转换 (Raw -> Standard) ★★★
	var finalData []LotteryData

	for _, raw := range rawDataList {
		cleanTickets := []UserTicket{}

		for _, t := range raw.Tickets {
			// 处理红球：遍历 interface{} 数组，转为 string 数组
			cleanRed := []string{}
			for _, r := range t.Red {
				cleanRed = append(cleanRed, anyToString(r))
			}

			// 处理蓝球
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
	// 容错：去除 potential whitespace
	issue = strings.TrimSpace(issue)

	if strings.Contains(lotteryType, "双色球") && issue == "2025107" {
		// 对应你的图片期号 2025107
		// 这里我随机填了一组中奖号码用于测试，你可以改成图片上的号码测试是否中奖
		// 假设开奖号码就是第一行的号码: 02 11 15 21 28 33 + 07
		return WinningNumbers{Red: []string{"02", "11", "15", "21", "28", "33"}, Blue: []string{"07"}}
	}

	// 之前的 Mock 数据
	if strings.Contains(lotteryType, "双色球") && issue == "2025145" {
		return WinningNumbers{Red: []string{"02", "09", "15", "23", "28", "33"}, Blue: []string{"06"}}
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

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		c.JSON(500, gin.H{"error": "服务端未配置 GEMINI_API_KEY"})
		return
	}

	ocrResults, err := callGeminiOCR(fileBytes, apiKey)
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
	if os.Getenv("GEMINI_API_KEY") == "" {
		log.Fatal("请先设置环境变量 GEMINI_API_KEY")
	}

	r := gin.Default()
	r.MaxMultipartMemory = 8 << 20

	r.POST("/api/v1/scan", verifyHandler)

	fmt.Printf("🚀 验奖机启动 (SDK: google.golang.org/genai | Model: %s)\n", GEMINI_MODEL)
	fmt.Println("监听端口: 8080")
	r.Run(":8080")
}
