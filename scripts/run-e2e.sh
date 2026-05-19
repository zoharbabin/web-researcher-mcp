#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BINARY="$PROJECT_ROOT/web-researcher-mcp"
PID=""

cleanup() {
    if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
        kill "$PID" 2>/dev/null || true
        wait "$PID" 2>/dev/null || true
    fi
    rm -f "$BINARY"
    echo "[e2e] Cleanup complete."
}

trap cleanup EXIT

echo "[e2e] Building binary..."
cd "$PROJECT_ROOT"
CGO_ENABLED=0 go build -o "$BINARY" ./cmd/web-researcher-mcp

echo "[e2e] Starting server in STDIO mode..."

# Create a temporary file for the response
RESPONSE_FILE=$(mktemp)

# Send MCP initialize request via stdin and capture response
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"e2e-test","version":"1.0.0"}}}' \
    | timeout 10 "$BINARY" > "$RESPONSE_FILE" 2>/dev/null &
PID=$!

# Wait for the process to finish (it will exit after processing stdin EOF)
wait "$PID" 2>/dev/null || true
PID=""

echo "[e2e] Verifying response..."

if [[ ! -s "$RESPONSE_FILE" ]]; then
    echo "[e2e] FAIL: No response received from server."
    rm -f "$RESPONSE_FILE"
    exit 1
fi

RESPONSE=$(cat "$RESPONSE_FILE")
rm -f "$RESPONSE_FILE"

# Check that response contains expected MCP initialize result
if echo "$RESPONSE" | grep -q '"result"'; then
    echo "[e2e] PASS: Received valid initialize response."
    echo "[e2e] Response: $RESPONSE"
    exit 0
else
    echo "[e2e] FAIL: Response does not contain expected result."
    echo "[e2e] Response: $RESPONSE"
    exit 1
fi
