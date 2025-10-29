# Runbook
## Incident Tiers & Escalation
- HighErrorRate/HighLatency -> Sev2; BreakerOpenTooLong -> Sev2; DLQSizeHighCrit -> Sev1.
## DLQ Replay
- Gunakan endpoint admin replay per-id atau batch (kind); pastikan root cause diatasi sebelum replay massal.
## Scaling
- Tambah replicas API/worker; pantau queue_depth & webhook latency p95.
## Drain & Rolling Update
- Set readiness=false, tunggu job selesai, deploy, verifikasi health & alerts clear.
