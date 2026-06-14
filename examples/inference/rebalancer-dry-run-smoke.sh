#!/usr/bin/env bash
set -euo pipefail

SCHEDULER_URL="${SCHEDULER_URL:-http://localhost:8888}"
PROXY_URL="${PROXY_URL:-http://localhost:8080}"
MODEL="${MODEL:-Qwen/Qwen2.5-7B-Instruct}"
PROMPT="${PROMPT:-Say hi in one short sentence.}"
REQUESTS="${REQUESTS:-3}"
SLEEP_SECONDS="${SLEEP_SECONDS:-2}"

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

json_pretty() {
  if command -v jq >/dev/null 2>&1; then
    jq .
  else
    cat
  fi
}

need curl

echo "== Scheduler health =="
curl -fsS "${SCHEDULER_URL}/healthz"
echo

echo "== Scheduler inference workers =="
curl -fsS "${SCHEDULER_URL}/api/v1/control/workers" | json_pretty
echo

echo "== Proxy health =="
curl -fsS "${PROXY_URL}/healthz" | json_pretty
echo

echo "== Proxy pressure before traffic =="
curl -fsS "${PROXY_URL}/api/v1/control/pressure" | json_pretty
echo

echo "== Sending ${REQUESTS} chat request(s) via proxy =="
for i in $(seq 1 "${REQUESTS}"); do
  echo "-- request ${i}/${REQUESTS}"
  curl -fsS "${PROXY_URL}/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "{
      \"model\":\"${MODEL}\",
      \"messages\":[{\"role\":\"user\",\"content\":\"${PROMPT}\"}],
      \"stream\": false
    }" | json_pretty
  echo
  sleep "${SLEEP_SECONDS}"
done

echo "== Proxy worker stats after traffic =="
curl -fsS "${PROXY_URL}/api/v1/control/worker-stats" | json_pretty
echo

echo "== Proxy pressure after traffic =="
curl -fsS "${PROXY_URL}/api/v1/control/pressure" | json_pretty
echo

cat <<EOF

Dry-run rebalancer log check:
  tail -f scheduler.log | grep '\\[rebalancer\\]'

Expected progression under pressure:
  action=none reason=not_sustained
  action=add_prefill or action=add_decode after sustain_window_seconds
  action=none reason=cooldown after an action
EOF
