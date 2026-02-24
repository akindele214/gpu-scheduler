#!/usr/bin/env bash
set -euo pipefail

# ── configuration ─────────────────────────────────────────────────────────────

SSH_KEY="vast-key"
SSH_USER="root"
SERVER="173.185.79.174:44024"    # server node ip:ssh_port
REMOTE_DIR="/root/gpu-scheduler"
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

# ── parse server ──────────────────────────────────────────────────────────────

SERVER_IP="${SERVER%%:*}"
SERVER_PORT="${SERVER##*:}"
if [ "${SERVER_PORT}" = "${SERVER_IP}" ]; then SERVER_PORT=22; fi

SSH_CMD="ssh -i ${SSH_KEY} -p ${SERVER_PORT} -o StrictHostKeyChecking=no ${SSH_USER}@${SERVER_IP}"

# ── sync source code ─────────────────────────────────────────────────────────

echo "[1/4] Syncing source code to server..."
rsync -av \
  --exclude '.git' \
  --exclude 'bin' \
  --exclude 'vast-key' \
  -e "ssh -i ${SSH_KEY} -p ${SERVER_PORT}" \
  "${PROJECT_DIR}/" "${SSH_USER}@${SERVER_IP}:${REMOTE_DIR}/"

# ── build on server ──────────────────────────────────────────────────────────

echo "[2/4] Building scheduler on server..."
${SSH_CMD} "export PATH=\$PATH:/usr/local/go/bin && cd ${REMOTE_DIR} && go build -o scheduler ./cmd/scheduler"

# ── deploy K8s manifests ──────────────────────────────────────────────────────

echo "[3/4] Applying RBAC to cluster..."
${SSH_CMD} "kubectl apply -f ${REMOTE_DIR}/deploy/rbac.yaml"

# ── prepare config and start scheduler ────────────────────────────────────────

echo "[4/4] Starting scheduler on server..."
${SSH_CMD} "pkill -f '${REMOTE_DIR}/scheduler' || true"
sleep 1

# Ensure mockMode is false for real cluster
${SSH_CMD} "cd ${REMOTE_DIR} && sed -i 's/mockMode: true/mockMode: false/' config.yaml"

${SSH_CMD} \
  "cd ${REMOTE_DIR} && KUBECONFIG=/etc/rancher/k3s/k3s.yaml \
    nohup ./scheduler > scheduler.log 2>&1 &"

echo ""
echo "=== Scheduler deployed to ${SERVER_IP}:${SERVER_PORT} ==="
echo "Logs: ssh -i ${SSH_KEY} -p ${SERVER_PORT} ${SSH_USER}@${SERVER_IP} tail -f ${REMOTE_DIR}/scheduler.log"
