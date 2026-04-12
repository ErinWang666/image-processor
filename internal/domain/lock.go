package domain

import (
	"context"
	"time"
)

// TaskLocker 工业级分布式锁接口
type TaskLocker interface {
	// Acquire 尝试获取锁。
	// 返回值 1: token (当前锁的唯一标识 UUID)
	// 返回值 2: acquired (是否抢占成功)
	// 返回值 3: error (系统内部错误)
	Acquire(ctx context.Context, key string, ttl time.Duration) (string, bool, error)
	
	// Release 释放锁，必须传入当时获取的 token 进行安全核对
	Release(ctx context.Context, key string, token string) error
}