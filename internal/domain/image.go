package domain

import (
	"context"
	"time"
)

// ImageStatus 定义了图片处理的生命周期状态
type ImageStatus string

const (
	StatusPending    ImageStatus = "PENDING"
	StatusProcessing ImageStatus = "PROCESSING"
	StatusCompleted  ImageStatus = "COMPLETED"
	StatusFailed     ImageStatus = "FAILED"
)

// Image 是我们的核心领域对象 (Domain Entity)
type Image struct {
	ID        string      `json:"id"`
	UserID    string      `json:"user_id"`
	RawURL    string      `json:"raw_url"`    // 原始图地址
	ThumbnailURL string    `json:"thumbnail_url"`
	// --- 新增：Tags 字段，用于存储 AI 提取的标签 ---
	// gorm:"serializer:json" 会让 GORM 自动在存取时处理 JSON 序列化
	Tags         []string    `json:"tags" gorm:"serializer:json"`
	Status    ImageStatus `json:"status"`     // 当前状态
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// ImageRepository 定义了存储层的抽象接口
// 这里体现了“依赖倒置原则”：业务逻辑不依赖具体数据库，而是数据库实现此接口
type ImageRepository interface {
	Save(ctx context.Context, img *Image) error
	GetByID(ctx context.Context, id string) (*Image, error)
	UpdateStatus(ctx context.Context, id string, status ImageStatus) error
	// --- 修改：增加 tags []string 参数 ---
	UpdateResult(ctx context.Context, id string, status ImageStatus, thumbnailURL string, tags []string) error
	// 👇 以下是为 Outbox 新增的三个契约
	UpdateStatusWithOutbox(ctx context.Context, id string, status ImageStatus, topic string, payload interface{}) error
	GetPendingOutboxMessages(ctx context.Context, limit int) ([]*OutboxMessage, error)
	MarkOutboxAsSent(ctx context.Context, id string) error
}