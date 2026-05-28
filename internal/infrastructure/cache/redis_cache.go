package cache

import (
	"context"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/redis/go-redis/v9"
)

// RedisCache 是 domain.Cache 接口的 Redis 实现
//
// 你可以把它和 lock/redis_lock.go 对比着看，结构几乎一模一样：
//   - 都持有一个 *redis.Client
//   - 都通过构造函数返回 domain 层的接口类型
//   - 都在 infrastructure 层，属于"基础设施的具体实现"
//
// 这就是 Clean Architecture 的套路：每个基础设施能力都是同一个模板
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache 构造函数
// 参数是 *redis.Client（和 NewRedisLocker 共享同一个连接），返回值是 domain.Cache 接口
//
// 为什么返回接口而不是 *RedisCache？
// → 这样调用方拿到的是 domain.Cache 类型，完全不知道底层是 Redis
//   如果将来要换成 Memcached，只需要写一个新的 struct 实现 domain.Cache，
//   然后在 main.go 里换一行构造函数就行，其他代码都不用动
func NewRedisCache(client *redis.Client) domain.Cache {
	return &RedisCache{client: client}
}

// Get 从 Redis 读取缓存
//
// 这里最关键的设计是对 redis.Nil 的处理：
//
//	redis.Nil 是 go-redis 库专有的错误，表示"Key 不存在"
//	但是在我们的 domain.Cache 接口里，"Key 不存在"不是错误，而是正常的 Cache Miss
//	所以我们把它翻译成 (nil, false, nil) —— 没数据、没命中、没错误
//
//	这个翻译动作就是 infrastructure 层的职责：
//	把第三方库的细节（redis.Nil）转换成业务层的通用语义（bool）
func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := c.client.Get(ctx, key).Bytes()

	if err == redis.Nil {
		// Key 不存在 = Cache Miss，不是错误
		return nil, false, nil
	}
	if err != nil {
		// 网络超时、连接池耗尽等真正的系统故障
		return nil, false, err
	}

	// Cache Hit ✅
	return val, true, nil
}

// Set 将数据写入 Redis 并设置过期时间
//
// 底层就是 Redis 的 SET key value EX seconds 命令
// ttl 参数控制这条数据在 Redis 里活多久，过期后 Redis 自动删除
//
// 为什么一定要设 TTL？
// → 如果不设 TTL，万一删缓存的操作失败了（比如网络闪断），
//   这条旧数据就会永远留在 Redis 里，导致永久性的数据不一致
//   设了 TTL 就相当于加了一道保险：就算删除失败，最多不一致 10 分钟
func (c *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

// Delete 删除 Redis 中的缓存键
//
// 用于 Cache-Aside 写路径：数据库更新成功后，删掉旧的缓存
// 这样下次查询就会 Cache Miss → 从 DB 加载最新数据 → 回填缓存
func (c *RedisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}
