package http_test // ← 注意：用 http_test 而不是 http，这是"黑盒测试"写法
//   只能调用 Handler 包的公开方法，不能访问内部变量

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ErinWang666/image-processor/internal/domain"
	httphandler "github.com/ErinWang666/image-processor/internal/interface/http"
	"github.com/ErinWang666/image-processor/internal/interface/http/mocks"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// setupTestRouter 创建一个测试用的路由
// 注意：不用 SetupRouter，因为它会挂载限流中间件，测试里不需要
func setupTestRouter(uc *mocks.MockImageUsecase, st *mocks.MockStorage) *gin.Engine {
	gin.SetMode(gin.TestMode) // 关掉 Gin 的调试输出
	h := httphandler.NewImageHandler(uc, st)
	r := gin.New()
	api := r.Group("/api/v1")
	api.POST("/images/upload", h.HandleUploadRequest)
	api.POST("/images/confirm", h.HandleConfirmRequest)
	api.GET("/images/:id", h.HandleGetImage)
	return r
}

// 测试一：HandleUploadRequest 正常路径
func TestHandleUploadRequest_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockUC := mocks.NewMockImageUsecase(ctrl)
	mockST := mocks.NewMockStorage(ctrl)

	// 期望：storage.GetPresignedURL 会被调用一次，返回一个假 URL
	mockST.EXPECT().
		GetPresignedURL(gomock.Any(), "images", gomock.Any(), gomock.Any()).
		Return("https://fake-url.com/upload", nil).
		Times(1)

	// 期望：usecase.CreateTask 会被调用一次，返回 nil（成功）
	mockUC.EXPECT().
		CreateTask(gomock.Any(), gomock.Any()).
		Return(nil).
		Times(1)

	r := setupTestRouter(mockUC, mockST)

	req := httptest.NewRequest("POST", "/api/v1/images/upload", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	assert.Equal(t, "https://fake-url.com/upload", body["upload_url"])
	assert.Equal(t, "PENDING", body["status"])
}

// 测试二：HandleUploadRequest 存储失败
func TestHandleUploadRequest_StorageFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockUC := mocks.NewMockImageUsecase(ctrl)
	mockST := mocks.NewMockStorage(ctrl)

	// storage 报错，后续的 CreateTask 不应该被调用
	mockST.EXPECT().
		GetPresignedURL(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return("", errors.New("minio down")).
		Times(1)

	// Times(0) 表示期望 CreateTask 一次都不被调用
	mockUC.EXPECT().CreateTask(gomock.Any(), gomock.Any()).Times(0)

	r := setupTestRouter(mockUC, mockST)
	req := httptest.NewRequest("POST", "/api/v1/images/upload", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// 测试三：HandleConfirmRequest 正常路径
func TestHandleConfirmRequest_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockUC := mocks.NewMockImageUsecase(ctrl)
	mockST := mocks.NewMockStorage(ctrl)

	mockUC.EXPECT().
		ConfirmTask(gomock.Any(), "img-123").
		Return(nil).
		Times(1)

	r := setupTestRouter(mockUC, mockST)

	// 构造 JSON 请求体
	body := bytes.NewBufferString(`{"image_id": "img-123"}`)
	req := httptest.NewRequest("POST", "/api/v1/images/confirm", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// 测试四：HandleConfirmRequest Body 格式错误
func TestHandleConfirmRequest_InvalidBody(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockUC := mocks.NewMockImageUsecase(ctrl)
	mockST := mocks.NewMockStorage(ctrl)

	// Body 格式错误，ConfirmTask 不应该被调用
	mockUC.EXPECT().ConfirmTask(gomock.Any(), gomock.Any()).Times(0)

	r := setupTestRouter(mockUC, mockST)

	// 不带 image_id 的请求 → binding 会失败
	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest("POST", "/api/v1/images/confirm", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 测试五：HandleGetImage 图片存在（状态 Completed）
func TestHandleGetImage_Completed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockUC := mocks.NewMockImageUsecase(ctrl)
	mockST := mocks.NewMockStorage(ctrl)

	fakeImg := &domain.Image{
		ID:           "img-123",
		Status:       domain.StatusCompleted,
		ThumbnailURL: "https://cdn.example.com/thumb.jpg",
		Tags:         []string{"cat", "animal"},
	}
	mockUC.EXPECT().
		GetImage(gomock.Any(), "img-123").
		Return(fakeImg, nil).
		Times(1)

	r := setupTestRouter(mockUC, mockST)
	req := httptest.NewRequest("GET", "/api/v1/images/img-123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	assert.Equal(t, "https://cdn.example.com/thumb.jpg", body["display_url"])
}

// 测试六：HandleGetImage 图片不存在
func TestHandleGetImage_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockUC := mocks.NewMockImageUsecase(ctrl)
	mockST := mocks.NewMockStorage(ctrl)

	mockUC.EXPECT().
		GetImage(gomock.Any(), "img-999").
		Return(nil, errors.New("not found")).
		Times(1)

	r := setupTestRouter(mockUC, mockST)
	req := httptest.NewRequest("GET", "/api/v1/images/img-999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
