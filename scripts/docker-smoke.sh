#!/usr/bin/env bash
# docker-smoke.sh — build the shipped Docker image and validate it serves MCP
# over HTTP end-to-end, exactly as a user running `docker run -p ... -e PORT=...`
# would. This is the regression guard for the main.go HTTP-lifecycle fix: a
# container with PORT set but no stdin attached must stay alive (not exit on
# stdin EOF) and answer real MCP requests.
#
# Network-free w.r.t. secrets: DuckDuckGo is the zero-config search fallback, so
# no API keys are needed. Chromium is baked into the image but is NOT exercised
# here (no scrape against a JS page) to keep the smoke deterministic and fast.
#
# Usage: scripts/docker-smoke.sh [image-tag]
set -euo pipefail

IMAGE="${1:-web-researcher-mcp:smoke}"
CONTAINER="wr-smoke-$$"
HOST_PORT=18080
CONTAINER_PORT=8080
BASE="http://127.0.0.1:${HOST_PORT}"

cleanup() {
  echo "--- cleanup: removing container ${CONTAINER}"
  docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "=== [1/4] Build image ${IMAGE}"
docker build -t "${IMAGE}" .

echo "=== [2/4] Run container (PORT=${CONTAINER_PORT}, no -i: stdin is EOF)"
# Deliberately NO -i / -t: this reproduces the production deployment shape that
# the lifecycle fix addresses. If main.go fell through to RunSTDIO the container
# would exit within milliseconds and the readiness poll below would fail.
docker run -d --name "${CONTAINER}" \
  -p "${HOST_PORT}:${CONTAINER_PORT}" \
  -e "PORT=${CONTAINER_PORT}" \
  "${IMAGE}" >/dev/null

# Helper: dump logs and fail.
fail() {
  echo "!!! $1"
  echo "--- docker logs ${CONTAINER}:"
  docker logs "${CONTAINER}" 2>&1 || true
  echo "--- container state:"
  docker inspect -f '{{.State.Status}} (exit {{.State.ExitCode}})' "${CONTAINER}" 2>&1 || true
  exit 1
}

echo "=== [3/4] Poll ${BASE}/health/ready (15s budget)"
ready=""
for _ in $(seq 1 75); do
  # If the container has already exited, stop polling a dead server immediately.
  state="$(docker inspect -f '{{.State.Status}}' "${CONTAINER}" 2>/dev/null || echo gone)"
  if [ "${state}" != "running" ]; then
    fail "container is '${state}' before readiness — the HTTP lifecycle regression?"
  fi
  body="$(curl -fsS "${BASE}/health/ready" 2>/dev/null || true)"
  if [ "${body}" = "ready" ]; then
    ready="yes"
    break
  fi
  sleep 0.2
done
[ -n "${ready}" ] || fail "server not ready within 15s"
echo "    /health/ready -> ready"

echo "=== [4/4] Drive MCP over HTTP: initialize + web_search"
# The Streamable HTTP transport requires a dual Accept and returns SSE-framed
# bodies; the Mcp-Session-Id from initialize must be propagated to tools/call.
ACCEPT='Accept: application/json, text/event-stream'
CT='Content-Type: application/json'

init_headers="$(mktemp)"
init_body="$(curl -fsS -D "${init_headers}" \
  -H "${CT}" -H "${ACCEPT}" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"docker-smoke","version":"1.0.0"}}}' \
  "${BASE}/mcp/" 2>/dev/null)" || fail "initialize request failed"

# Header names are case-insensitive; grep -i and trim CR.
SESSION_ID="$(grep -i '^Mcp-Session-Id:' "${init_headers}" | head -1 | cut -d: -f2- | tr -d '\r' | xargs || true)"
rm -f "${init_headers}"
[ -n "${SESSION_ID}" ] || fail "no Mcp-Session-Id returned by initialize (body: ${init_body})"
echo "    initialize -> session ${SESSION_ID}"

# notifications/initialized (fire-and-forget; no response body expected).
curl -fsS -o /dev/null \
  -H "${CT}" -H "${ACCEPT}" -H "Mcp-Session-Id: ${SESSION_ID}" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  "${BASE}/mcp/" 2>/dev/null || true

search_body="$(curl -fsS \
  -H "${CT}" -H "${ACCEPT}" -H "Mcp-Session-Id: ${SESSION_ID}" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"web_search","arguments":{"query":"docker smoke test"}}}' \
  "${BASE}/mcp/" 2>/dev/null)" || fail "web_search request failed"

# The SSE body carries a `data: {...}` line with the JSON-RPC result. Accept any
# well-formed result for id 2; this proves the transport + tool plumbing works
# (provider results are not asserted — DuckDuckGo may rate-limit in CI).
echo "${search_body}" | grep -q '"id":2' || fail "web_search returned no result for id 2 (body: ${search_body})"
echo "    web_search -> result received"

echo "=== docker-smoke PASSED"
