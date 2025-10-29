## DB Down
- /health/ready -> 503; /products cached OK; checkout -> 503 (error shape standar).
## Redis Down
- Rate limit disabled; queue workers backoff; metrics menunjukkan DLQ meningkat jika provider down bersamaan.
## Provider Down
- Breaker open; webhook -> DLQ; alert WebhookP99Slow/HighErrorRate mungkin aktif.
