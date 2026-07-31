#!/usr/bin/env bash
# Smoke-tests every admin GET endpoint the dashboard calls, using a real admin JWT.
set -uo pipefail

BASE="http://localhost:58081/api/v1"

TOKEN=$(curl -sS -X POST "$BASE/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@toko.com","password":"password123"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["data"]["accessToken"])')

if [ -z "$TOKEN" ]; then
  echo "login failed"
  exit 1
fi

PATHS=(
  "/admin/products?page=1&limit=3"
  "/admin/categories"
  "/admin/brands"
  "/admin/orders?page=1&limit=3"
  "/admin/orders/stats"
  "/admin/vouchers?page=1&limit=3"
  "/admin/vouchers/stats"
  "/admin/analytics/overview?range=30d"
  "/admin/webhooks"
  "/admin/webhook-deliveries?limit=3"
  "/admin/audit-logs?page=1&limit=3"
  "/admin/queue/stats?kind=webhook"
  "/admin/queue/dlq?limit=3"
)

fail=0
for p in "${PATHS[@]}"; do
  body=$(curl -sS -H "Authorization: Bearer $TOKEN" "$BASE$p")
  code=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$BASE$p")
  keys=$(printf '%s' "$body" | python3 -c '
import sys, json
try:
    d = json.load(sys.stdin)
except Exception as exc:
    print("unparseable:", exc)
    raise SystemExit
if isinstance(d, dict):
    if "data" in d and isinstance(d["data"], list):
        first = d["data"][0] if d["data"] else None
        print("n=%d" % len(d["data"]), "first_keys=" + ",".join(sorted(first)) if isinstance(first, dict) else "")
    elif "data" in d and isinstance(d["data"], dict):
        print("keys=" + ",".join(sorted(d["data"])))
    else:
        print("keys=" + ",".join(sorted(d)))
else:
    print(type(d).__name__)
')
  if [ "$code" != "200" ]; then
    fail=1
    printf '%s  %s\n      body=%s\n' "$code" "$p" "$(printf '%s' "$body" | head -c 200)"
  else
    printf '%s  %-46s %s\n' "$code" "$p" "$keys"
  fi
done

exit "$fail"
