package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/ErinWang666/image-processor/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types" // 🌟 新增：用于获取队列 ARN
)

type SQSQueue struct {
	client   *sqs.Client
	queueURL string
}

// NewSQSQueue 构造函数，自动连接 ElasticMQ 并创建主队列 + 死信队列 (DLQ)
func NewSQSQueue(ctx context.Context, queueName string) (domain.Queue, error) {
	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			PartitionID:   "aws",
			URL:           "http://127.0.0.1:9324",
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

	// 🌟 1. 先创建死信队列 (DLQ)
	dlqName := queueName + "_dlq"
	dlqOutput, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(dlqName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create DLQ: %v", err)
	}

	// 🌟 2. 获取 DLQ 的 ARN (Amazon Resource Name，唯一标识符)
	dlqAttr, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       dlqOutput.QueueUrl,
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get DLQ ARN: %v", err)
	}
	dlqArn := dlqAttr.Attributes[string(types.QueueAttributeNameQueueArn)]

	// 🌟 3. 创建主队列，并绑定 RedrivePolicy (重试策略：失败 3 次就扔进 DLQ)
	redrivePolicy := fmt.Sprintf(`{"deadLetterTargetArn":"%s","maxReceiveCount":"3"}`, dlqArn)
	mainOutput, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(queueName),
		Attributes: map[string]string{
			"RedrivePolicy": redrivePolicy,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create main queue: %v", err)
	}

	log.Printf("🌩️ [SQS] Connected to Main Queue: %s", *mainOutput.QueueUrl)
	log.Printf("🌩️ [SQS] DLQ Configured at: %s (Max retries: 3)", *dlqOutput.QueueUrl)

	return &SQSQueue{
		client:   client,
		queueURL: *mainOutput.QueueUrl,
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
				// 🌟 核心：如果是因为系统正在关机导致的 Context 取消，就退出循环
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					log.Println("🛑 [SQS Subscriber] Context canceled, gracefully stopping polling...")
					break
				}
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