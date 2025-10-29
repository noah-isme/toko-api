#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <provider-host>" >&2
  exit 1
fi

HOST="$1"

sudo iptables -I OUTPUT -p tcp -d "$HOST" -j REJECT
trap 'sudo iptables -D OUTPUT -p tcp -d "$HOST" -j REJECT' EXIT

echo "Provider traffic to $HOST blackholed. Observe /metrics for breaker state and API failures. Press Ctrl+C to restore." 
while true; do sleep 5; done
