package http

import (
	"log"
	"net/http"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/gin-gonic/gin"
)

// RateLimitMiddleware 返回一个 Gin 中间件函数，对每个请求按 IP 进行限流
//
// 为什么是"返回函数"而不是直接写一个 Handler？
// → 这是 Gin 中间件的标准写法，叫"闭包工厂"：
//   外层函数接收配置参数（limiter），
//   内层函数是真正的 Handler，它通过闭包捕获外层的 limiter 变量。
//   这样 SetupRouter 里只需要 api.Use(RateLimitMiddleware(limiter)) 一行即可。
//
// 为什么参数是 domain.RateLimiter 接口而不是 *RedisRateLimiter？
// → 和 cached_repo 持有 domain.Cache 是同一个思路：
//   中间件不依赖具体实现，方便测试（传一个 Mock），也方便将来替换实现
func RateLimitMiddleware(limiter domain.RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 用客户端 IP 作为限流的 Key
		// c.ClientIP() 会自动处理 X-Forwarded-For 和 X-Real-IP 头，
		// 适用于服务在 Nginx / Load Balancer 后面的场景
		ip := c.ClientIP()
		key := "ratelimit:ip:" + ip

		allowed, err := limiter.Allow(c.Request.Context(), key)

		if err != nil {
			// ⚠️ Redis 故障时降级放行，而不是返回 500
			// 原则：限流是保护系统的，不是阻断服务的
			// 如果限流器自己挂了，宁愿放开请求，也不能因为限流器故障导致整个 API 不可用
			log.Printf("⚠️ [RateLimit] Redis error (degrading to allow): %v", err)
			c.Next()
			return
		}

		if !allowed {
			// 令牌不足，拒绝请求
			//
			// 为什么要设置 Retry-After 响应头？
			// → HTTP 规范规定 429 响应应该携带 Retry-After，
			//   告诉客户端多少秒后可以重试，而不是让客户端盲目轮询
			// → 值设为 1 秒（1 / rate ≈ 下一个令牌到达的时间）
			//   对于调试工具（curl、Postman）和规范的 HTTP 客户端都很友好
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "Too Many Requests",
				"message":     "Rate limit exceeded. Please slow down.",
				"retry_after": 1,
			})
			// 注意：这里用 AbortWithStatusJSON 而不是 return！
			// return 只是退出当前函数，后续中间件和 Handler 还会继续执行
			// Abort 会中断整个中间件链，后面的 Handler 不会被调用
			// 拒绝请求必须用 Abort，否则业务逻辑还是会执行
			return
		}

		// 令牌充足，放行请求，进入下一个中间件或 Handler
		c.Next()
	}
}
