package persistence

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"golang.org/x/sync/singleflight"
)

const (
	cacheKeyPrefix = "cache:image:"
	cacheTTL       = 10 * time.Minute
)

// CachedRepository 装饰器模式：包装 PostgresRepository，加上 Cache-Aside 缓存逻辑
// 同样实现 domain.ImageRepository 接口，对 Usecase 层完全透明
type CachedRepository struct {
	inner   domain.ImageRepository
	cache   domain.Cache
	sfGroup singleflight.Group // 防缓存击穿：同一 Key 的并发请求只有 1 个去查 DB
}

// NewCachedRepository 构造函数
func NewCachedRepository(inner domain.ImageRepository, cache domain.Cache) domain.ImageRepository {
	return &CachedRepository{
		inner: inner,
		cache: cache,
	}
}

// GetByID — Cache-Aside 读路径
// 流程：① 查 Redis → 命中直接返回；② Miss 时用 singleflight 查 DB；③ 回填 Redis
func (r *CachedRepository) GetByID(ctx context.Context, id string) (*domain.Image, error) {
	cacheKey := cacheKeyPrefix + id

	// 第一步：查缓存
	data, hit, err := r.cache.Get(ctx, cacheKey)
	if err != nil {
		// Redis 故障时降级到 DB，而不是报错
		log.Printf("⚠️ [Cache] Redis GET error (degrading to DB): %v", err)
	}

	if hit {
		log.Printf("✅ [Cache] HIT for key: %s", cacheKey)
		var img domain.Image
		if err := json.Unmarshal(data, &img); err != nil {
			// JSON 损坏，删掉脏数据，回退到 DB
			log.Printf("⚠️ [Cache] Corrupted cache, falling through to DB: %v", err)
			r.cache.Delete(ctx, cacheKey)
		} else {
			return &img, nil
		}
	}

	// 第二步：singleflight 防击穿，确保同一 Key 同时只有 1 个请求打到 DB
	log.Printf("❌ [Cache] MISS for key: %s, querying DB...", cacheKey)
	result, err, _ := r.sfGroup.Do(cacheKey, func() (interface{}, error) {
		img, err := r.inner.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		// 第三步：回填缓存
		if jsonData, e := json.Marshal(img); e == nil {
			if setErr := r.cache.Set(ctx, cacheKey, jsonData, cacheTTL); setErr != nil {
				log.Printf("⚠️ [Cache] Failed to SET (non-fatal): %v", setErr)
			} else {
				log.Printf("📝 [Cache] Cached key: %s (TTL: %v)", cacheKey, cacheTTL)
			}
		}
		return img, nil
	})

	if err != nil {
		return nil, err
	}
	return result.(*domain.Image), nil
}

// UpdateResult — Cache-Aside 写路径：先更新 DB，再删缓存
func (r *CachedRepository) UpdateResult(ctx context.Context, id string, status domain.ImageStatus, thumbnailURL string, tags []string) error {
	if err := r.inner.UpdateResult(ctx, id, status, thumbnailURL, tags); err != nil {
		return err
	}
	cacheKey := cacheKeyPrefix + id
	if err := r.cache.Delete(ctx, cacheKey); err != nil {
		log.Printf("⚠️ [Cache] Failed to invalidate after UpdateResult (TTL will handle it): %v", err)
	} else {
		log.Printf("🗑️ [Cache] Invalidated cache: %s", cacheKey)
	}
	return nil
}

// UpdateStatus — 状态变更后也要删缓存，防止返回旧状态
func (r *CachedRepository) UpdateStatus(ctx context.Context, id string, status domain.ImageStatus) error {
	err := r.inner.UpdateStatus(ctx, id, status)
	if err == nil {
		r.cache.Delete(ctx, cacheKeyPrefix+id)
	}
	return err
}

func (r *CachedRepository) UpdateStatusWithOutbox(ctx context.Context, id string, status domain.ImageStatus, topic string, payload interface{}) error {
	err := r.inner.UpdateStatusWithOutbox(ctx, id, status, topic, payload)
	if err == nil {
		r.cache.Delete(ctx, cacheKeyPrefix+id)
	}
	return err
}

// 以下方法纯透传，和缓存无关
func (r *CachedRepository) Save(ctx context.Context, img *domain.Image) error {
	return r.inner.Save(ctx, img)
}

func (r *CachedRepository) GetPendingOutboxMessages(ctx context.Context, limit int) ([]*domain.OutboxMessage, error) {
	return r.inner.GetPendingOutboxMessages(ctx, limit)
}

func (r *CachedRepository) MarkOutboxAsSent(ctx context.Context, id string) error {
	return r.inner.MarkOutboxAsSent(ctx, id)
}
