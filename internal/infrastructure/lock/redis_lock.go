package lock

import (
	"context"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// 定义 Lua 脚本常量：先比对值，如果相等才执行 DEL 操作
const releaseLockLuaScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end
`

type RedisLocker struct {
	client *redis.Client
}

func NewRedisLocker(client *redis.Client) domain.TaskLocker {
	return &RedisLocker{client: client}
}

func (r *RedisLocker) Acquire(ctx context.Context, key string, ttl time.Duration) (string, bool, error) {
	// 1. 生成全球唯一的 Token (这把锁的钥匙)
	token := uuid.New().String()

	// 2. 尝试加锁，把 Token 存进去
	success, err := r.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return "", false, err // Redis 宕机等系统故障
	}
	
	// 3. 返回生成的 Token 和 抢锁结果
	return token, success, nil
}

func (r *RedisLocker) Release(ctx context.Context, key string, token string) error {
	// 使用 Eval 执行 Lua 脚本，保证 Compare-And-Delete 的原子性
	// KEYS[1] 对应 key，ARGV[1] 对应 token
	err := r.client.Eval(ctx, releaseLockLuaScript, []string{key}, token).Err()
	
	if err != nil && err != redis.Nil {
		return err // 真正的 Redis 故障
	}
	
	// 如果脚本返回 0，说明要么锁过期了，要么锁变成别人的了，但无论如何我们成功避免了误删！
	return nil 
}