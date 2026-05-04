#!/usr/bin/env bash
set -euo pipefail

# Unified inference deploy helper via gpu-scheduler.
#
# Commands:
#   examples/inference/inference-unified.sh apply
#   examples/inference/inference-unified.sh status
#   examples/inference/inference-unified.sh verify
#   examples/inference/inference-unified.sh cleanup
#   examples/inference/inference-unified.sh logs
#
# Optional env overrides:
#   NAMESPACE=default
#   MANIFEST=examples/inference/inference-unified.yaml
#   SCHEDULER_URL=http://localhost:8888
#   PROXY_URL=http://localhost:8080
#   MODEL_GROUP=Qwen/Qwen2.5-14B-Instruct

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

NAMESPACE="${NAMESPACE:-default}"
MANIFEST="${MANIFEST:-${REPO_ROOT}/examples/inference/inference-unified.yaml}"
SCHEDULER_URL="${SCHEDULER_URL:-http://localhost:8888}"
PROXY_URL="${PROXY_URL:-http://localhost:8080}"
MODEL_GROUP="${MODEL_GROUP:-Qwen/Qwen2.5-14B-Instruct}"

POD_NAME="inference-unified"
SVC_NAME="inference-unified-svc"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1"
    exit 1
  }
}

do_apply() {
  require_cmd kubectl
  echo "Applying ${MANIFEST}"
  kubectl apply -f "${MANIFEST}"
  echo "Waiting for pod/${POD_NAME} to become Ready..."
  kubectl -n "${NAMESPACE}" wait --for=condition=Ready "pod/${POD_NAME}" --timeout=10m
}

do_status() {
  require_cmd kubectl
  echo "Pod + Service status:"
  kubectl -n "${NAMESPACE}" get pod "${POD_NAME}" -o wide
  kubectl -n "${NAMESPACE}" get svc "${SVC_NAME}" -o wide
}

do_verify() {
  require_cmd curl
  echo "Scheduler workers:"
  curl -fsS "${SCHEDULER_URL}/api/v1/control/workers" | python3 -m json.tool
  echo
  echo "Proxy health:"
  curl -fsS "${PROXY_URL}/healthz" | python3 -m json.tool
  echo
  echo "Test request via proxy:"
  curl -fsS "${PROXY_URL}/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "{
      \"model\":\"${MODEL_GROUP}\",
      \"messages\":[{\"role\":\"user\",\"content\":\"Say hi in one short sentence.\"}],
      \"stream\": false
    }" | python3 -m json.tool
}

do_cleanup() {
  require_cmd kubectl
  echo "Deleting ${MANIFEST}"
  kubectl delete -f "${MANIFEST}" --ignore-not-found=true
}

do_logs() {
  require_cmd kubectl
  kubectl -n "${NAMESPACE}" logs -f "pod/${POD_NAME}"
}

ACTION="${1:-}"
case "${ACTION}" in
  apply)
    do_apply
    ;;
  status)
    do_status
    ;;
  verify)
    do_verify
    ;;
  cleanup)
    do_cleanup
    ;;
  logs)
    do_logs
    ;;
  *)
    echo "Usage: $0 {apply|status|verify|cleanup|logs}"
    exit 1
    ;;
esac
