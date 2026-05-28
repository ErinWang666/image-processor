# Image Processor

A production-ready, asynchronous image processing service built with Go.
Designed with clean architecture, distributed systems patterns, and production reliability in mind.

## ✅ What's Implemented

| Feature | Pattern / Tech | Why |
|---------|---------------|-----|
| Async image processing | **Transactional Outbox + SQS** | Atomically dispatch tasks without losing messages |
| Duplicate prevention | **Redis Distributed Lock** | One image processed by exactly one worker |
| Cache layer | **Redis Cache-Aside** + singleflight | Reduce DB load, prevent cache stampede |
| Rate limiting | **Redis Token Bucket (Lua)** | Per-IP distributed rate limiting |
| AI tagging | **Google Gemini Vision** | Auto-tag images on completion |
| Object storage | **MinIO (S3-compatible)** | Presigned URL upload flow |

## 🧪 Testing

```bash
# Unit tests (Usecase + HTTP Handler, no external services needed)
go test ./internal/usecase/... ./internal/interface/... -v

# Integration tests (requires Docker)
go test ./internal/infrastructure/persistence/... -v
```

| Layer | Type | Count |
|-------|------|-------|
| Usecase | Unit (gomock) | 8 |
| HTTP Handler | Unit (httptest + gomock) | 6 |
| PostgreSQL Repository | Integration (testcontainers) | 5 |

## 🛠️ Tech Stack

- **Language**: Go
- **Database**: PostgreSQL (GORM)
- **Cache / Lock / Rate Limit**: Redis
- **Message Queue**: AWS SQS (ElasticMQ for local dev)
- **Storage**: MinIO (S3-compatible)
- **AI**: Google Gemini Pro Vision
- **Testing**: gomock, testify, testcontainers-go

## 🏗️ Architecture

```
HTTP Request
    │
    ▼
RateLimitMiddleware (Redis Token Bucket)
    │
    ▼
Handler → Usecase → CachedRepository ──► Redis
                          │
                          └──────────────► PostgreSQL
                    │
                    ▼
              Outbox Relay → SQS → Worker → AI Tagging → MinIO
```

The service follows **clean architecture**: Domain → Usecase → Infrastructure, with all dependencies flowing inward via interfaces.

## 🚦 Local Development

```bash
# 1. Start infrastructure (PostgreSQL, Redis, MinIO, ElasticMQ)
docker-compose up -d

# 2. Set your API key
cp .env.example .env   # fill in GEMINI_API_KEY

# 3. Run the server
go run cmd/server/main.go
```

## 🔑 Key Design Decisions

- **Decorator Pattern** for the cache layer: `CachedRepository` wraps `PostgresRepository`, both implement `ImageRepository`. Zero changes to Usecase layer.
- **singleflight** prevents cache stampede: when a hot key expires, only 1 DB query fires regardless of concurrent requests.
- **Lua script** for rate limiting: ensures "read token → decrement → write" is atomic across multiple service instances.
- **Optimistic locking** in `UpdateResult`: `WHERE status = 'PROCESSING'` prevents a completed task from being overwritten by a slow duplicate worker.
