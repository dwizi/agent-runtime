#!/bin/sh
set -eu

PKI_DIR="/etc/caddy/pki"
CA_KEY="$PKI_DIR/clients-ca.key"
CA_CRT="$PKI_DIR/clients-ca.crt"
CLIENT_KEY="$PKI_DIR/admin-client.key"
CLIENT_CSR="$PKI_DIR/admin-client.csr"
CLIENT_CRT="$PKI_DIR/admin-client.crt"
CLIENT_P12="$PKI_DIR/admin-client.p12"

mkdir -p "$PKI_DIR"

is_pem_cert() {
  path="$1"
  [ -f "$path" ] && command grep -q "BEGIN CERTIFICATE" "$path"
}

is_pem_key() {
  path="$1"
  [ -f "$path" ] && command grep -Eq "BEGIN (RSA |EC )?PRIVATE KEY|BEGIN PRIVATE KEY" "$path"
}

if ! is_pem_cert "$CA_CRT" || ! is_pem_key "$CA_KEY"; then
  rm -f "$CA_CRT" "$CA_KEY" "$PKI_DIR/clients-ca.srl"
  openssl req -x509 -newkey rsa:4096 -nodes -sha256 -days 3650 \
    -subj "/CN=Spinner Admin Client CA" \
    -keyout "$CA_KEY" -out "$CA_CRT"
fi

if ! is_pem_key "$CLIENT_KEY" || ! is_pem_cert "$CLIENT_CRT"; then
  rm -f "$CLIENT_KEY" "$CLIENT_CRT" "$CLIENT_CSR" "$CLIENT_P12"
  openssl req -newkey rsa:2048 -nodes \
    -subj "/CN=spinner-admin-client" \
    -keyout "$CLIENT_KEY" -out "$CLIENT_CSR"
  openssl x509 -req -days 825 -sha256 \
    -in "$CLIENT_CSR" \
    -CA "$CA_CRT" -CAkey "$CA_KEY" -CAcreateserial \
    -out "$CLIENT_CRT"
  openssl pkcs12 -export \
    -inkey "$CLIENT_KEY" \
    -in "$CLIENT_CRT" \
    -certfile "$CA_CRT" \
    -passout pass:spinner \
    -out "$CLIENT_P12"
  rm -f "$CLIENT_CSR"
fi
