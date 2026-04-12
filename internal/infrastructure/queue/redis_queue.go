package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/redis/go-redis/v9"
)

type RedisQueue struct {
	client *redis.Client
}

func NewRedisQueue(client *redis.Client) domain.Queue {
	return &RedisQueue{client: client}
}

// Publish 实现发送消息
// 我们使用 Redis List 的 RPUSH 命令来模拟队列 (FIFO)
func (q *RedisQueue) Publish(ctx context.Context, topic string, payload domain.TaskPayload) error {
	// 1. 序列化消息 (变成 JSON 字符串)
	bytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// 2. 推送到 Redis List (Topic 就是 Key)
	err = q.client.RPush(ctx, topic, bytes).Err()
	if err != nil {
		return fmt.Errorf("failed to push to redis: %w", err)
	}

	return nil
}

// Subscribe 实现阻塞式拉取
func (q *RedisQueue) Subscribe(ctx context.Context, topic string) (<-chan domain.TaskPayload, error) {
	// 创建一个 Go Channel，用来把消息传给外面的 Worker
	msgChan := make(chan domain.TaskPayload)

	// 启动一个后台协程，不断从 Redis 拉取数据
	go func() {
		defer close(msgChan) // 退出时关闭通道

		for {
			select {
			case <-ctx.Done(): // 如果主程序叫停
				return
			default:
				// BLPOP: 从 topic 列表中阻塞等待 0 秒 (0 表示无限等待)
				// 这是一个高效的“长轮询”
				result, err := q.client.BLPop(ctx, 0*time.Second, topic).Result()
				if err != nil {
					// 如果是上下文取消或者是 Redis 连接错误，稍作休眠或退出
					if ctx.Err() != nil {
						return
					}
					fmt.Printf("Error receiving from redis: %v\n", err)
					continue
				}

				// result[0] 是 key 名，result[1] 是值 (JSON 字符串)
				rawJSON := result[1]

				var payload domain.TaskPayload
				if err := json.Unmarshal([]byte(rawJSON), &payload); err != nil {
					fmt.Printf("Failed to unmarshal payload: %v\n", err)
					continue
				}

				// 把解包后的任务扔进通道
				msgChan <- payload
			}
		}
	}()

	return msgChan, nil
}

// PublishRaw 直接推送已经序列化好的字节流 (专供 Outbox Relay 使用)
func (q *RedisQueue) PublishRaw(ctx context.Context, topic string, payload []byte) error {
	// 使用 LPush 把 []byte 推入 Redis List 左侧
	err := q.client.LPush(ctx, topic, payload).Err()
	if err != nil {
		return err
	}
	return nil
}