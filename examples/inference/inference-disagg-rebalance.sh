#!/usr/bin/env bash
set -euo pipefail

# Additive disaggregated inference rebalance helper via gpu-scheduler.
#
# This keeps existing workers stable:
#   apply-base        => 1P:1D: prefill-0 + decode-0
#   scale-prefill-up => 2P:1D: adds prefill-1 only
#   scale-decode-up  => 1P:2D: adds decode-1 only
#
# Commands:
#   examples/inference/inference-disagg-rebalance.sh apply-base
#   examples/inference/inference-disagg-rebalance.sh scale-prefill-up
#   examples/inference/inference-disagg-rebalance.sh scale-prefill-down
#   examples/inference/inference-disagg-rebalance.sh scale-decode-up
#   examples/inference/inference-disagg-rebalance.sh scale-decode-down
#   examples/inference/inference-disagg-rebalance.sh status
#   examples/inference/inference-disagg-rebalance.sh verify
#   examples/inference/inference-disagg-rebalance.sh cleanup
#   examples/inference/inference-disagg-rebalance.sh logs prefill-0
#   examples/inference/inference-disagg-rebalance.sh logs prefill-1
#   examples/inference/inference-disagg-rebalance.sh logs decode-0
#   examples/inference/inference-disagg-rebalance.sh logs decode-1
#
# Optional env overrides:
#   NAMESPACE=default
#   SCHEDULER_URL=http://localhost:8888
#   PROXY_URL=http://localhost:8080
#   MODEL_GROUP=Qwen/Qwen2.5-7B-Instruct
#   HF_TOKEN=hf_...  # optional, creates/updates Kubernetes secret hf-token

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

NAMESPACE="${NAMESPACE:-default}"
BASE_MANIFEST="${BASE_MANIFEST:-${REPO_ROOT}/examples/inference/inference-disagg-rebalance-base.yaml}"
ADD_PREFILL_MANIFEST="${ADD_PREFILL_MANIFEST:-${REPO_ROOT}/examples/inference/inference-disagg-rebalance-add-prefill.yaml}"
ADD_DECODE_MANIFEST="${ADD_DECODE_MANIFEST:-${REPO_ROOT}/examples/inference/inference-disagg-rebalance-add-decode.yaml}"
SCHEDULER_URL="${SCHEDULER_URL:-http://localhost:8888}"
PROXY_URL="${PROXY_URL:-http://localhost:8080}"
MODEL_GROUP="${MODEL_GROUP:-Qwen/Qwen2.5-7B-Instruct}"

PREFILL_0_POD="inference-prefill-0"
PREFILL_1_POD="inference-prefill-1"
DECODE_0_POD="inference-decode-0"
DECODE_1_POD="inference-decode-1"

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

wait_for_ready() {
  local pod="$1"
  echo "Waiting for pod/${pod} to become Ready..."
  kubectl -n "${NAMESPACE}" wait --for=condition=Ready "pod/${pod}" --timeout=15m
}

apply_manifest() {
  local manifest="$1"
  ensure_hf_token_secret
  echo "Applying ${manifest}"
  kubectl apply -f "${manifest}"
}

delete_manifest() {
  local manifest="$1"
  echo "Deleting ${manifest}"
  kubectl delete -f "${manifest}" --ignore-not-found=true
}

do_apply_base() {
  require_cmd kubectl
  apply_manifest "${BASE_MANIFEST}"
  wait_for_ready "${PREFILL_0_POD}"
  wait_for_ready "${DECODE_0_POD}"
}

do_scale_prefill_up() {
  require_cmd kubectl
  apply_manifest "${ADD_PREFILL_MANIFEST}"
  wait_for_ready "${PREFILL_1_POD}"
}

do_scale_decode_up() {
  require_cmd kubectl
  apply_manifest "${ADD_DECODE_MANIFEST}"
  wait_for_ready "${DECODE_1_POD}"
}

do_scale_prefill_down() {
  require_cmd kubectl
  delete_manifest "${ADD_PREFILL_MANIFEST}"
}

do_scale_decode_down() {
  require_cmd kubectl
  delete_manifest "${ADD_DECODE_MANIFEST}"
}

do_status() {
  require_cmd kubectl
  echo "Pod status:"
  kubectl -n "${NAMESPACE}" get pod -l app=inference-disagg-rebalance -o wide
  echo
  echo "Service status:"
  kubectl -n "${NAMESPACE}" get svc -l app=inference-disagg-rebalance -o wide
  echo
  echo "Service endpoints:"
  kubectl -n "${NAMESPACE}" get endpoints -l app=inference-disagg-rebalance -o wide
}

do_verify() {
  require_cmd curl
  echo "Scheduler workers:"
  curl -fsS "${SCHEDULER_URL}/api/v1/control/workers" | python3 -m json.tool
  echo
  echo "Worker health via NodePorts:"
  for port in 30101 30102 30103 30104; do
    if curl -fsS -m 5 "http://127.0.0.1:${port}/health" >/dev/null; then
      echo "port ${port}: ok"
    else
      echo "port ${port}: unavailable"
    fi
  done
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
  delete_manifest "${ADD_PREFILL_MANIFEST}"
  delete_manifest "${ADD_DECODE_MANIFEST}"
  delete_manifest "${BASE_MANIFEST}"
}

do_logs() {
  require_cmd kubectl
  local target="${1:-}"
  case "${target}" in
    prefill-0)
      kubectl -n "${NAMESPACE}" logs -f "pod/${PREFILL_0_POD}"
      ;;
    prefill-1)
      kubectl -n "${NAMESPACE}" logs -f "pod/${PREFILL_1_POD}"
      ;;
    decode-0)
      kubectl -n "${NAMESPACE}" logs -f "pod/${DECODE_0_POD}"
      ;;
    decode-1)
      kubectl -n "${NAMESPACE}" logs -f "pod/${DECODE_1_POD}"
      ;;
    *)
      echo "Usage: $0 logs {prefill-0|prefill-1|decode-0|decode-1}"
      exit 1
      ;;
  esac
}

ACTION="${1:-}"
case "${ACTION}" in
  apply-base)
    do_apply_base
    ;;
  scale-prefill-up)
    do_scale_prefill_up
    ;;
  scale-prefill-down)
    do_scale_prefill_down
    ;;
  scale-decode-up)
    do_scale_decode_up
    ;;
  scale-decode-down)
    do_scale_decode_down
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
    echo "Usage: $0 {apply-base|scale-prefill-up|scale-prefill-down|scale-decode-up|scale-decode-down|status|verify|cleanup|logs}"
    exit 1
    ;;
esac
