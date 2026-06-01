package persistence_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/ErinWang666/image-processor/internal/infrastructure/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 包级变量（共享的 repo）
var testRepo domain.ImageRepository

// TestMain（启动容器、建表）
func TestMain(m *testing.M) {
	ctx := context.Background()

	// 启动 PostgreSQL 容器
	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:15-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
	)
	if err != nil {
		fmt.Printf("failed to start postgres container: %v\n", err)
		os.Exit(1)
	}
	defer pgContainer.Terminate(ctx) //nolint:errcheck

	// 拿到容器的连接地址
	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Printf("failed to get connection string: %v\n", err)
		os.Exit(1)
	}

	// 连接数据库，带重试（PostgreSQL 容器启动后内部还需要几秒完全就绪）
	var db *gorm.DB
	for i := 0; i < 10; i++ {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err == nil {
			break
		}
		fmt.Printf("waiting for postgres to be ready... (%d/10)\n", i+1)
		time.Sleep(time.Second)
	}
	if err != nil {
		fmt.Printf("failed to connect db after retries: %v\n", err)
		os.Exit(1)
	}

	// 自动建表
	if err := db.AutoMigrate(&domain.Image{}); err != nil {
		fmt.Printf("failed to migrate: %v\n", err)
		os.Exit(1)
	}

	// 创建 repo，赋值给包级变量，供所有测试使用
	testRepo = persistence.NewPostgresRepository(db)

	// 跑所有测试
	os.Exit(m.Run())
}

// 三个测试函数
func TestSave_Success(t *testing.T) {
	ctx := context.Background()
	img := &domain.Image{
		ID:        "test-save-001",
		UserID:    "user-1",
		Status:    domain.StatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := testRepo.Save(ctx, img)
	assert.NoError(t, err)
}

func TestGetByID_Found(t *testing.T) {
	ctx := context.Background()

	// 先存一条数据
	img := &domain.Image{
		ID:        "test-get-001",
		UserID:    "user-2",
		Status:    domain.StatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, testRepo.Save(ctx, img)) // require：失败就立刻停止测试

	// 再查出来
	found, err := testRepo.GetByID(ctx, "test-get-001")
	assert.NoError(t, err)
	assert.Equal(t, "test-get-001", found.ID)
	assert.Equal(t, domain.StatusPending, found.Status)
}

func TestGetByID_NotFound(t *testing.T) {
	ctx := context.Background()

	_, err := testRepo.GetByID(ctx, "this-id-does-not-exist")
	assert.Error(t, err) // 期望返回错误
}

func TestUpdateResult_Success(t *testing.T) {
	ctx := context.Background()

	// 先存一条 PROCESSING 状态的记录
	img := &domain.Image{
		ID:        "test-update-001",
		UserID:    "user-3",
		Status:    domain.StatusProcessing, // 必须是 PROCESSING 才能更新
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, testRepo.Save(ctx, img))

	// 更新结果
	err := testRepo.UpdateResult(ctx,
		"test-update-001",
		domain.StatusCompleted,
		"https://cdn.example.com/thumb.jpg",
		[]string{"cat", "animal"},
	)
	assert.NoError(t, err)

	// 验证数据库里的值确实变了
	updated, err := testRepo.GetByID(ctx, "test-update-001")
	assert.NoError(t, err)
	assert.Equal(t, domain.StatusCompleted, updated.Status)
	assert.Equal(t, "https://cdn.example.com/thumb.jpg", updated.ThumbnailURL)
}

func TestUpdateResult_WrongStatus(t *testing.T) {
	ctx := context.Background()

	// 存一条 PENDING 状态的记录（不是 PROCESSING）
	img := &domain.Image{
		ID:        "test-update-002",
		UserID:    "user-4",
		Status:    domain.StatusPending, // ← 不是 PROCESSING
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, testRepo.Save(ctx, img))

	// UpdateResult 内部有 WHERE status = 'PROCESSING' 的卫哨
	// 这条记录是 PENDING，所以 RowsAffected = 0，应该返回错误
	err := testRepo.UpdateResult(ctx,
		"test-update-002",
		domain.StatusCompleted,
		"https://cdn.example.com/thumb.jpg",
		[]string{},
	)
	assert.Error(t, err) // 期望乐观锁卫哨触发，返回错误
}
