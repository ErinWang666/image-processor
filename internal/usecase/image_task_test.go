package usecase_test

// ==========================================
// 📖 在阅读这个文件之前，先理解三个核心概念
// ==========================================
//
// 1. 为什么包名是 usecase_test 而不是 usecase？
//    → Go 的惯例：测试文件和被测代码在同一个目录，但包名加 _test 后缀
//      这样测试代码只能访问被测包的公共 API，和外部使用者一样的视角
//      如果测试需要访问私有方法，才用同包名（但单元测试一般不需要）
//
// 2. 什么是 Mock？为什么要用 Mock？
//    → 想象你要测 CreateTask 这个函数。它内部调用了 repo.Save(DB 操作)
//      如果真的连 DB，测试就依赖外部环境（DB 挂了测试就失败），而且慢
//      Mock 就是一个"假的 repo"：你提前告诉它"当 Save 被调用时，返回 nil"
//      这样测试只关心 usecase 的逻辑，完全不依赖真实 DB
//      就像测"司机会不会踩刹车"，你不需要真的上路，用模拟器就行
//
// 3. gomock 的工作方式
//    → EXPECT() 阶段：声明"我期望这个方法被调用，参数是什么，返回什么"
//      执行阶段：实际调用被测函数
//      验证阶段：ctrl.Finish() 检查所有期望是否都实现了（谁没被调用就报错）

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/ErinWang666/image-processor/internal/usecase"
	"github.com/ErinWang666/image-processor/internal/usecase/mocks"
	"go.uber.org/mock/gomock"
)

// ==========================================
// 🔧 辅助函数：创建测试用的 ImageUsecase
// ==========================================
//
// 为什么抽成函数而不是每个测试里重复写？
// → DRY（Don't Repeat Yourself）原则
//   所有测试用例共享这个 setup 逻辑，改一处就全改了
//
// 返回四个值：
//   - *ImageUsecase: 被测对象
//   - *MockImageRepository: 假 DB
//   - *MockTaskLocker: 假分布式锁
//   - *MockAIService: 假 AI 服务
// （Storage 和 Queue 在这些测试里不涉及，暂时不返回）
func newTestUsecase(t *testing.T) (
	*usecase.ImageUsecase,
	*mocks.MockImageRepository,
	*mocks.MockTaskLocker,
	*mocks.MockAIService,
	*gomock.Controller,
) {
	// gomock.Controller 是"期望管理器"
	// 它记录所有 EXPECT() 声明，最后验证是否都被调用到了
	// t.Cleanup 会在测试结束时自动调用 ctrl.Finish()，所以不用手动写 defer ctrl.Finish()
	ctrl := gomock.NewController(t)

	mockRepo := mocks.NewMockImageRepository(ctrl)
	mockLocker := mocks.NewMockTaskLocker(ctrl)
	mockAI := mocks.NewMockAIService(ctrl)

	// Storage 和 Queue 这些测试用例里不会用到，传 nil 就行
	// 如果测试里意外调用了它们会 panic，这正是我们想要的：提前发现不该调的调了
	uc := usecase.NewImageUsecase(mockRepo, nil, nil, mockAI, mockLocker)

	return uc, mockRepo, mockLocker, mockAI, ctrl
}

// ==========================================
// 📋 测试 CreateTask
// ==========================================

