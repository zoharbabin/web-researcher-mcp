#!/usr/bin/env bash
# e2e-oauth-docker.sh — stand up a full local Docker deployment (HTTPS reverse
# proxy + a minimal static-JWKS OAuth issuer + the real server image) and run
# an end-to-end pass over the regulated, consent-gated tools (memory_save/
# memory_recall, get_my_analytics, monitor_query_save/monitor_query_check),
# plus a tenant/user isolation check and a GDPR export/erasure check.
#
# This is a manual/local e2e harness, NOT part of `make verify` or CI: it needs
# a Docker daemon (Rancher Desktop, Docker Desktop, etc.) and is deliberately
# heavier than docker-smoke.sh (which only proves the container serves MCP over
# plain HTTP). Use this when you need to validate the full HTTPS+OAuth+
# consent-gated-feature story before a release, not on every commit.
#
# Why a hand-minted JWKS instead of a real IdP: internal/auth/middleware.go
# validates RS256 JWTs against a JWKS fetched from
# ${OAUTH_ISSUER_URL}/.well-known/jwks.json — there is no dev-mode bypass, so
# the only way to drive that code path locally is a real (self-signed) RSA
# keypair + a static JWKS server. This script builds one from scratch every
# run so there is never a stale keypair to debug.
#
# Rancher Desktop / Docker Desktop VM note: only paths under $HOME are shared
# into the container VM by default, so this script does its work under
# $WORK_DIR (default: ~/.cache/web-researcher-mcp/e2e-oauth), not /tmp.
#
# Usage:
#   scripts/e2e-oauth-docker.sh          # run the full pass
#   scripts/e2e-oauth-docker.sh --keep   # leave containers running after the run
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="web-researcher-mcp:e2e-oauth"
WORK_DIR="${WORK_DIR:-$HOME/.cache/web-researcher-mcp/e2e-oauth}"
NET="wrm-e2e-net"
APP="wrm-e2e-app"
PROXY="wrm-e2e-proxy"
ISSUER_CONTAINER="wrm-e2e-jwks"
HTTPS_PORT=8543
JWKS_PORT=9599
KID="e2e-test-key"
ISSUER_URL="http://${ISSUER_CONTAINER}:${JWKS_PORT}"
AUDIENCE="web-researcher-mcp"
KEEP="${1:-}"

pass=0
fail=0
check() {
  local desc="$1" cond="$2"
  if [ "$cond" = "0" ]; then
    echo "  [PASS] $desc"
    pass=$((pass + 1))
  else
    echo "  [FAIL] $desc"
    fail=$((fail + 1))
  fi
}

cleanup() {
  if [ "$KEEP" != "--keep" ]; then
    echo "=== cleanup: removing containers + network"
    docker rm -f "$APP" "$PROXY" "$ISSUER_CONTAINER" >/dev/null 2>&1 || true
    docker network rm "$NET" >/dev/null 2>&1 || true
  else
    echo "=== --keep set: leaving $APP, $PROXY, $ISSUER_CONTAINER running on $NET"
  fi
}
trap cleanup EXIT

echo "=== [1/8] Prep work dir: $WORK_DIR"
rm -rf "$WORK_DIR"
mkdir -p "$WORK_DIR/certs" "$WORK_DIR/www/.well-known"

echo "=== [2/8] Generate self-signed TLS cert for the HTTPS proxy"
openssl req -x509 -nodes -newkey rsa:2048 -days 7 \
  -keyout "$WORK_DIR/certs/server.key" -out "$WORK_DIR/certs/server.crt" \
  -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  >/dev/null 2>&1

echo "=== [3/8] Mint RSA keypair + JWKS + two per-user JWTs"
python3 "$REPO_ROOT/scripts/e2e-oauth-mint-jwt.py" init "$WORK_DIR" "$KID"
cp "$WORK_DIR/jwks.json" "$WORK_DIR/www/.well-known/jwks.json"
USER1=e2e-user-alice
USER2=e2e-user-bob
python3 "$REPO_ROOT/scripts/e2e-oauth-mint-jwt.py" mint "$WORK_DIR" "$USER1" "$KID" "$ISSUER_URL" "$AUDIENCE" > "$WORK_DIR/token1.txt"
python3 "$REPO_ROOT/scripts/e2e-oauth-mint-jwt.py" mint "$WORK_DIR" "$USER2" "$KID" "$ISSUER_URL" "$AUDIENCE" > "$WORK_DIR/token2.txt"
TOKEN1="$(cat "$WORK_DIR/token1.txt")"
TOKEN2="$(cat "$WORK_DIR/token2.txt")"

ADMIN_KEY="$(openssl rand -hex 32)"

cat > "$WORK_DIR/Caddyfile" <<EOF
{
	auto_https off
}

https://localhost:${HTTPS_PORT} {
	tls /certs/server.crt /certs/server.key
	reverse_proxy ${APP}:8080
}
EOF

echo "=== [4/8] Build image ${IMAGE}"
docker build -t "$IMAGE" "$REPO_ROOT" >/dev/null

echo "=== [5/8] Start network + JWKS issuer + app + HTTPS proxy"
docker network rm "$NET" >/dev/null 2>&1 || true
docker network create "$NET" >/dev/null

docker rm -f "$ISSUER_CONTAINER" "$APP" "$PROXY" >/dev/null 2>&1 || true

docker run -d --name "$ISSUER_CONTAINER" --network "$NET" \
  -v "$WORK_DIR/www:/usr/share/nginx/html:ro" \
  nginx:alpine sh -c "sed -i 's/listen  *80;/listen ${JWKS_PORT};/' /etc/nginx/conf.d/default.conf && nginx -g 'daemon off;'" \
  >/dev/null

