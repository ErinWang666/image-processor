# Image Processor

A high-performance, asynchronous image processing service built with Go, focusing on reliability and horizontal scalability.

## 🚀 Current Project Status

The core architecture and infrastructure are now in place. The system successfully implements the **Transactional Outbox Pattern** to ensure atomicity between database updates and message dispatching.

### Key Features Implemented:

- **Asynchronous Processing**: Decoupled image processing tasks using **AWS SQS** (via ElasticMQ) to handle heavy workloads without blocking the API.
- **Reliable Messaging**: Implemented the **Transactional Outbox pattern** to guarantee "at-least-once" message delivery even during system failures.
- **Distributed Locking**: Integrated **Redis Distributed Locks** to prevent duplicate processing of the same image by multiple workers.
- **Object Storage**: Direct integration with **MinIO (S3-compatible)** for secure and scalable image storage.
- **AI Integration**: Automatic image tagging and analysis powered by **Google Gemini AI**.
- **Containerized Infrastructure**: Full local environment orchestration via **Docker Compose** (PostgreSQL, Redis, MinIO, ElasticMQ).

## 🛠️ Tech Stack

- **Language**: Go (Golang)
- **Database**: PostgreSQL
- **Cache/Lock**: Redis
- **Message Queue**: AWS SQS (ElasticMQ for local dev)
- **Storage**: MinIO (S3)
- **AI**: Google Gemini Pro Vision

## 🚦 Getting Started (Local Dev)

1. **Infrastructure**: Spin up the required services:
   ```bash
   docker-compose up -d
   ```
2. **Environment**: Configure your .env file with your GEMINI_API_KEY.
3. **Run API & Worker**:
   ```bash
   go run cmd/server/main.go
   ```

## 🏗️ Architecture Overview

The system follows a modular design where the API stores the initial state and an "Outbox" message in a single transaction. A background relay service then picks up these messages and pushes them to SQS for Worker consumption.
