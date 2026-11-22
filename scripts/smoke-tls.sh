#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
umask 077

export TLS_UID TLS_GID TLS_CERT_DIR
TLS_UID=$(id -u)
TLS_GID=$(id -g)
TLS_CERT_DIR=$(mktemp -d "$PWD/.lab-tls.XXXXXX")

compose() {
  docker compose -f compose.yaml -f compose.tls.yaml "$@"
}

started=false
cleanup() {
  if [[ "$started" == true ]]; then compose rm --stop --force tls-envoy >/dev/null || true; fi
  rm -rf -- "$TLS_CERT_DIR"
}
trap cleanup EXIT

(
  cd "$TLS_CERT_DIR"
  for ca in ca foreign; do
    openssl req -x509 -nodes -newkey rsa:2048 -sha256 -days 2 \
      -subj "/CN=Authz local $ca" -keyout "$ca.key" -out "$ca.crt"
  done

  cat >server.ext <<'EXT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:localhost,IP:127.0.0.1
EXT
  cat >client.ext <<'EXT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=clientAuth
EXT

  issue() {
    local name=$1 ca=$2 extensions=$3
    openssl req -new -nodes -newkey rsa:2048 -subj "/CN=$name" \
      -keyout "$name.key" -out "$name.csr"
    openssl x509 -req -in "$name.csr" -CA "$ca.crt" -CAkey "$ca.key" \
      -CAcreateserial -out "$name.crt" -days 2 -sha256 -extfile "$extensions.ext"
  }

  issue server ca server
  issue client ca client
  issue foreign-client foreign client
) >"$TLS_CERT_DIR/openssl.log" 2>&1 || { cat "$TLS_CERT_DIR/openssl.log" >&2; exit 1; }

compose run --rm --no-deps tls-envoy --mode validate -c /etc/envoy/envoy.yaml
started=true
compose up -d tls-envoy
ca="$TLS_CERT_DIR/ca.crt"
tls=https://127.0.0.1:8443/healthz
mtls=https://127.0.0.1:8444/healthz

ready=false
for _ in {1..30}; do
  if curl -fsS --max-time 3 --cacert "$ca" "$tls" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  compose logs --no-color tls-envoy >&2
  exit 1
fi

curl -fsS --max-time 5 --cacert "$ca" --cert "$TLS_CERT_DIR/client.crt" \
  --key "$TLS_CERT_DIR/client.key" "$mtls" >/dev/null

expect_rejection() {
  local label=$1
  shift
  local code
  if code=$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' "$@" 2>"$TLS_CERT_DIR/curl.log"); then
    printf '%s unexpectedly reached HTTP (status %s)\n' "$label" "$code" >&2
    return 1
  fi
  if [[ "$code" != 000 ]] || ! grep -Eiq 'certificate|SSL|TLS|connection reset' "$TLS_CERT_DIR/curl.log"; then
    cat "$TLS_CERT_DIR/curl.log" >&2
    return 1
  fi
}

expect_rejection 'Untrusted server CA' --cacert "$TLS_CERT_DIR/foreign.crt" "$tls"
expect_rejection 'Missing client certificate' --cacert "$ca" "$mtls"
expect_rejection 'Untrusted client certificate' --cacert "$ca" \
  --cert "$TLS_CERT_DIR/foreign-client.crt" --key "$TLS_CERT_DIR/foreign-client.key" "$mtls"

echo 'Passed TLS and mTLS checks, including certificate rejection'
