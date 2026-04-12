package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

func main() {
	ctx := context.Background()
	// 注意：把你真实的 API Key 填在下面
	apiKey := "AIzaSyAGN8ZqE4nPCeor8dWCgVT9c_zHHFHyRR4"

	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		log.Fatalf("创建客户端失败: %v", err)
	}
	defer client.Close()

	fmt.Println("🔍 正在向 Google 服务器查询最新的模型列表...")
	
	// 调用 ListModels 方法
	iter := client.ListModels(ctx)
	for {
		m, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatalf("查询失败: %v", err)
		}
		
		// 打印模型名字和它的简短描述
		fmt.Printf("📦 模型名称: %s \n   📖 描述: %s\n\n", m.Name, m.Description)
	}
}