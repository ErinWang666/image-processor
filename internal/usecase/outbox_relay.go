package usecase

import (
	"context"
	"log"
	"time"
)

// StartOutboxRelay 启动邮递员协程
func (u *ImageUsecase) StartOutboxRelay(ctx context.Context) {
	log.Println("📮 [Outbox Relay] Messenger started, polling for pending messages...")
	
	ticker := time.NewTicker(2 * time.Second) // 每2秒扫一次地
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.processOutboxMessages(ctx)
		}
	}
}

func (u *ImageUsecase) processOutboxMessages(ctx context.Context) {
	// 1. 从数据库捞出 PENDING 状态的消息
	// 这需要在 repo 里写一个 GetPendingOutboxMessages 方法
	messages, err := u.repo.GetPendingOutboxMessages(ctx, 10) // 每次取10条
	if err != nil || len(messages) == 0 {
		return 
	}

	for _, msg := range messages {
		// 2. 尝试发送到真实队列 (Redis)
		// 注意：这里的 payload 是 []byte，我们需要 Publish 接口支持发送字节
		err := u.queue.PublishRaw(ctx, msg.Topic, msg.Payload)
		if err != nil {
			log.Printf("❌ [Outbox Relay] Failed to send message %s: %v", msg.ID, err)
			continue // 发送失败，等下一轮重试
		}

		// 3. 发送成功，更新状态为 SENT
		u.repo.MarkOutboxAsSent(ctx, msg.ID)
		log.Printf("✅ [Outbox Relay] Message %s sent to queue", msg.ID)
	}
}