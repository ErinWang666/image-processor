package domain

import (
	"time"
)

// 发件箱消息状态
const (
	OutboxStatusPending = "PENDING"
	OutboxStatusSent    = "SENT"
	OutboxStatusFailed  = "FAILED"
)

// OutboxMessage 发件箱模型
type OutboxMessage struct {
	ID        string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Topic     string    `gorm:"type:varchar(100);not null"` // 发给哪个队列，比如 "image_processing_queue"
	Payload   []byte    `gorm:"type:jsonb;not null"`        // 要发送的具体内容 (JSON)
	Status    string    `gorm:"type:varchar(20);not null;default:'PENDING'"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}