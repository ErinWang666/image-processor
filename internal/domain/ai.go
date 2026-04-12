package domain

import "context"

// AIService 定义了 AI 相关的能力接口
type AIService interface {
	// GenerateTags 传入图片的字节流，返回一组标签
	GenerateTags(ctx context.Context, imageData []byte) ([]string, error)
}