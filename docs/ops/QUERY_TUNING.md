# Query Tuning

Target the following expensive flows when investigating latency regressions:

1. Product listing (filters & sorts).
2. Product search (full-text lookups).
3. Related products (category recommendations).
4. Top products analytics materialized view refreshes.
5. Orders listing (admin dashboard).

For each query:
- Capture the current plan: `EXPLAIN (ANALYZE, BUFFERS)` in staging with production-like data.
- Record the baseline plan, duration, and buffers in this document or the incident ticket.
- Apply index, statistics, or rewrite changes. Regenerate SQLC artifacts if query definitions change.
- Re-run `EXPLAIN (ANALYZE, BUFFERS)` and document improvements.
- Validate with load tests (`perf/k6`) before promoting to production.

Document owner: Backend Oncall. Update whenever indexes/migrations related to these queries are modified.
