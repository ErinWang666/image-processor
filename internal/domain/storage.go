package domain

import (
	"context"
	"io"
	"time"
)

// Storage 定义了文件存储的抽象接口
type Storage interface {
	// Upload 上传文件流
	Upload(ctx context.Context, bucket string, key string, body io.Reader) error
	
	// Download 下载文件流 (返回一个 ReadCloser，调用者必须记得 Close)
	Download(ctx context.Context, bucket string, key string) (io.ReadCloser, error)
	
	// GetPresignedURL 生成临时上传/下载链接
	GetPresignedURL(ctx context.Context, bucket string, key string, expiry time.Duration) (string, error)
}