// TestCreateTask_Success 测试正常路径：repo.Save 成功
//
// 测试函数命名规范：Test_函数名_场景描述
// 这样在 go test -v 输出里很容易看出哪个场景失败了
func TestCreateTask_Success(t *testing.T) {
	uc, mockRepo, _, _, _ := newTestUsecase(t)
	ctx := context.Background()

	img := &domain.Image{
		ID:     "img-001",
		UserID: "user-999",
		Status: domain.StatusPending,
	}

	// EXPECT：声明"我期望 Save 会被调用一次，参数是 ctx 和 img，返回 nil（成功）"
	// gomock.Any() 匹配任意值，这里用 ctx 和 img 精确匹配
	mockRepo.EXPECT().
		Save(ctx, img).        // 期望的方法和参数
		Return(nil).           // 期望的返回值
		Times(1)               // 期望被调用的次数

	// 执行被测函数
	err := uc.CreateTask(ctx, img)

	// 断言：期望没有错误返回
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// TestCreateTask_RepoError 测试错误路径：repo.Save 失败时，CreateTask 应该返回错误
func TestCreateTask_RepoError(t *testing.T) {
	uc, mockRepo, _, _, _ := newTestUsecase(t)
	ctx := context.Background()

	img := &domain.Image{ID: "img-001"}

	// 模拟 DB 故障：Save 返回一个错误
	dbErr := errors.New("database connection refused")
	mockRepo.EXPECT().
		Save(ctx, img).
		Return(dbErr).
		Times(1)

	err := uc.CreateTask(ctx, img)

	// 断言：期望有错误返回
	if err == nil {
		t.Error("expected an error, got nil")
	}
	// 进一步验证：错误里包含 repo 返回的原始错误（因为用了 %w 包装）
	if !errors.Is(err, dbErr) {
		t.Errorf("expected error to wrap dbErr, got: %v", err)
	}
}

// ==========================================
// 📋 测试 ConfirmTask
// ==========================================

// TestConfirmTask_Success 测试正常路径
func TestConfirmTask_Success(t *testing.T) {
	uc, mockRepo, _, _, _ := newTestUsecase(t)
	ctx := context.Background()
	imageID := "img-001"

	// ConfirmTask 内部会调用 UpdateStatusWithOutbox
	// gomock.Any() 匹配任意参数（因为 payload 是动态构建的，不好精确匹配）
	mockRepo.EXPECT().
		UpdateStatusWithOutbox(ctx, imageID, domain.StatusPending, "image_processing_queue", gomock.Any()).
		Return(nil).
		Times(1)

	err := uc.ConfirmTask(ctx, imageID)

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// TestConfirmTask_OutboxError 测试错误路径：UpdateStatusWithOutbox 失败
func TestConfirmTask_OutboxError(t *testing.T) {
	uc, mockRepo, _, _, _ := newTestUsecase(t)
	ctx := context.Background()
	imageID := "img-001"

	outboxErr := errors.New("transaction failed")
	mockRepo.EXPECT().
		UpdateStatusWithOutbox(ctx, imageID, domain.StatusPending, "image_processing_queue", gomock.Any()).
		Return(outboxErr).
		Times(1)

	err := uc.ConfirmTask(ctx, imageID)

	if err == nil {
		t.Error("expected an error, got nil")
	}
	if !errors.Is(err, outboxErr) {
		t.Errorf("expected error to wrap outboxErr, got: %v", err)
	}
}

// ==========================================
// 📋 测试 ProcessImage
// ==========================================

// TestProcessImage_LockNotAcquired 测试重复消费场景：锁抢不到时应该静默返回（不报错）
//
// 这个测试很重要！它验证了你的幂等性设计：
// 同一个任务被两个 Worker 同时消费时，先抢到锁的那个处理，后来的直接跳过
func TestProcessImage_LockNotAcquired(t *testing.T) {
	uc, mockRepo, mockLocker, _, _ := newTestUsecase(t)
	ctx := context.Background()

	task := domain.TaskPayload{ImageID: "img-001", UserID: "user-999"}

	// 模拟锁被别人持有，acquired = false
	mockLocker.EXPECT().
		Acquire(ctx, "task:lock:img-001", 10*time.Minute).
		Return("", false, nil). // token 为空，acquired 为 false，无错误
		Times(1)

	// 因为锁没抢到，后面的 repo.UpdateStatus 不应该被调用
	// 不写 mockRepo.EXPECT() 就意味着期望它不被调用
	// 如果 UpdateStatus 被意外调用了，ctrl.Finish() 会报错
	_ = mockRepo // 明确表示 mockRepo 在这个测试里不被期望调用

	err := uc.ProcessImage(ctx, task)

	// 锁没抢到应该返回 nil（静默跳过，不是错误）
	if err != nil {
		t.Errorf("expected nil error when lock not acquired, got: %v", err)
	}
}

// TestProcessImage_LockError 测试获取锁时 Redis 故障的场景
func TestProcessImage_LockError(t *testing.T) {
	uc, _, mockLocker, _, _ := newTestUsecase(t)
	ctx := context.Background()

	task := domain.TaskPayload{ImageID: "img-001"}

	redisErr := errors.New("redis: connection refused")
	mockLocker.EXPECT().
		Acquire(ctx, "task:lock:img-001", 10*time.Minute).
		Return("", false, redisErr).
		Times(1)

	err := uc.ProcessImage(ctx, task)

	// Redis 故障应该返回 error（不能静默跳过，需要重试）
	if err == nil {
		t.Error("expected an error when lock fails, got nil")
	}
}

// ==========================================
// 📋 测试 GetImage
// ==========================================

// TestGetImage_Success 测试正常查询
func TestGetImage_Success(t *testing.T) {
	uc, mockRepo, _, _, _ := newTestUsecase(t)
	ctx := context.Background()

	expected := &domain.Image{
		ID:     "img-001",
		Status: domain.StatusCompleted,
	}

	mockRepo.EXPECT().
		GetByID(ctx, "img-001").
		Return(expected, nil).
		Times(1)

	result, err := uc.GetImage(ctx, "img-001")

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// 验证返回的是预期的对象
	if result.ID != expected.ID {
		t.Errorf("expected ID %s, got %s", expected.ID, result.ID)
	}
	if result.Status != domain.StatusCompleted {
		t.Errorf("expected status COMPLETED, got %s", result.Status)
	}
}

// TestGetImage_NotFound 测试查询不存在的 ID
func TestGetImage_NotFound(t *testing.T) {
	uc, mockRepo, _, _, _ := newTestUsecase(t)
	ctx := context.Background()

	notFoundErr := errors.New("record not found")
	mockRepo.EXPECT().
		GetByID(ctx, "img-not-exist").
		Return(nil, notFoundErr).
		Times(1)

	result, err := uc.GetImage(ctx, "img-not-exist")

	if result != nil {
		t.Error("expected nil result, got non-nil")
	}
	if !errors.Is(err, notFoundErr) {
		t.Errorf("expected notFoundErr, got: %v", err)
	}
}
