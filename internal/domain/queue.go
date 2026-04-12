package domain

import (
	"context"
)

type TaskPayload struct {
	ImageID   string `json:"image_id"`
	UserID    string `json:"user_id"`
	EventType string `json:"event_type"`
}

// Queue 接口增加了 Subscribe 方法
// 它返回一个只读通道 (<-chan)，Worker 只要盯着这个通道看就行
type Queue interface {
	Publish(ctx context.Context, topic string, payload TaskPayload) error
	// 👇 新增：专门用来发送已经被序列化好的 []byte (为 Outbox Relay 准备)
	PublishRaw(ctx context.Context, topic string, payload []byte) error
	Subscribe(ctx context.Context, topic string) (<-chan TaskPayload, error)
}