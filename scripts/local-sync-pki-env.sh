#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
ENV_FILE="$ROOT_DIR/.env"
ENV_EXAMPLE="$ROOT_DIR/.env.example"
PKI_DIR="$ROOT_DIR/ops/caddy/pki"
BACKUP_DONE=0
BACKUP_PATH=""

if [ ! -f "$ENV_FILE" ]; then
  if [ -f "$ENV_EXAMPLE" ]; then
    cp "$ENV_EXAMPLE" "$ENV_FILE"
  else
    touch "$ENV_FILE"
  fi
fi

mkdir -p "$PKI_DIR"

wait_for_file() {
  path="$1"
  attempts="${2:-20}"
  index=0
  while [ "$index" -lt "$attempts" ]; do
    if [ -f "$path" ]; then
      return 0
    fi
    index=$((index + 1))
    sleep 1
  done
  return 1
}

set_if_empty() {
  key="$1"
  value="$2"
  if ! grep -Eq "^${key}=" "$ENV_FILE"; then
    backup_env_once
    printf "%s=%s\n" "$key" "$value" >>"$ENV_FILE"
    return 0
  fi
  current="$(sed -n "s/^${key}=//p" "$ENV_FILE" | head -n 1)"
  if [ -n "$current" ]; then
    return 0
  fi
  backup_env_once
  awk -v key="$key" -v value="$value" '
    BEGIN { done = 0 }
    {
      if ($0 ~ ("^" key "=") && done == 0) {
        print key "=" value
        done = 1
      } else {
        print $0
      }
    }
  ' "$ENV_FILE" >"${ENV_FILE}.tmp"
  mv "${ENV_FILE}.tmp" "$ENV_FILE"
}

backup_env_once() {
  if [ "$BACKUP_DONE" -eq 1 ]; then
    return 0
  fi
  stamp="$(date +%Y%m%d%H%M%S)"
  BACKUP_PATH="${ENV_FILE}.bak.${stamp}"
  cp "$ENV_FILE" "$BACKUP_PATH"
  BACKUP_DONE=1
}

if ! wait_for_file "$PKI_DIR/clients-ca.crt" 30; then
  echo "warning: $PKI_DIR/clients-ca.crt not found yet, skipped .env sync" >&2
  exit 0
fi

if ! wait_for_file "$PKI_DIR/admin-client.crt" 30; then
  echo "warning: $PKI_DIR/admin-client.crt not found yet, skipped .env sync" >&2
  exit 0
fi

if ! wait_for_file "$PKI_DIR/admin-client.key" 30; then
  echo "warning: $PKI_DIR/admin-client.key not found yet, skipped .env sync" >&2
  exit 0
fi

is_pem_cert() {
  path="$1"
  command grep -q "BEGIN CERTIFICATE" "$path"
}

is_pem_key() {
  path="$1"
  command grep -Eq "BEGIN (RSA |EC )?PRIVATE KEY|BEGIN PRIVATE KEY" "$path"
}

if ! is_pem_cert "$PKI_DIR/clients-ca.crt" || ! is_pem_cert "$PKI_DIR/admin-client.crt" || ! is_pem_key "$PKI_DIR/admin-client.key"; then
  echo "warning: pki files exist but are not valid PEM yet, skipped .env sync" >&2
  exit 0
fi

set_if_empty "SPINNER_ADMIN_TLS_CA_FILE" "$PKI_DIR/clients-ca.crt"
set_if_empty "SPINNER_ADMIN_TLS_CERT_FILE" "$PKI_DIR/admin-client.crt"
set_if_empty "SPINNER_ADMIN_TLS_KEY_FILE" "$PKI_DIR/admin-client.key"

if [ "$BACKUP_DONE" -eq 1 ]; then
  echo "synced local mTLS paths to $ENV_FILE (backup: $BACKUP_PATH)"
else
  echo "no sync changes needed in $ENV_FILE"
fi
