# k6 Load Tests

This folder contains reusable k6 scenarios to measure key SLOs.

## Scenarios
- `perf/k6/read_heavy.js` – catalogue listing & detail read-through.
- `perf/k6/mixed_checkout.js` – cart, add item, checkout mix.
- `perf/k6/webhook_burst.js` – webhook delivery burst throughput.

Each scenario honours the following environment variables:
- `BASE_URL` (default: ${PERF_K6_BASE_URL})
- `VUS` (default: scenario specific)
- `DURATION` (default: scenario specific)
- `HTTP_P95`, `HTTP_P99` (optional) for latency budgets.

## Smoke PR (CI budget)
```bash
BASE_URL=${PERF_K6_BASE_URL} \\
VUS=${PERF_K6_VUS_SMOKE} \\
DURATION=${PERF_K6_DURATION_SMOKE} \\
k6 run perf/k6/read_heavy.js
```

## Full Baseline
```bash
k6 run -e BASE_URL=$URL -e VUS=100 -e DURATION=5m perf/k6/read_heavy.js
k6 run -e BASE_URL=$URL -e VUS=40 -e DURATION=5m perf/k6/mixed_checkout.js
k6 run -e BASE_URL=$URL -e VUS=60 -e DURATION=3m perf/k6/webhook_burst.js
```

## Reports
- Smoke summary is exported to `perf/smoke.json` in CI.
- Baseline summaries should be stored under `perf/results/` for trend analysis.
