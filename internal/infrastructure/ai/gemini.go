package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type GeminiService struct {
	client *genai.Client
	model  *genai.GenerativeModel
}

// NewGeminiService 初始化 Gemini 客户端
func NewGeminiService(ctx context.Context, apiKey string) (domain.AIService, error) {
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}

	// 使用 gemini-1.5-flash 模型，速度极快，支持多模态（看图）
	model := client.GenerativeModel("gemini-2.5-flash")
	
	// 设置温度值 (0.0 - 1.0)，越低回答越固定、客观，适合打标签
	model.SetTemperature(0.2) 

	return &GeminiService{
		client: client,
		model:  model,
	}, nil
}

// GenerateTags 实现看图打标签的逻辑
func (s *GeminiService) GenerateTags(ctx context.Context, imageData []byte) ([]string, error) {
	// 1. 精心设计的 Prompt (提示词工程)
	prompt := genai.Text("请提取这张图片的5个核心关键词，用于SEO标签。请严格只返回一个JSON数组格式的字符串，例如：[\"猫\", \"草地\", \"阳光\", \"可爱\", \"宠物\"]，不要有任何其他解释性文字或Markdown格式。")

	// 2. 包装图片数据 (因为我们在 Usecase 里已经把它转成了 JPEG)
	imgData := genai.ImageData("jpeg", imageData)

	// 3. 发送请求给 Gemini
	resp, err := s.model.GenerateContent(ctx, prompt, imgData)
	if err != nil {
		return nil, fmt.Errorf("gemini api call failed: %w", err)
	}

	// 4. 解析返回值
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("gemini returned empty response")
	}

	var rawText string
	if part, ok := resp.Candidates[0].Content.Parts[0].(genai.Text); ok {
		rawText = string(part)
	}

	// 5. 清理大模型可能带有的 Markdown 标记 (比如 ```json ... ```)
	rawText = strings.TrimSpace(rawText)
	rawText = strings.TrimPrefix(rawText, "```json")
	rawText = strings.TrimPrefix(rawText, "```")
	rawText = strings.TrimSuffix(rawText, "```")
	rawText = strings.TrimSpace(rawText)

	// 6. 将 JSON 字符串解析为 Go 的字符串切片
	var tags []string
	if err := json.Unmarshal([]byte(rawText), &tags); err != nil {
		return nil, fmt.Errorf("failed to parse tags JSON: %w, raw response: %s", err, rawText)
	}

	return tags, nil
}