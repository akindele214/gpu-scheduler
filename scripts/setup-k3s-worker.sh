#!/usr/bin/env bash
set -euo pipefail

# ── configuration ─────────────────────────────────────────────────────────────

SSH_KEY="/Users/akindele214/Desktop/Dev/go_projects/go-scheduler/vast-key"
SSH_USER="root"

# k3s server details (ip:ssh_port)
K3S_SERVER="173.185.79.174:44024"
K3S_API_PORT="44169"          # external port mapped to k3s 6443
K3S_TOKEN="K10890f719c8fea460f3cd3ad48e653694e3620c6b1a7c1f6613e37b66213db3830::server:90bcec65f5de5ff2560c4cd576e32650"

# Add worker nodes here as "ip:port" (port defaults to 22 if omitted)
NODES=(
  # 173.185.79.174:44024
  "188.242.219.244:35251"
)

# ── helpers ──────────────────────────────────────────────────────────────────

# Parse server ip and ssh port
SERVER_IP="${K3S_SERVER%%:*}"
SERVER_SSH_PORT="${K3S_SERVER##*:}"
if [ "${SERVER_SSH_PORT}" = "${SERVER_IP}" ]; then SERVER_SSH_PORT=22; fi

SERVER_SSH="ssh -i ${SSH_KEY} -p ${SERVER_SSH_PORT} -o StrictHostKeyChecking=no ${SSH_USER}@${SERVER_IP}"

# ── setup ─────────────────────────────────────────────────────────────────────

for entry in "${NODES[@]}"; do
  # Parse ip and port
  ip="${entry%%:*}"
  port="${entry##*:}"
  if [ "${port}" = "${ip}" ]; then port=22; fi

  echo ""
  echo "=== Setting up k3s worker on ${ip}:${port} ==="

  SSH_CMD="ssh -i ${SSH_KEY} -p ${port} -o StrictHostKeyChecking=no ${SSH_USER}@${ip}"

  # Use a unique node name to avoid hostname collisions (Vast.ai defaults to "ubuntu")
  NODE_NAME="gpu-worker-${ip//./-}"
  echo "Node name: ${NODE_NAME}"

  # ── phase 1: prerequisites ────────────────────────────────────────────────

  # Verify NVIDIA driver
  echo "[1/8] Checking NVIDIA driver..."
  ${SSH_CMD} "nvidia-smi > /dev/null 2>&1" || { echo "ERROR: nvidia-smi failed on ${ip}, skipping"; continue; }
  echo "  NVIDIA driver OK"

  # Install NVIDIA Container Toolkit if not present
  echo "[2/8] Checking NVIDIA Container Toolkit..."
  NVCTK_INSTALLED=$(${SSH_CMD} "nvidia-ctk --version > /dev/null 2>&1 && echo yes || echo no")
  if [ "${NVCTK_INSTALLED}" = "no" ]; then
    echo "  Installing NVIDIA Container Toolkit..."
    ${SSH_CMD} "curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --batch --yes --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg \
      && curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
        sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
        sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list \
      && sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit"
    echo "  Installed"
  else
    echo "  Already installed"
  fi

  # ── phase 2: containerd config (BEFORE k3s install) ─────────────────────

  echo "[3/8] Configuring containerd for NVIDIA runtime..."
  ${SSH_CMD} "sudo mkdir -p /var/lib/rancher/k3s/agent/etc/containerd"
  ${SSH_CMD} "sudo tee /var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl > /dev/null << 'CONTAINERD_EOF'
version = 2

