package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/ErinWang666/image-processor/internal/infrastructure/ai"
	"github.com/ErinWang666/image-processor/internal/infrastructure/lock" // 🌟 新增：引入锁的包
	"github.com/ErinWang666/image-processor/internal/infrastructure/persistence"
	"github.com/ErinWang666/image-processor/internal/infrastructure/queue"
	"github.com/ErinWang666/image-processor/internal/infrastructure/storage"
	http_interface "github.com/ErinWang666/image-processor/internal/interface/http"
	"github.com/ErinWang666/image-processor/internal/usecase"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. 程序启动
	log.Println("🚀 [Step 1] Program started. Initializing...")

	// 2. 初始化 Postgres
	log.Println("🔌 [Step 2] Connecting to PostgreSQL...")
	dsn := "host=localhost user=user password=password dbname=image_processor port=5432 sslmode=disable"
	db, err := persistence.InitDB(dsn)
	if err != nil {
		log.Fatalf("❌ Failed to connect to database: %v", err)
	}
	log.Println("✅ [Step 2] PostgreSQL connected!")

	// 3. 初始化 Redis
	log.Println("🔌 [Step 3] Connecting to Redis...")
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	// 测试 Redis 连接
	ctxRedis, cancelRedis := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRedis()
	if err := rdb.Ping(ctxRedis).Err(); err != nil {
		log.Fatalf("❌ Failed to connect to redis: %v", err)
	}
	log.Println("✅ [Step 3] Redis connected!")

	// 4. 初始化 MinIO (对象存储)
	log.Println("🪣 [Step 4] Connecting to MinIO Storage...")
	storageClient, err := storage.NewS3Storage()
	if err != nil {
		log.Fatalf("❌ Failed to connect to storage: %v", err)
	}
	log.Println("✅ [Step 4] MinIO Storage connected!")

	// 🧪 测试 MinIO
	testKey := "test-upload-" + time.Now().Format("150405") + ".jpg"
	url, err := storageClient.GetPresignedURL(context.Background(), "images", testKey, 15*time.Minute)
	if err != nil {
		log.Printf("⚠️ Failed to generate presigned URL: %v", err)
	} else {
		log.Printf("🔗 [Test] Generated Presigned Upload URL: %s", url)
	}

	// 4.5 初始化 Gemini AI
	log.Println("🧠 [Step 4.5] Connecting to Gemini AI...")
	// ✅ 专业写法：提示这是需要配置的
	geminiKey := os.Getenv("GEMINI_API_KEY") 
	if geminiKey == "" {
		// 为了本地不报错，你可以先给个假字符串，或者直接 log.Fatal 提示配置环境变量
		geminiKey = "your_gemini_api_key_here" 
	}
	aiClient, err := ai.NewGeminiService(context.Background(), geminiKey)
	if err != nil {
		log.Fatalf("❌ Failed to initialize AI service: %v", err)
	}
	log.Println("✅ [Step 4.5] Gemini AI connected!")

	// 5. 自动迁移
	log.Println("🛠️ [Step 5] Migrating database schema...")
	db.AutoMigrate(&domain.Image{}, &domain.OutboxMessage{}) // <-- 加上 OutboxMessage

	// 6. 依赖注入 (Dependency Injection)
	repo := persistence.NewPostgresRepository(db)
	msgQueue := queue.NewRedisQueue(rdb)
	
	// 🌟 新增：实例化 Redis 分布式锁
	taskLocker := lock.NewRedisLocker(rdb)

	// 🌟 修改：把 taskLocker 作为第五个参数传进去
	imageUsecase := usecase.NewImageUsecase(repo, msgQueue, storageClient, aiClient, taskLocker)

	// 7. 启动后台 Worker (消费者)
	log.Println("👷 [Worker] Starting background workers...")
	tasks, err := msgQueue.Subscribe(context.Background(), "image_processing_queue")
	if err != nil {
		log.Fatalf("❌ Failed to subscribe to queue: %v", err)
	}

	const WorkerCount = 5
	for i := 0; i < WorkerCount; i++ {
		go func(workerID int) {
			log.Printf("👷 Worker %d started", workerID)
			for task := range tasks {
				err := imageUsecase.ProcessImage(context.Background(), task)
				if err != nil {
					log.Printf("❌ [Worker %d] Failed to process task %s: %v", workerID, task.ImageID, err)
				}
			}
		}(i)
	}

	// 🌟 ========================================================
	// 🌟 启动咱们的事务性发件箱 (Outbox Relay) 清道夫协程！
	go imageUsecase.StartOutboxRelay(context.Background())
	// 🌟 ========================================================

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