docker run -d --name "$APP" --network "$NET" \
  -e PORT=8080 \
  -e ADMIN_API_KEY="$ADMIN_KEY" \
  -e MEMORY_ENABLED=true \
  -e USER_ANALYTICS_ENABLED=true \
  -e MONITORING_ENABLED=true \
  -e OAUTH_ISSUER_URL="$ISSUER_URL" \
  -e OAUTH_AUDIENCE="$AUDIENCE" \
  "$IMAGE" >/dev/null

docker run -d --name "$PROXY" --network "$NET" \
  -p "${HTTPS_PORT}:${HTTPS_PORT}" \
  -v "$WORK_DIR/Caddyfile:/etc/caddy/Caddyfile:ro" \
  -v "$WORK_DIR/certs:/certs:ro" \
  caddy:2-alpine >/dev/null

BASE="https://localhost:${HTTPS_PORT}"
CURL_OPTS=(-sk)

echo "=== [6/8] Wait for readiness"
ready=""
for _ in $(seq 1 50); do
  body="$(curl "${CURL_OPTS[@]}" "$BASE/health/ready" 2>/dev/null || true)"
  [ "$body" = "ready" ] && { ready=yes; break; }
  sleep 0.3
done
if [ -z "$ready" ]; then
  echo "!!! server never became ready"
  docker logs "$APP" 2>&1 | tail -40
  exit 1
fi
check "HTTPS proxy + /health/ready" 0

echo "=== [7/8] Grant consent for both users, then drive MCP over HTTPS+OAuth"
for user in "$USER1" "$USER2"; do
  for purpose in memory analytics monitoring; do
    curl "${CURL_OPTS[@]}" -X POST "$BASE/admin/consent" \
      -H "X-Admin-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
      -d "{\"tenant_id\":\"default\",\"user_id\":\"$user\",\"purpose\":\"$purpose\",\"granted\":true}" \
      >/dev/null
  done
done

mcp_init() {
  local token="$1" hdrs
  hdrs="$(mktemp)"
  curl "${CURL_OPTS[@]}" -D "$hdrs" -X POST "$BASE/mcp/" \
    -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
    -H "Authorization: Bearer $token" \
    -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e-oauth","version":"1.0"}}}' \
    >/dev/null
  local sid
  sid="$(grep -i '^Mcp-Session-Id:' "$hdrs" | head -1 | cut -d: -f2- | tr -d '\r' | xargs)"
  rm -f "$hdrs"
  curl "${CURL_OPTS[@]}" -X POST "$BASE/mcp/" \
    -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
    -H "Authorization: Bearer $token" -H "Mcp-Session-Id: $sid" \
    -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' -o /dev/null
  echo "$sid"
}

mcp_call() {
  local token="$1" sid="$2" name="$3" args="$4"
  curl "${CURL_OPTS[@]}" -X POST "$BASE/mcp/" \
    -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
    -H "Authorization: Bearer $token" -H "Mcp-Session-Id: $sid" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"$name\",\"arguments\":$args}}"
}

SID1="$(mcp_init "$TOKEN1")"
SID2="$(mcp_init "$TOKEN2")"
[ -n "$SID1" ] && [ -n "$SID2" ]
check "MCP initialize for two distinct authenticated users" $?

R="$(mcp_call "$TOKEN1" "$SID1" memory_save '{"note":"e2e isolation secret for alice","topic":"e2e"}')"
echo "$R" | grep -q '"status":"ok"'
check "memory_save (alice)" $?

R="$(mcp_call "$TOKEN1" "$SID1" memory_recall '{}')"
echo "$R" | grep -q 'e2e isolation secret for alice'
check "memory_recall sees own note (alice)" $?

R="$(mcp_call "$TOKEN2" "$SID2" memory_recall '{}')"
if echo "$R" | grep -q 'e2e isolation secret for alice'; then leak=1; else leak=0; fi
check "memory_recall does NOT leak alice's note to bob (isolation)" "$leak"
echo "$R" | grep -q '"count":0'
check "bob's memory_recall count is 0" $?

R="$(mcp_call "$TOKEN1" "$SID1" get_my_analytics '{}')"
echo "$R" | grep -q '"userId":"'"$USER1"'"'
check "get_my_analytics scoped to alice" $?

R="$(mcp_call "$TOKEN1" "$SID1" monitor_query_save '{"query":"e2e oauth harness query","provider":"duckduckgo"}')"
echo "$R" | grep -q '"status":"ok"'
check "monitor_query_save (alice)" $?

R="$(mcp_call "$TOKEN2" "$SID2" monitor_query_check '{"query":"e2e oauth harness query","provider":"duckduckgo"}')"
echo "$R" | grep -q '"status":"not_found"'
check "monitor_query_check has no baseline for bob (isolation)" $?

echo "=== [8/8] GDPR export + erasure"
R="$(curl "${CURL_OPTS[@]}" -H "X-Admin-Key: $ADMIN_KEY" "$BASE/admin/data?tenant_id=default&user_id=$USER1")"
echo "$R" | grep -q 'e2e isolation secret for alice'
check "/admin/data export includes alice's memory" $?

curl "${CURL_OPTS[@]}" -X DELETE -H "X-Admin-Key: $ADMIN_KEY" "$BASE/admin/data?tenant_id=default&user_id=$USER1" >/dev/null

R="$(curl "${CURL_OPTS[@]}" -H "X-Admin-Key: $ADMIN_KEY" "$BASE/admin/data?tenant_id=default&user_id=$USER1")"
echo "$R" | grep -qv 'e2e isolation secret for alice'
check "/admin/data erasure removed alice's memory" $?

echo
echo "=== RESULTS: $pass passed, $fail failed"
[ "$fail" -eq 0 ] || exit 1
