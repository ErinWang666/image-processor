package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/joho/godotenv"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/ErinWang666/image-processor/internal/infrastructure/ai"
	"github.com/ErinWang666/image-processor/internal/infrastructure/cache"
	"github.com/ErinWang666/image-processor/internal/infrastructure/lock"
	"github.com/ErinWang666/image-processor/internal/infrastructure/persistence"
	"github.com/ErinWang666/image-processor/internal/infrastructure/queue"
	"github.com/ErinWang666/image-processor/internal/infrastructure/ratelimit"
	"github.com/ErinWang666/image-processor/internal/infrastructure/storage"
	http_interface "github.com/ErinWang666/image-processor/internal/interface/http"
	"github.com/ErinWang666/image-processor/internal/usecase"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 0. 程序启动前，先加载 .env 文件里的机密配置
	log.Println("🚀 [Step 0] Loading environment variables...")
	if err := godotenv.Load(); err != nil {
		// 这里用 Println 即可，因为在真实的生产服务器（如 Docker）上，
		// 我们是不传 .env 文件的，而是直接在容器里配环境变量。
		log.Println("⚠️ No .env file found or error loading it. Relying on system environment variables.")
	}

	// 1. 程序启动
	log.Println("🚀 [Step 1] Program started. Initializing...")

	// 2. 初始化 Postgres
	log.Println("🔌 [Step 2] Connecting to PostgreSQL...")
	dsn := "host=localhost user=user password=password dbname=image_processor port=5432 sslmode=disable"
	db, err := persistence.InitDB(dsn)
	if err != nil {
		log.Fatalf("❌ Failed to connect to database: %v", err)
	}

	// 3. 初始化 Redis (🌟 保留：用于分布式锁)
	log.Println("🔌 [Step 3] Connecting to Redis...")
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	ctxRedis, cancelRedis := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRedis()
	if err := rdb.Ping(ctxRedis).Err(); err != nil {
		log.Fatalf("❌ Failed to connect to redis: %v", err)
	}

	// 4. 初始化 MinIO
	log.Println("🪣 [Step 4] Connecting to MinIO Storage...")
	storageClient, err := storage.NewS3Storage()
	if err != nil {
		log.Fatalf("❌ Failed to connect to storage: %v", err)
	}

	// 4.5 初始化 Gemini AI
	log.Println("🧠 [Step 4.5] Connecting to Gemini AI...")
	geminiKey := os.Getenv("GEMINI_API_KEY") 
	if geminiKey == "" {
		geminiKey = "your_gemini_api_key_here" 
	}
	aiClient, err := ai.NewGeminiService(context.Background(), geminiKey)
	if err != nil {
		log.Fatalf("❌ Failed to initialize AI service: %v", err)
	}

	// 5. 自动迁移
	log.Println("🛠️ [Step 5] Migrating database schema...")
	db.AutoMigrate(&domain.Image{}, &domain.OutboxMessage{})

	// 6. 依赖注入
	// 6.1 创建底层 Postgres Repo（直接操作数据库）
	postgresRepo := persistence.NewPostgresRepository(db)

	// 6.2 创建 Redis 缓存实例（复用上面 Step 3 已经连好的 rdb）
	redisCache := cache.NewRedisCache(rdb)

	// 6.3 用装饰器包装：CachedRepository 在 GetByID 前先查 Redis，
	//     在 UpdateResult 后删 Redis，对 Usecase 层完全透明
	repo := persistence.NewCachedRepository(postgresRepo, redisCache)
	
	log.Println("🌩️ [Step 6] Initializing AWS SQS Queue (ElasticMQ)...")
	msgQueue, err := queue.NewSQSQueue(context.Background(), "image_processing_queue")
	if err != nil {
		log.Fatalf("❌ Failed to initialize SQS queue: %v", err)
	}
	taskLocker := lock.NewRedisLocker(rdb)
	imageUsecase := usecase.NewImageUsecase(repo, msgQueue, storageClient, aiClient, taskLocker)

	// 限流器：容量 10（最大突发），每秒补充 2 个令牌（稳定上限）
	// 复用已有的 rdb 连接，不需要再建新的 Redis 连接
	rateLimiter := ratelimit.NewRedisRateLimiter(rdb, 10, 2.0)

	// 🌟🌟 核心改动：创建一个监听系统退出信号 (Ctrl+C) 的全局 Context 🌟🌟
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer stop() // 确保函数退出时释放资源

	// 7. 启动后台 Worker (消费者) - 🌟 注意这里传入的是全局 ctx
	log.Println("👷 [Worker] Starting background workers with SQS...")
	tasks, err := msgQueue.Subscribe(ctx, "image_processing_queue")
	if err != nil {
		log.Fatalf("❌ Failed to subscribe to SQS queue: %v", err)
	}

	const WorkerCount = 3 
	for i := 0; i < WorkerCount; i++ {
		go func(workerID int) {
			log.Printf("👷 SQS Worker %d started", workerID)
			for task := range tasks {
				// 🌟 传入全局 ctx，如果系统正在关机，业务逻辑也可以感知到
				err := imageUsecase.ProcessImage(ctx, task)
				if err != nil {
					log.Printf("❌ [Worker %d] Failed to process task %s: %v", workerID, task.ImageID, err)
				}
			}
			log.Printf("🛑 SQS Worker %d shut down gracefully.", workerID)
		}(i)
	}

	// 🌟 启动事务性发件箱 (Outbox Relay) - 传入全局 ctx
	go imageUsecase.StartOutboxRelay(ctx)

	log.Println("✨ [Step 7] All components running. Waiting for requests...")

	// 8. 启动 HTTP API 服务器
	log.Println("🌐 [Step 8] Starting HTTP API Server...")
	imageHandler := http_interface.NewImageHandler(imageUsecase, storageClient)
	// 把限流器传给 SetupRouter，由路由层决定挂在哪个路由组上
	router := http_interface.SetupRouter(imageHandler, rateLimiter)

	srv := &http.Server{
		Addr:    ":8080",
		Handler: router,
	}

	// 🌟 把启动 HTTP 服务器放到单独的 goroutine 里，不要阻塞主进程
	go func() {
		log.Println("🚀 API Server listening on http://localhost:8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ Failed to start server: %v", err)
		}
	}()

	// 🌟🌟 优雅停机核心拦截：死死卡住主进程，直到收到 Ctrl+C 信号 🌟🌟
	<-ctx.Done()
	log.Println("\n⚠️  [Shutdown] Received interrupt signal. Initiating graceful shutdown...")

	// 给 HTTP 服务器最多 5 秒钟的时间处理完现有的请求
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("❌ [Shutdown] Server forced to shutdown: %v", err)
	}

	// 注意：我们的 SQS Subscribe 函数里也监听了 ctx.Done()，
	// 所以 Worker 会自然把手头的 task 处理完，然后由于 tasks channel 关闭而退出 for 循环。

	log.Println("✅ [Shutdown] Graceful shutdown completed. Goodbye!")
}