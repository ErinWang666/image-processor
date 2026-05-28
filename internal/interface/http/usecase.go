package http

import (
	"context"

	"github.com/ErinWang666/image-processor/internal/domain"
)

// ImageUsecase 定义了 Handler 层需要的 Usecase 能力
// 为什么不直接用 *usecase.ImageUsecase？
// → Handler 层不应该依赖具体实现，依赖接口才能 Mock、才能测试
// 这里只列 handler.go 实际用到的三个方法，不需要全部
type ImageUsecase interface {
	CreateTask(ctx context.Context, img *domain.Image) error
	ConfirmTask(ctx context.Context, imageID string) error
	GetImage(ctx context.Context, id string) (*domain.Image, error)
}
