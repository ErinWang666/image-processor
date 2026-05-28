package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/redis/go-redis/v9"
)

// ==========================================
// 🪣 令牌桶 Lua 脚本（核心）
// ==========================================
//
// 为什么用 Lua 脚本而不是普通的 Go 代码 + 多次 Redis 命令？
//
// 假设不用 Lua，分成三步：
//   Step1: GET tokens   → 读到当前令牌数 = 1
//   Step2: 判断 >= 1，可以通过
//   Step3: SET tokens 0  → 扣减
//
// 问题：Step1 和 Step3 之间有时间窗口，另一个实例可能也读到 tokens=1，
// 两个请求都通过了，但桶里实际只有 1 个令牌 → 限流形同虚设！
//
// Lua 脚本在 Redis 里是原子执行的，整段脚本相当于一条命令，
// 不会被其他 Redis 命令插入，彻底消除竞态条件。
// 这和你的分布式锁用 Lua 做 Compare-And-Delete 是完全相同的思路。
//
// 脚本逻辑：
//   1. 读取当前令牌数 和 上次更新时间戳
//   2. 计算距上次更新过了多少秒 → 按速率补充令牌（不超过桶容量）
//   3. 令牌够 → 扣 1 个，返回 1（允许）
//   4. 令牌不够 → 不扣，返回 0（拒绝）
//   5. 写回令牌数和时间戳，设置 TTL（防止 Key 永久占用 Redis 内存）
//
// KEYS[1] = 限流的 Key（如 "ratelimit:ip:192.168.1.1"）
// ARGV[1] = 桶的容量 capacity（最多存多少令牌）
// ARGV[2] = 每秒补充速率 rate
// ARGV[3] = 当前时间戳（秒）
// ARGV[4] = Key 的过期时间 TTL（秒）
const tokenBucketLuaScript = `
local key      = KEYS[1]
local capacity = tonumber(ARGV[1])
local rate     = tonumber(ARGV[2])
local now      = tonumber(ARGV[3])
local ttl      = tonumber(ARGV[4])

-- 读取当前桶状态（令牌数 和 上次更新时间）
local tokens_key    = key .. ":tokens"
local timestamp_key = key .. ":ts"

local current_tokens = tonumber(redis.call("GET", tokens_key))
local last_time      = tonumber(redis.call("GET", timestamp_key))

-- 第一次请求：桶是满的
if current_tokens == nil then
    current_tokens = capacity
    last_time      = now
end

-- 按速率补充令牌：距上次更新的秒数 × 速率 = 应补充的令牌数
local elapsed  = math.max(0, now - last_time)
local refill   = math.floor(elapsed * rate)
current_tokens = math.min(capacity, current_tokens + refill)

-- 判断令牌是否充足
local allowed = 0
if current_tokens >= 1 then
    current_tokens = current_tokens - 1
    allowed = 1
end

-- 写回状态，并设置 TTL 防止内存泄漏
redis.call("SET", tokens_key,    current_tokens, "EX", ttl)
redis.call("SET", timestamp_key, now,            "EX", ttl)

return allowed
`

// RedisRateLimiter 是 domain.RateLimiter 的 Redis + 令牌桶实现
type RedisRateLimiter struct {
	client   *redis.Client
	capacity int           // 桶的容量（允许的最大突发请求数）
	rate     float64       // 每秒补充的令牌数（稳定流量上限）
	ttl      time.Duration // Redis Key 的过期时间
}

// NewRedisRateLimiter 构造函数
//
// capacity = 10, rate = 2.0 的含义：
//   → 突发最多允许 10 个并发请求同时通过（桶容量）
//   → 长期稳定的流量上限是每秒 2 个请求（令牌补充速率）
//   → 效果：正常用户偶尔高峰没问题，持续刷接口会被限流
//
// 为什么返回 domain.RateLimiter 接口？
// → 和 NewRedisCache、NewRedisLocker 一模一样的套路：
//   main.go 拿到接口，中间件只认接口，底层可以随时替换实现
func NewRedisRateLimiter(client *redis.Client, capacity int, rate float64) domain.RateLimiter {
	return &RedisRateLimiter{
		client:   client,
		capacity: capacity,
		rate:     rate,
		ttl:      time.Duration(capacity)*time.Second*2 + 10*time.Second, // 保证桶能补满的时间 + 余量
	}
}

// Allow 执行令牌桶限流检查
//
// 整个方法只做一件事：拼参数，执行 Lua 脚本，解读返回值
// 所有复杂的令牌计算逻辑都在 Lua 脚本里（原子执行）
func (r *RedisRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	now := time.Now().Unix() // 当前 Unix 时间戳（秒）
	ttlSecs := int(r.ttl.Seconds())

	// Eval 执行 Lua 脚本：
	//   第一个参数：脚本内容
	//   第二个参数：KEYS 数组（脚本里用 KEYS[1] 访问）
	//   后面的参数：ARGV 数组（脚本里用 ARGV[1]、ARGV[2]... 访问）
	result, err := r.client.Eval(ctx, tokenBucketLuaScript,
		[]string{key},                                   // KEYS
		r.capacity, fmt.Sprintf("%.4f", r.rate), now, ttlSecs, // ARGV
	).Int64()

	if err != nil {
		// Redis 故障：返回 error，由调用方（中间件）决定如何降级
		// 不在这里直接 return true，因为"要不要降级"是业务决策，不是基础设施决策
		return false, fmt.Errorf("rate limiter redis error: %w", err)
	}

	// Lua 脚本返回 1 = 允许，0 = 拒绝
	return result == 1, nil
}
