#!/usr/bin/env bash
# Development only. Never issue production identities with this CA.
set -euo pipefail
umask 077
# Git Bash must not turn the certificate subject into a Windows path.
export MSYS_NO_PATHCONV=1

target="${1:-../.local/grpc-tls}"
if [[ -e "$target" ]]; then
  echo "Refusing to overwrite an existing certificate directory: $target" >&2
  exit 1
fi
command -v openssl >/dev/null || { echo "OpenSSL is required" >&2; exit 1; }
mkdir -p -- "$target"
chmod 700 "$target"
# Use an explicit local config instead of an inherited machine-wide OpenSSL path.
cat > "$target/openssl.cnf" <<'EOF'
[req]
distinguished_name = dn
prompt = no
[dn]
CN = OSMI LOCAL DEVELOPMENT ONLY
EOF
export OPENSSL_CONF="$target/openssl.cnf"

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout "$target/dev-ca.key" -out "$target/ca.pem" -sha256 -days 30 \
  -subj "/CN=OSMI LOCAL DEVELOPMENT ONLY" \
  -addext 'basicConstraints=critical,CA:TRUE' \
  -addext 'keyUsage=critical,keyCertSign,cRLSign'

cat > "$target/server.ext" <<'EOF'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=serverAuth
subjectAltName=DNS:osmi-server,DNS:localhost,IP:127.0.0.1,IP:::1
EOF
cat > "$target/gateway.ext" <<'EOF'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=clientAuth
subjectAltName=DNS:osmi-gateway
EOF

for service in server gateway; do
  openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -keyout "$target/$service.key" -out "$target/$service.csr" \
    -subj "/CN=OSMI LOCAL $service"
  serial="$(openssl rand -hex 16)"
  openssl x509 -req -in "$target/$service.csr" \
    -CA "$target/ca.pem" -CAkey "$target/dev-ca.key" -set_serial "0x$serial" \
    -out "$target/$service.pem" -sha256 -days 7 -extfile "$target/$service.ext"
done
echo "Development certificates created in: $target"
echo "Keep keys outside Git. Configure both services before restarting them."
