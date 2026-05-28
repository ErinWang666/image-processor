package domain

import "context"

// RateLimiter 定义了限流器的抽象接口
//
// 为什么只有一个方法？
// → 接口越小越好（Interface Segregation Principle）
//   调用方只需要问一个问题："我这个请求，能不能通过？"
//   底层用令牌桶、漏桶、还是滑动窗口，调用方完全不需要知道
//
// 为什么参数是 key string 而不是直接传 IP？
// → 灵活性。今天按 IP 限流，明天可能按 UserID 限流，后天按 API Key 限流
//   调用方（中间件）负责决定用什么作为 key，限流器只负责执行限流逻辑
//   这就是"关注点分离"：谁来限 vs 怎么限，是两个独立的问题
type RateLimiter interface {
	// Allow 检查 key 对应的请求是否被允许通过
	// 返回 true  → 令牌充足，请求放行
	// 返回 false → 令牌耗尽，请求应该被拒绝（返回 429）
	// 返回 error → Redis 故障等系统错误，调用方应该降级放行
	Allow(ctx context.Context, key string) (bool, error)
}
