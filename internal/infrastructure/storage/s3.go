package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Storage struct {
	client          *s3.Client
	presignClient   *s3.PresignClient
}

// NewS3Storage 初始化 S3 客户端 (同时支持 AWS 和 MinIO)
func NewS3Storage() (domain.Storage, error) {
	// 1. 加载配置
	// 在真实大厂代码中，这些 key 应该从环境变量读取 (os.Getenv)
	// 这里为了教学方便，我们先写死 MinIO 的默认账号密码
	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion("us-east-1"), // MinIO 默认需要一个 Region，虽然它不用
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("admin", "password", "")),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to load sdk config: %w", err)
	}

	// 2. 创建 S3 客户端
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		// 关键点：强制使用本地 URL，而不是连接真实的 AWS
		o.BaseEndpoint = aws.String("http://localhost:9000")
		// 必须设置，否则 SDK 会尝试用虚拟主机名访问 (bucket.localhost)，导致 DNS 失败
		o.UsePathStyle = true 
	})

	return &S3Storage{
		client:        client,
		presignClient: s3.NewPresignClient(client),
	}, nil
}

// Upload 实现上传
func (s *S3Storage) Upload(ctx context.Context, bucket string, key string, body io.Reader) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   body,
	})
	if err != nil {
		return fmt.Errorf("failed to upload to s3: %w", err)
	}
	return nil
}

// Download 实现下载
func (s *S3Storage) Download(ctx context.Context, bucket string, key string) (io.ReadCloser, error) {
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download from s3: %w", err)
	}
	return output.Body, nil
}

// GetPresignedURL 生成预签名链接
func (s *S3Storage) GetPresignedURL(ctx context.Context, bucket string, key string, expiry time.Duration) (string, error) {
	req, err := s.presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, func(o *s3.PresignOptions) {
		o.Expires = expiry
	})
	if err != nil {
		return "", fmt.Errorf("failed to presign request: %w", err)
	}
	return req.URL, nil
}