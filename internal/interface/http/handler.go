package http

import (
	"net/http"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/ErinWang666/image-processor/internal/usecase"
	"github.com/gin-gonic/gin"
)

type ImageHandler struct {
	usecase *usecase.ImageUsecase
	storage domain.Storage
}

func NewImageHandler(u *usecase.ImageUsecase, s domain.Storage) *ImageHandler {
	return &ImageHandler{
		usecase: u,
		storage: s,
	}
}

// API 1: 申请上传 (生成 URL + 落库)
func (h *ImageHandler) HandleUploadRequest(c *gin.Context) {
	taskID := "img-" + time.Now().Format("20060102150405")
	objectKey := taskID + ".jpg"

	image := &domain.Image{
		ID:        taskID,
		UserID:    "user-999",
		Status:    domain.StatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// 1. 生成 URL
	uploadURL, err := h.storage.GetPresignedURL(c.Request.Context(), "images", objectKey, 15*time.Minute)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate upload link"})
		return
	}

	// 2. 调用 Usecase 仅仅落库，不再发队列！
	if err := h.usecase.CreateTask(c.Request.Context(), image); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save task to DB"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "Upload URL generated successfully",
		"image_id":   taskID,
		"status":     "PENDING",
		"upload_url": uploadURL,
	})
}

// 定义接收前端 Confirm 请求的结构体
type ConfirmRequest struct {
	ImageID string `json:"image_id" binding:"required"`
}

// API 2: 确认上传 (发队列唤醒 Worker)
func (h *ImageHandler) HandleConfirmRequest(c *gin.Context) {
	var req ConfirmRequest
	
	// 解析前端传过来的 JSON 数据
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body. 'image_id' is required."})
		return
	}

	// 调用 Usecase 发队列
	if err := h.usecase.ConfirmTask(c.Request.Context(), req.ImageID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to confirm task"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "Task confirmed. Worker is processing...",
		"image_id": req.ImageID,
	})
}

// API 3: 查询处理结果
func (h *ImageHandler) HandleGetImage(c *gin.Context) {
	imageID := c.Param("id") // 从 URL 路径里拿到 id

	img, err := h.usecase.GetImage(c.Request.Context(), imageID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Image not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"image_id":      img.ID,
		"status":        img.Status,
		"thumbnail_url": img.ThumbnailURL,
		"tags":          img.Tags, // <-- 就是这行！把标签吐给前端
	})
}

// SetupRouter 配置路由
func SetupRouter(handler *ImageHandler) *gin.Engine {
	r := gin.Default()

	api := r.Group("/api/v1")
	{
		api.POST("/images/upload", handler.HandleUploadRequest)
		api.POST("/images/confirm", handler.HandleConfirmRequest) // --- 新增的路由 ---
		api.GET("/images/:id", handler.HandleGetImage) // --- 新增的 GET 路由 ---
	}

	return r
}