package domain

import (
	"context"
	"time"
)

// Cache 定义了缓存层的抽象接口
// 和 Storage、Queue、TaskLocker 一样，业务层只认接口，不认具体实现
//
// 为什么返回 []byte 而不是 *Image？
// → 因为缓存是通用基础设施，不应该和特定业务对象绑定
//   今天存 Image，明天可能还要缓存 User、Order，所以用最通用的字节流
//
// 为什么 Get 返回 ([]byte, bool, error) 三个值？
// → bool 表示"是否命中"，这样调用方不需要 import redis 包来判断 redis.Nil
//   这就是接口抽象的核心价值：不泄露实现细节
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}
