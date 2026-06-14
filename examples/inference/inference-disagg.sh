#!/usr/bin/env bash
set -euo pipefail

# Disaggregated inference deploy helper via gpu-scheduler.
#
# Commands:
#   examples/inference/inference-disagg.sh apply
#   examples/inference/inference-disagg.sh status
#   examples/inference/inference-disagg.sh verify
#   examples/inference/inference-disagg.sh cleanup
#   examples/inference/inference-disagg.sh logs prefill
#   examples/inference/inference-disagg.sh logs decode
#
# Optional env overrides:
#   NAMESPACE=default
#   MANIFEST=examples/inference/inference-disagg.yaml
#   SCHEDULER_URL=http://localhost:8888
#   PROXY_URL=http://localhost:8080
#   MODEL_GROUP=Qwen/Qwen2.5-7B-Instruct
#   HF_TOKEN=hf_...  # optional, creates/updates Kubernetes secret hf-token

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

NAMESPACE="${NAMESPACE:-default}"
MANIFEST="${MANIFEST:-${REPO_ROOT}/examples/inference/inference-disagg.yaml}"
SCHEDULER_URL="${SCHEDULER_URL:-http://localhost:8888}"
PROXY_URL="${PROXY_URL:-http://localhost:8080}"
MODEL_GROUP="${MODEL_GROUP:-Qwen/Qwen2.5-7B-Instruct}"

PREFILL_POD="inference-prefill"
DECODE_POD="inference-decode"
PREFILL_SVC="inference-prefill-svc"
DECODE_SVC="inference-decode-svc"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1"
    exit 1
  }
}

ensure_hf_token_secret() {
  if [[ -z "${HF_TOKEN:-}" ]]; then
    echo "HF_TOKEN not set; continuing without Hugging Face auth secret"
    return
  fi

  echo "Creating/updating Hugging Face token secret in namespace ${NAMESPACE}"
  kubectl -n "${NAMESPACE}" create secret generic hf-token \
    --from-literal=HF_TOKEN="${HF_TOKEN}" \
    --dry-run=client \
    -o yaml | kubectl apply -f -
}

do_apply() {
  require_cmd kubectl
  ensure_hf_token_secret
  echo "Applying ${MANIFEST}"
  kubectl apply -f "${MANIFEST}"
  echo "Waiting for pod/${PREFILL_POD} to become Ready..."
  kubectl -n "${NAMESPACE}" wait --for=condition=Ready "pod/${PREFILL_POD}" --timeout=15m
  echo "Waiting for pod/${DECODE_POD} to become Ready..."
  kubectl -n "${NAMESPACE}" wait --for=condition=Ready "pod/${DECODE_POD}" --timeout=15m
}

do_status() {
  require_cmd kubectl
  echo "Pod status:"
  kubectl -n "${NAMESPACE}" get pod "${PREFILL_POD}" "${DECODE_POD}" -o wide
  echo
  echo "Service status:"
  kubectl -n "${NAMESPACE}" get svc "${PREFILL_SVC}" "${DECODE_SVC}" -o wide
  echo
  echo "Service endpoints:"
  kubectl -n "${NAMESPACE}" get endpoints "${PREFILL_SVC}" "${DECODE_SVC}" -o wide
}

do_verify() {
  require_cmd curl
  echo "Scheduler workers:"
  curl -fsS "${SCHEDULER_URL}/api/v1/control/workers" | python3 -m json.tool
  echo
  echo "Prefill health via NodePort:"
  curl -fsS -m 5 "http://127.0.0.1:30081/health" >/dev/null
  echo "ok"
  echo
  echo "Decode health via NodePort:"
  curl -fsS -m 5 "http://127.0.0.1:30082/health" >/dev/null
  echo "ok"
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
  local target="${1:-}"
  case "${target}" in
    prefill)
      kubectl -n "${NAMESPACE}" logs -f "pod/${PREFILL_POD}"
      ;;
    decode)
      kubectl -n "${NAMESPACE}" logs -f "pod/${DECODE_POD}"
      ;;
    *)
      echo "Usage: $0 logs {prefill|decode}"
      exit 1
      ;;
  esac
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
    do_logs "${2:-}"
    ;;
  *)
    echo "Usage: $0 {apply|status|verify|cleanup|logs}"
    exit 1
    ;;
esac