[plugins.\"io.containerd.grpc.v1.cri\".containerd]
  default_runtime_name = \"nvidia\"

[plugins.\"io.containerd.grpc.v1.cri\".containerd.runtimes.nvidia]
  privileged_without_host_devices = false
  runtime_engine = \"\"
  runtime_root = \"\"
  runtime_type = \"io.containerd.runc.v2\"

[plugins.\"io.containerd.grpc.v1.cri\".containerd.runtimes.nvidia.options]
  BinaryName = \"/usr/bin/nvidia-container-runtime\"
CONTAINERD_EOF"

  # ── phase 3: clean up stale node on server ─────────────────────────────

  echo "[4/8] Cleaning up stale node entries on server..."
  ${SERVER_SSH} "kubectl delete secret -n kube-system ${NODE_NAME}.node-password.k3s 2>/dev/null || true; kubectl delete node ${NODE_NAME} 2>/dev/null || true"

  # ── phase 4: k3s agent ─────────────────────────────────────────────────

  K3S_RUNNING=$(${SSH_CMD} "systemctl is-active k3s-agent 2>&1 || true")

  if [ "${K3S_RUNNING}" = "active" ]; then
    echo "[5/8] k3s agent already running, skipping install"
  else
    echo "[5/8] Installing k3s agent..."
    # Clear stale node password on worker
    ${SSH_CMD} "sudo rm -f /etc/rancher/node/password"
    ${SSH_CMD} "sudo rm -rf /var/lib/rancher/k3s/agent/*.crt /var/lib/rancher/k3s/agent/*.key"
    ${SSH_CMD} "curl -sfL https://get.k3s.io | K3S_URL=https://${SERVER_IP}:${K3S_API_PORT} K3S_TOKEN=${K3S_TOKEN} K3S_NODE_NAME=${NODE_NAME} sh -"
  fi

  # ── phase 5: CNI plugins ───────────────────────────────────────────────

  echo "[6/8] Checking CNI plugins..."
  CNI_EXISTS=$(${SSH_CMD} "test -f /opt/cni/bin/bridge && echo yes || echo no")
  if [ "${CNI_EXISTS}" = "no" ]; then
    echo "  Installing CNI plugins..."
    ${SSH_CMD} "sudo mkdir -p /opt/cni/bin && curl -L https://github.com/containernetworking/plugins/releases/download/v1.4.0/cni-plugins-linux-amd64-v1.4.0.tgz | sudo tar -xz -C /opt/cni/bin"
  else
    echo "  Already installed"
  fi

  # ── phase 6: flannel fix ───────────────────────────────────────────────

  echo "[7/8] Fixing flannel binary and config..."
  # Copy flannel binary from k3s data dir
  ${SSH_CMD} "sudo cp /var/lib/rancher/k3s/data/current/bin/cni /opt/cni/bin/flannel 2>/dev/null || true"
  # Symlink flannel config to where CNI expects it
  ${SSH_CMD} "sudo mkdir -p /etc/cni/net.d && sudo ln -sf /var/lib/rancher/k3s/agent/etc/cni/net.d/10-flannel.conflist /etc/cni/net.d/10-flannel.conflist 2>/dev/null || true"

  # Restart agent to pick up CNI/flannel changes
  ${SSH_CMD} "sudo systemctl restart k3s-agent"

  # ── phase 7: iptables port redirect (AFTER restart) ───────────────────
  # Vast.ai maps port 6443 to an external port. After joining, k3s agent
  # tries to connect to the server's advertised address on port 6443 directly.
  # We add the redirect AFTER restart because k3s flushes iptables on start.

  echo "[8/8] Setting up iptables port redirect (6443 -> ${K3S_API_PORT})..."
  # Wait for k3s to finish its iptables setup, then add our rule
  echo "  Waiting for k3s iptables to settle..."
  sleep 15
  ${SSH_CMD} "sudo iptables -t nat -I OUTPUT 1 -d ${SERVER_IP} -p tcp --dport 6443 -j DNAT --to-destination ${SERVER_IP}:${K3S_API_PORT}"
  # Verify the rule is in place
  ${SSH_CMD} "sudo iptables -t nat -L OUTPUT -n --line-numbers | head -5"

  echo ""
  echo "k3s worker setup complete on ${NODE_NAME} (${ip}:${port})"
done

echo ""
echo "=== Done. Set up ${#NODES[@]} worker(s) ==="
echo "Verify with: kubectl get nodes (on server)"

# sudo iptables -t nat -I OUTPUT 1 -d 173.185.79.174 -p tcp --dport 6443 -j DNAT --to-destination 173.185.79.174:44169
# sudo iptables -t nat -L OUTPUT -n --line-numbers
