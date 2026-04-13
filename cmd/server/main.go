package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/ErinWang666/image-processor/internal/infrastructure/ai"
	"github.com/ErinWang666/image-processor/internal/infrastructure/lock"
	"github.com/ErinWang666/image-processor/internal/infrastructure/persistence"
	"github.com/ErinWang666/image-processor/internal/infrastructure/queue"
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

	// 6. 依赖注入 (Dependency Injection)
	repo := persistence.NewPostgresRepository(db)
	
	// 🌩️ ========================================================
	// 🌩️ [NEW] 核心改动：使用 AWS SQS 驱动替代 Redis 驱动
	// ========================================================
	log.Println("🌩️ [Step 6] Initializing AWS SQS Queue (ElasticMQ)...")
	msgQueue, err := queue.NewSQSQueue(context.Background(), "image_processing_queue")
	if err != nil {
		log.Fatalf("❌ Failed to initialize SQS queue: %v", err)
	}

	// 🌟 实例化 Redis 分布式锁 (锁依然用 Redis，互不干扰)
	taskLocker := lock.NewRedisLocker(rdb)

	imageUsecase := usecase.NewImageUsecase(repo, msgQueue, storageClient, aiClient, taskLocker)

	// 7. 启动后台 Worker (消费者)
	log.Println("👷 [Worker] Starting background workers with SQS...")
	// SQS 驱动的 Subscribe 会自动处理长轮询和删除确认
	tasks, err := msgQueue.Subscribe(context.Background(), "image_processing_queue")
	if err != nil {
		log.Fatalf("❌ Failed to subscribe to SQS queue: %v", err)
	}

	const WorkerCount = 3 // SQS 长轮询比较占连接，初期可以少开几个
	for i := 0; i < WorkerCount; i++ {
		go func(workerID int) {
			log.Printf("👷 SQS Worker %d started", workerID)
			for task := range tasks {
				err := imageUsecase.ProcessImage(context.Background(), task)
				if err != nil {
					log.Printf("❌ [Worker %d] Failed to process task %s: %v", workerID, task.ImageID, err)
				}
			}
		}(i)
	}

	// 🌟 启动事务性发件箱 (Outbox Relay)
	go imageUsecase.StartOutboxRelay(context.Background())

	log.Println("✨ [Step 7] All components running. Waiting for requests...")

	// 8. 启动 HTTP API 服务器
	log.Println("🌐 [Step 8] Starting HTTP API Server...")
	imageHandler := http_interface.NewImageHandler(imageUsecase, storageClient)
	router := http_interface.SetupRouter(imageHandler)

	log.Println("🚀 API Server listening on http://localhost:8080")
	if err := router.Run(":8080"); err != nil {
		log.Fatalf("❌ Failed to start server: %v", err)
	}
}