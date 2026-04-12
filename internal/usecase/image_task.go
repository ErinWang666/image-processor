package usecase

import (
	"bytes" // 新增
	"context"
	"fmt"

	// "io"
	"log"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/disintegration/imaging" // 新增
)

// ImageUsecase 核心处理逻辑
type ImageUsecase struct {
	repo    domain.ImageRepository
	queue   domain.Queue
	storage domain.Storage
	ai      domain.AIService
	locker  domain.TaskLocker // <-- 🌟 新增：引入分布式锁防线
}

// NewImageUsecase 构造函数
func NewImageUsecase(
	r domain.ImageRepository,
	q domain.Queue,
	s domain.Storage,
	a domain.AIService,
	l domain.TaskLocker, // <-- 🌟 新增：接收外部注入的锁实例
) *ImageUsecase {
	return &ImageUsecase{
		repo:    r,
		queue:   q,
		storage: s,
		ai:      a,
		locker:  l, // <-- 🌟 赋值
	}
}

// --- 改造 1：只负责存数据库，不发队列 ---
func (u *ImageUsecase) CreateTask(ctx context.Context, img *domain.Image) error {
	log.Printf("📝 [Usecase] Saving pending task to DB: %s", img.ID)
	if err := u.repo.Save(ctx, img); err != nil {
		return fmt.Errorf("repo save failed: %w", err)
	}
	return nil
}

// --- 改造 2：前端传完文件后，调这个方法 ---
// 现在的逻辑：同生共死，只写数据库，不发队列
func (u *ImageUsecase) ConfirmTask(ctx context.Context, imageID string) error {
	log.Printf("🚀 [Usecase] Confirming task and committing to Outbox: %s", imageID)
	
	taskPayload := domain.TaskPayload{
		ImageID:   imageID,
		UserID:    "user-999", 
		EventType: "image_uploaded",
	}

	// 🚨 核心改动：不再直接调用 u.queue.Publish
	// 而是调用我们在 Repo 里准备好的事务方法
	// 它会：1. 把 images 表状态改完 PENDING； 2. 在 outbox_messages 表存入 taskPayload
	topic := "image_processing_queue"
	err := u.repo.UpdateStatusWithOutbox(ctx, imageID, domain.StatusPending, topic, taskPayload)
	if err != nil {
		return fmt.Errorf("transactional outbox failed: %w", err)
	}

	return nil
}

func (u *ImageUsecase) ProcessImage(ctx context.Context, task domain.TaskPayload) error {
	log.Printf("🔄 [Usecase] Start processing image: %s", task.ImageID)

	// ==========================================
	// 🛡️ 防线一：尝试获取 Redis 分布式锁
	// ==========================================
	lockKey := fmt.Sprintf("task:lock:%s", task.ImageID)
	
	// 🌟 修改点 1：接收返回的 token
	token, acquired, err := u.locker.Acquire(ctx, lockKey, 10*time.Minute)
	if err != nil {
		return fmt.Errorf("failed to check lock: %w", err)
	}
	if !acquired {
		log.Printf("⚠️ [Worker] Task %s is already being processed. Dropping duplicate message.", task.ImageID)
		return nil
	}
	
	// 🌟 修改点 2：在 defer 释放锁时，必须交出这把钥匙 (token)
	defer u.locker.Release(ctx, lockKey, token)
	// ==========================================


	if err := u.repo.UpdateStatus(ctx, task.ImageID, domain.StatusProcessing); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// 1. 从 MinIO 下载原图
	fileKey := task.ImageID + ".jpg"
	fileStream, err := u.storage.Download(ctx, "images", fileKey)
	if err != nil {
		u.repo.UpdateStatus(ctx, task.ImageID, domain.StatusFailed)
		return fmt.Errorf("failed to download from minio: %w", err)
	}
	defer fileStream.Close()

	// 2. 解码图像
	log.Println("⚙️ [Image Processor] Decoding image...")
	srcImage, err := imaging.Decode(fileStream)
	if err != nil {
		u.repo.UpdateStatus(ctx, task.ImageID, domain.StatusFailed)
		return fmt.Errorf("failed to decode image: %w", err)
	}

	// 3. 执行处理
	log.Println("⚙️ [Image Processor] Resizing image to width 800px...")
	dstImage := imaging.Resize(srcImage, 800, 0, imaging.Lanczos)

	// 4. 将处理后的图片编码进内存缓冲区
	buf := new(bytes.Buffer)
	err = imaging.Encode(buf, dstImage, imaging.JPEG)
	if err != nil {
		u.repo.UpdateStatus(ctx, task.ImageID, domain.StatusFailed)
		return fmt.Errorf("failed to encode processed image: %w", err)
	}

	// 🧠 呼叫 AI 进行识图打标签
	log.Println("🧠 [AI] Analyzing image and generating tags...")
	tags, err := u.ai.GenerateTags(ctx, buf.Bytes())
	if err != nil {
		log.Printf("⚠️ [AI] Failed to generate tags (continuing without tags): %v", err)
		tags = []string{"AI识别失败"}
	} else {
		log.Printf("✨ [AI] Successfully generated tags: %v", tags)
	}

	// 5. 将处理好的新图片回传到 MinIO
	processedKey := "processed/" + task.ImageID + "-thumb.jpg"
	log.Printf("📤 [Image Processor] Uploading processed image to MinIO as: %s", processedKey)
	
	fileReader := bytes.NewReader(buf.Bytes())
	err = u.storage.Upload(ctx, "images", processedKey, fileReader)
	if err != nil {
		u.repo.UpdateStatus(ctx, task.ImageID, domain.StatusFailed)
		return fmt.Errorf("failed to upload processed image: %w", err)
	}

	// 6. --- 终极闭环：将新图片的 URL 存入数据库 ---
	finalURL := fmt.Sprintf("http://localhost:9000/images/%s", processedKey)
	log.Printf("💾 [Usecase] Saving result to DB. Thumbnail URL: %s", finalURL)
	
	// ==========================================
	// 🛡️ 防线二：底层带有状态卫哨的乐观锁更新
	// ==========================================
	if err := u.repo.UpdateResult(ctx, task.ImageID, domain.StatusCompleted, finalURL, tags); err != nil {
		// 如果底层的 postgres.go 报了 "optimistic lock failed (RowsAffected = 0)"
		// 说明在我们干活的期间，有人篡改了数据库状态。这属于严重的并发冲突，直接报错。
		return fmt.Errorf("failed to complete task and save URL: %w", err)
	}

	log.Printf("✅ [Usecase] Task finished successfully: %s", task.ImageID)
	return nil
}

// GetImage 获取图片处理状态和结果
func (u *ImageUsecase) GetImage(ctx context.Context, id string) (*domain.Image, error) {
	// 这里其实需要 Repo 层有个 GetByID 的方法，如果你的 postgres.go 里还没有，
	// 等下我们在 Repo 里补上这句： r.db.Where("id = ?", id).First(&img)
	return u.repo.GetByID(ctx, id)
}