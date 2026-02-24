# k3s GPU Setup Guide

Tested on Ubuntu with NVIDIA A100-SXM4-40GB, Driver 570.195.03, CUDA 12.8.

## Prerequisites

1. **Verify NVIDIA driver is working:**

   ```bash
   nvidia-smi
   ```
2. **Install NVIDIA Container Toolkit** (if not already installed):

   ```bash
   curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg \
     && curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
       sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
       sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

   sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
   ```

   Verify installation:

   ```bash
   nvidia-ctk --version
   ```

## Pre-configure containerd for k3s (BEFORE installing k3s)

This is the key step - configure nvidia runtime BEFORE k3s installation so k3s picks it up automatically.

```bash
sudo mkdir -p /var/lib/rancher/k3s/agent/etc/containerd

sudo tee /var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl > /dev/null << 'EOF'
version = 2

[plugins."io.containerd.grpc.v1.cri".containerd]
  default_runtime_name = "nvidia"

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia]
  privileged_without_host_devices = false
  runtime_engine = ""
  runtime_root = ""
  runtime_type = "io.containerd.runc.v2"

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia.options]
  BinaryName = "/usr/bin/nvidia-container-runtime"
EOF
```

## Install k3s

```bash
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="server --advertise-address=163.5.212.79 --tls-san=163.5.212.79 --node-external-ip=163.5.212.79" sh -
```

## Fix kubeconfig permissions

```bash
mkdir -p ~/.kube && sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config && sudo chown $(id -u):$(id -g) ~/.kube/config
export KUBECONFIG=~/.kube/config && echo 'export KUBECONFIG=~/.kube/config' >> ~/.bashrc
```

Verify:

```bash
kubectl get nodes
```

## Install CNI plugins and flannel (if missing)

If the node stays NotReady or pods fail with "failed to find plugin bridge", install CNI plugins and fix flannel:

```bash
sudo mkdir -p /opt/cni/bin
curl -L https://github.com/containernetworking/plugins/releases/download/v1.4.0/cni-plugins-linux-amd64-v1.4.0.tgz | sudo tar -xz -C /opt/cni/bin
sudo cp /var/lib/rancher/k3s/data/current/bin/cni /opt/cni/bin/flannel
```

k3s places the flannel config in its own directory but the CNI subsystem looks in `/etc/cni/net.d/`. Symlink it:

```bash
sudo mkdir -p /etc/cni/net.d
sudo ln -sf /var/lib/rancher/k3s/agent/etc/cni/net.d/10-flannel.conflist /etc/cni/net.d/10-flannel.conflist
sudo systemctl restart k3s
```

## Deploy NVIDIA Device Plugin

Use this simplified manifest (no node selector):

```bash
cat << 'EOF' | kubectl apply -f -
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: nvidia-device-plugin-daemonset
  namespace: kube-system
spec:
  selector:
    matchLabels:
      name: nvidia-device-plugin-ds
  template:
    metadata:
      labels:
        name: nvidia-device-plugin-ds
    spec:
      tolerations:
      - key: nvidia.com/gpu
        operator: Exists
        effect: NoSchedule
      containers:
      - image: nvcr.io/nvidia/k8s-device-plugin:v0.14.1
        name: nvidia-device-plugin-ctr
        env:
        - name: FAIL_ON_INIT_ERROR
          value: "false"
        securityContext:
          allowPrivilegeEscalation: false
          capabilities:
            drop: ["ALL"]
        volumeMounts:
        - name: device-plugin
          mountPath: /var/lib/kubelet/device-plugins
      volumes:
      - name: device-plugin
        hostPath:
          path: /var/lib/kubelet/device-plugins
EOF
```

## Verify GPU is advertised

Wait for the device plugin pod to be Running:

```bash
kubectl get pods -n kube-system -l name=nvidia-device-plugin-ds
```

Check GPU resources:

```bash
kubectl describe node | grep nvidia.com/gpu
```

Expected output:

```
  nvidia.com/gpu:     1
  nvidia.com/gpu:     1
  nvidia.com/gpu     0
```

(Capacity: 1, Allocatable: 1, Used: 0)

## Test GPU access (optional)

```bash
kubectl run gpu-test --rm -it --restart=Never \
  --image=nvidia/cuda:12.0.0-base-ubuntu22.04 \
  --limits=nvidia.com/gpu=1 \
  -- nvidia-smi
```

## Troubleshooting

### Device plugin shows 0 desired pods

The official NVIDIA device plugin manifest has a node selector. Use the simplified manifest above which removes it.

### kubeconfig permission denied after k3s restart

Re-run the kubeconfig fix:

```bash
sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
sudo chown $(id -u):$(id -g) ~/.kube/config
export KUBECONFIG=~/.kube/config
```

### CNI plugin errors / Node stuck NotReady

Install CNI plugins and symlink flannel config as shown above. k3s writes the flannel config to `/var/lib/rancher/k3s/agent/etc/cni/net.d/` but the CNI subsystem expects it in `/etc/cni/net.d/`.

### NVIDIA driver/library version mismatch

If `nvidia-smi` fails with "Driver/library version mismatch", the NVIDIA Container Toolkit may have updated libraries without reloading the kernel module. Reboot the instance to sync them.

### Device plugin can't find libnvidia-ml.so.1

The containerd nvidia runtime is not configured correctly. Make sure you created the config.toml.tmpl BEFORE installing k3s, or reconfigure and restart k3s.
