#!/usr/bin/env bash
set -euo pipefail

CERTS_DIR="$(cd "$(dirname "$0")/.." && pwd)/certs"
mkdir -p "${CERTS_DIR}"

echo "=== Generating webhook TLS certificates ==="

# 1. Generate CA
openssl genrsa -out "${CERTS_DIR}/ca.key" 2048
openssl req -x509 -new -nodes \
  -key "${CERTS_DIR}/ca.key" \
  -sha256 -days 3650 \
  -subj "/CN=gpu-scheduler-ca" \
  -out "${CERTS_DIR}/ca.crt"

# 2. Generate server key + CSR
openssl genrsa -out "${CERTS_DIR}/tls.key" 2048

cat > "${CERTS_DIR}/server.cnf" <<EOF
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
prompt = no

[req_distinguished_name]
CN = localhost

[v3_req]
subjectAltName = @alt_names

[alt_names]
DNS.1 = localhost
IP.1 = 127.0.0.1
EOF

openssl req -new \
  -key "${CERTS_DIR}/tls.key" \
  -config "${CERTS_DIR}/server.cnf" \
  -out "${CERTS_DIR}/tls.csr"

# 3. Sign with CA
openssl x509 -req \
  -in "${CERTS_DIR}/tls.csr" \
  -CA "${CERTS_DIR}/ca.crt" \
  -CAkey "${CERTS_DIR}/ca.key" \
  -CAcreateserial \
  -sha256 -days 3650 \
  -extensions v3_req \
  -extfile "${CERTS_DIR}/server.cnf" \
  -out "${CERTS_DIR}/tls.crt"

# Cleanup intermediates
rm -f "${CERTS_DIR}/tls.csr" "${CERTS_DIR}/server.cnf" "${CERTS_DIR}/ca.srl"

echo ""
echo "=== Certificates generated in ${CERTS_DIR}/ ==="
echo "  ca.crt   - CA certificate"
echo "  tls.crt  - Server certificate"
echo "  tls.key  - Server private key"
echo ""
echo "=== CA Bundle (base64) for mutating-webhook.yaml ==="
base64 < "${CERTS_DIR}/ca.crt" | tr -d '\n'
echo ""
