#!/usr/bin/env bash
set -euo pipefail

# ── configuration ─────────────────────────────────────────────────────────────

SSH_KEY="vast-key"
SSH_USER="root"
REMOTE_DIR="/root/gpu-agent"
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

# Server details (for SSH tunnel from workers)
SERVER="23.135.236.7:7454"
SERVER_IP="${SERVER%%:*}"
SERVER_SSH_PORT="${SERVER##*:}"
if [ "${SERVER_SSH_PORT}" = "${SERVER_IP}" ]; then SERVER_SSH_PORT=22; fi

# Add nodes here as "ip:port:role" (role = server or worker)
NODES=(
  # "23.135.236.7:7454:server"
  "23.135.236.7:30508:worker"
)

# ── deploy ────────────────────────────────────────────────────────────────────

for entry in "${NODES[@]}"; do
  IFS=':' read -r ip port role <<< "${entry}"

  echo ""
  echo "=== Deploying to ${ip}:${port} (${role}) ==="

  SSH_CMD="ssh -i ${SSH_KEY} -p ${port} -o StrictHostKeyChecking=no ${SSH_USER}@${ip}"

  # Get the node's k3s node name
  if [ "${role}" = "server" ]; then
    NODE_NAME=$(${SSH_CMD} "hostname")
  else
    NODE_NAME="gpu-worker-${ip//./-}"
  fi
  echo "Node name: ${NODE_NAME}"

  # Kill existing agent if running
  # ${SSH_CMD} "pkill -f gpu-agent || true"

  # Sync source code to node
  echo "Syncing source code..."
  rsync -av \
    --exclude '.git' \
    --exclude 'bin' \
    --exclude 'vast-key' \
    -e "ssh -i ${SSH_KEY} -p ${port}" \
    "${PROJECT_DIR}/" "${SSH_USER}@${ip}:${REMOTE_DIR}/"

  # Build on node
  echo "Building gpu-agent on node..."
  ${SSH_CMD} "export PATH=\$PATH:/usr/local/go/bin && cd ${REMOTE_DIR} && go build -o gpu-agent ./cmd/gpu-agent"

  # For worker nodes: set up SSH tunnel to reach scheduler on server
  if [ "${role}" = "worker" ]; then
    echo "Setting up SSH tunnel to scheduler on server..."
    ${SSH_CMD} "pkill -f 'ssh.*-L 8888:localhost:8888' || true"
    sleep 1

    # Copy SSH key to worker so it can tunnel to server
    rsync -az -e "ssh -i ${SSH_KEY} -p ${port} -o StrictHostKeyChecking=no" \
      "${SSH_KEY}" "${SSH_USER}@${ip}:~/.ssh/tunnel-key"
    ${SSH_CMD} "chmod 600 ~/.ssh/tunnel-key"

    # Start SSH tunnel in background
    ${SSH_CMD} \
      "nohup ssh -i ~/.ssh/tunnel-key -p ${SERVER_SSH_PORT} \
        -L 8888:localhost:8888 -N \
        -o StrictHostKeyChecking=no \
        ${SSH_USER}@${SERVER_IP} \
        > /dev/null 2>&1 &"
    sleep 2
    echo "SSH tunnel established"
  fi

  # Start agent — all nodes use localhost:8888
  ${SSH_CMD} \
    "nohup ${REMOTE_DIR}/gpu-agent \
      --node-name=${NODE_NAME} \
      --scheduler-url=http://localhost:8888 \
      --interval=5s \
      > ${REMOTE_DIR}/agent.log 2>&1 &"

  echo "Agent started on ${NODE_NAME} (${ip}:${port})"
done

echo ""
echo "=== Done. Deployed to ${#NODES[@]} node(s) ==="


# ssh -i vast-key -p 30508 root@23.135.236.7 "chmod 600 ~/.ssh/tunnel-key && ssh -f -i ~/.ssh/tunnel-key -p 7454 -L 8888:localhost:8888 -N -o StrictHostKeyChecking=no root@23.135.236.7"