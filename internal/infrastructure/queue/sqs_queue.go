package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type SQSQueue struct {
	client   *sqs.Client
	queueURL string
}

// NewSQSQueue 构造函数，自动连接 LocalStack 并创建队列
func NewSQSQueue(ctx context.Context, queueName string) (domain.Queue, error) {
	// 1. 加载 AWS 配置，并强行将地址指向本地 LocalStack (4566 端口)
	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			PartitionID:   "aws",
			URL:           "http://127.0.0.1:9324", // 🌟 改成 ElasticMQ 的端口 9324
			SigningRegion: "us-east-1",
		}, nil
	})

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithEndpointResolverWithOptions(customResolver),
		config.WithCredentialsProvider(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
		})),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %v", err)
	}

	client := sqs.NewFromConfig(cfg)

	// 2. 自动创建队列 (如果不存在的话)
	createOutput, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(queueName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create queue: %v", err)
	}

	log.Printf("🌩️ [SQS] Connected to LocalStack Queue: %s", *createOutput.QueueUrl)

	return &SQSQueue{
		client:   client,
		queueURL: *createOutput.QueueUrl,
	}, nil
}

// Publish 发送结构体消息 (兼容旧逻辑)
func (s *SQSQueue) Publish(ctx context.Context, topic string, payload domain.TaskPayload) error {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.PublishRaw(ctx, topic, bodyBytes)
}

// PublishRaw 发送原生字节消息 (专供 Outbox Relay 使用)
func (s *SQSQueue) PublishRaw(ctx context.Context, topic string, payload []byte) error {
	_, err := s.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(s.queueURL),
		MessageBody: aws.String(string(payload)),
	})
	return err
}

// Subscribe 消费者长轮询 (Long Polling)
func (s *SQSQueue) Subscribe(ctx context.Context, topic string) (<-chan domain.TaskPayload, error) {
	tasks := make(chan domain.TaskPayload)

	go func() {
		defer close(tasks)
		for {
			// 1. 从 SQS 拉取消息 (长轮询机制，最多等 20 秒)
			msgResult, err := s.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
				QueueUrl:            aws.String(s.queueURL),
				MaxNumberOfMessages: 1, // 每次拿1条
				WaitTimeSeconds:     20, // 🌟 核心：如果没有消息，不要疯狂重试，而是挂起等待 20 秒 (极大节省 CPU)
			})

			if err != nil {
				log.Printf("❌ [SQS] Receive error: %v", err)
				continue
			}

			if len(msgResult.Messages) == 0 {
				continue // 超时没拿到消息，继续下一轮轮询
			}

			message := msgResult.Messages[0]

			// 2. 解析 JSON
			var task domain.TaskPayload
			if err := json.Unmarshal([]byte(*message.Body), &task); err != nil {
				log.Printf("❌ [SQS] Unmarshal error: %v", err)
				continue
			}

			// 3. 把任务发给 Worker 协程
			tasks <- task

			// 4. 🌟 SQS 核心特性：消费成功后，必须显式删除消息！否则它会超时后再次出现！
			_, err = s.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(s.queueURL),
				ReceiptHandle: message.ReceiptHandle,
			})
			if err != nil {
				log.Printf("❌ [SQS] Failed to delete message: %v", err)
			}
		}
	}()

	return tasks, nil
}