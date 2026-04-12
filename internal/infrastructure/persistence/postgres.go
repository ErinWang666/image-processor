package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"gorm.io/driver/postgres" // 之前你可能漏掉了这个驱动包
	"gorm.io/gorm"
)

type PostgresRepository struct {
	db *gorm.DB
}

// NewPostgresRepository 构造函数
func NewPostgresRepository(db *gorm.DB) domain.ImageRepository {
	return &PostgresRepository{db: db}
}

// --- 补全这部分关键逻辑 ---

// InitDB 负责打开数据库连接
func InitDB(dsn string) (*gorm.DB, error) {
	// gorm.Open 是连接数据库的入口
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect database: %w", err)
	}
	return db, nil
}

// --- 补全结束 ---

func (r *PostgresRepository) Save(ctx context.Context, img *domain.Image) error {
	return r.db.WithContext(ctx).Create(img).Error
}

func (r *PostgresRepository) GetByID(ctx context.Context, id string) (*domain.Image, error) {
	var img domain.Image
	err := r.db.WithContext(ctx).First(&img, "id = ?", id).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}
	return &img, nil
}

func (r *PostgresRepository) UpdateStatus(ctx context.Context, id string, status domain.ImageStatus) error {
	return r.db.WithContext(ctx).Model(&domain.Image{}).
		Where("id = ?", id).
		Update("status", status).Error
}

func (r *PostgresRepository) UpdateResult(ctx context.Context, id string, status domain.ImageStatus, thumbnailURL string, tags []string) error {
	tagsBytes, err := json.Marshal(tags)
	if err != nil {
		return err
	}

	// 🚨 核心防御：加上 WHERE status = 'PROCESSING'
	result := r.db.WithContext(ctx).Model(&domain.Image{}).
		Where("id = ? AND status = ?", id, domain.StatusProcessing). // <--- 这就是状态卫哨！
		Updates(map[string]interface{}{
			"status":        status,
			"thumbnail_url": thumbnailURL,
			"tags":          tagsBytes,
			"updated_at":    time.Now(),
		})

	if result.Error != nil {
		return result.Error // 数据库本身报错
	}

	// 🚨 检验战果：如果被更新的行数是 0，说明什么？
	// 说明要么这个 ID 不存在，要么它的状态根本不是 PROCESSING！(已经被别的 Worker 抢先做完了)
	if result.RowsAffected == 0 {
		return fmt.Errorf("optimistic lock failed: task %s is not in PROCESSING state (RowsAffected = 0)", id)
	}

	return nil
}

// CreateTaskWithOutbox 开启一个事务，同时创建图片记录和发件箱消息
func (r *PostgresRepository) CreateTaskWithOutbox(ctx context.Context, image *domain.Image, topic string, payload interface{}) error {
	// 将 payload 序列化为 JSON 字节
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// 开启数据库事务
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 插入图片业务数据
		if err := tx.Create(image).Error; err != nil {
			return err // 报错会触发自动回滚 (Rollback)
		}

		// 2. 插入发件箱消息
		outboxMsg := &domain.OutboxMessage{
			Topic:   topic,
			Payload: payloadBytes,
			Status:  domain.OutboxStatusPending,
		}
		if err := tx.Create(outboxMsg).Error; err != nil {
			return err // 报错也会触发自动回滚，保证业务表也不会被插入！
		}

		return nil // 两个都成功，自动提交事务 (Commit)
	})
}

func (r *PostgresRepository) UpdateStatusWithOutbox(ctx context.Context, id string, status domain.ImageStatus, topic string, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 更新主表状态
		if err := tx.Model(&domain.Image{}).Where("id = ?", id).Update("status", status).Error; err != nil {
			return err
		}
		// 2. 插入发件箱
		outboxMsg := &domain.OutboxMessage{
			Topic:   topic,
			Payload: payloadBytes,
			Status:  domain.OutboxStatusPending,
		}
		return tx.Create(outboxMsg).Error
	})
}

// ==========================================
// 📮 Outbox 发件箱专用底层方法
// ==========================================

// GetPendingOutboxMessages 捞出需要发送的消息
func (r *PostgresRepository) GetPendingOutboxMessages(ctx context.Context, limit int) ([]*domain.OutboxMessage, error) {
	var messages []*domain.OutboxMessage
	
	// 💡 架构师细节：按创建时间升序排 (FIFO 先进先出)，并限制每次捞取的数量 (Limit)
	err := r.db.WithContext(ctx).
		Where("status = ?", domain.OutboxStatusPending).
		Order("created_at ASC").
		Limit(limit).
		Find(&messages).Error
		
	return messages, err
}

// MarkOutboxAsSent 发送成功后，标记为已发送
func (r *PostgresRepository) MarkOutboxAsSent(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).
		Model(&domain.OutboxMessage{}).
		Where("id = ?", id).
		Update("status", domain.OutboxStatusSent).Error
}