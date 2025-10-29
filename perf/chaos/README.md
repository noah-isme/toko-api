# Chaos Scenarios

1. **Database Down** – Stop PostgreSQL connections (e.g. firewall) and verify `/products` served from cache while `/checkout` fails with a consistent 503 payload. Ensure alerting fires.
2. **Redis Down** – Terminate Redis or block traffic. Rate limiting becomes permissive, queue processing pauses, and API read paths stay online with higher latency.
3. **Provider Down** – Blackhole outbound payment/shipping hosts (see `provider_down.sh`) and confirm circuit breakers open, checkout returns structured 503s, and webhook jobs land in the DLQ.
