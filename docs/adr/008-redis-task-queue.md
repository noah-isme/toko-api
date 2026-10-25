# ADR-008: Redis-Based Task Queue Over External Broker

**Status:** Accepted
**Date:** 2024-05

## Context

Webhook delivery needs reliable, asynchronous processing with retry semantics. Email notifications are currently handled by the API event notifier and analytics reads use PostgreSQL materialized views plus a Redis cache; they are not worker queue kinds. Options considered:
- External message brokers (RabbitMQ, Kafka, SQS).
- Redis-based queues (custom or via libraries like `asynq`).

## Decision

Build a **lightweight Redis-based task queue** (`internal/queue`) with:
- Redis sorted-set enqueue with deduplication (`DedupTTL`). The existence check, `ZADD` and the dedup `SET` run in one Lua script, so a dedup key exists only when the task was queued (the original `SETNX` + `ZADD` pair could leave a reservation behind after a failed publish). Email and push tasks use `OUTBOX_RELAY_MAX_AGE_SEC` as their dedup window; other kinds use `IDEMPOTENCY_TTL_SEC`.
- Visibility timeouts and periodic re-queue on timeout. Queue removal and processing-set insertion are separate Redis operations, so a worker crash between them can lose a task.
- Configurable max attempts with exponential back-off.
- Dead-letter queue (DLQ) stored in PostgreSQL for durability and admin inspection.
- Admin endpoints for DLQ list, replay, and stats.

The `cmd/worker` binary currently consumes the `webhook-delivery` task kind from Redis and processes tasks concurrently.

## Consequences

- **Positive:** No additional infrastructure — Redis is already required for caching and rate limiting.
- **Positive:** DLQ in PostgreSQL provides durable, queryable storage for failed tasks.
- **Positive:** Admin DLQ endpoints enable operational recovery without shell access.
- **Negative:** Redis persistence (RDB/AOF) is required to avoid task loss on restart.
- **Negative:** This custom queue has no benchmark-backed throughput guarantee; validate capacity for any materially higher workload.
