# ADR-008: Redis-Based Task Queue Over External Broker

**Status:** Accepted  
**Date:** 2024-05  

## Context

Background tasks (webhook delivery, email sending, analytics aggregation) need reliable, asynchronous processing with retry semantics. Options considered:
- External message brokers (RabbitMQ, Kafka, SQS).
- Redis-based queues (custom or via libraries like `asynq`).

## Decision

Build a **lightweight Redis-based task queue** (`internal/queue`) with:
- Atomic enqueue with deduplication (`DedupTTL`).
- Visibility timeouts and automatic re-queue on timeout.
- Configurable max attempts with exponential back-off.
- Dead-letter queue (DLQ) stored in PostgreSQL for durability and admin inspection.
- Admin endpoints for DLQ list, replay, and stats.

The `cmd/worker` binary consumes tasks from Redis and processes them concurrently.

## Consequences

- **Positive:** No additional infrastructure — Redis is already required for caching and rate limiting.
- **Positive:** DLQ in PostgreSQL provides durable, queryable storage for failed tasks.
- **Positive:** Admin DLQ endpoints enable operational recovery without shell access.
- **Negative:** Redis persistence (RDB/AOF) is required to avoid task loss on restart.
- **Negative:** Not suitable for very high throughput (>10k tasks/sec) — adequate for current scale.
