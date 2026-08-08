#!/usr/bin/env bash
# k8s-ci-session-continuity.sh — automated counterpart to the manual
# multi-pod Redis session-continuity check documented in
# docs/K8S_LOCAL_DEV.md (issue #495). Deploys 3 replicas of the server + a
# Redis backend from deploy/k8s-ci/ onto a `kind` cluster, then proves a
# sequential_search session created against one pod is readable — via
# get_research_session — from a DIFFERENT pod, by port-forwarding to each
# pod's own IP individually (deterministic: no reliance on kube-proxy
# load-balancing happening to spread 3+ requests across replicas).
#
# Usage:
#   scripts/k8s-ci-session-continuity.sh                  # creates + deletes its own kind cluster
#   KIND_CLUSTER_EXISTS=true scripts/k8s-ci-session-continuity.sh   # reuses an already-running
#                                                                     cluster (the CI job creates one
#                                                                     via helm/kind-action first)
# Requires: kind (unless KIND_CLUSTER_EXISTS=true), kubectl, docker, curl, jq,
# openssl. Exits non-zero (and dumps pod logs) on any failure, so the CI job
# that calls this fails loudly on a real regression.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER="${KIND_CLUSTER_NAME:-wrm-ci-495}"
IMAGE="web-researcher-mcp:ci-test"
NS="web-researcher"
PF_PORT=18080 # local port reused for each pod's port-forward, one at a time
CLUSTER_EXISTS="${KIND_CLUSTER_EXISTS:-false}"

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

# kill_background_jobs is a POSIX-safe substitute for `jobs -p | xargs -r
# kill` — `xargs -r` (skip invocation on empty input) is GNU-only and this
# script must also run on macOS/BSD xargs for local pre-PR validation.
kill_background_jobs() {
  local pids
  pids="$(jobs -p)"
  if [ -n "${pids}" ]; then
    # shellcheck disable=SC2086 # word-splitting is intended: one PID per arg
    kill ${pids} >/dev/null 2>&1 || true
  fi
}

cleanup() {
  echo "=== cleanup: stopping any lingering port-forward"
  kill_background_jobs
  if [ "${CLUSTER_EXISTS}" != "true" ]; then
    echo "=== cleanup: deleting kind cluster ${CLUSTER}"
    kind delete cluster --name "${CLUSTER}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

echo "=== [1/7] Build image ${IMAGE}"
docker build -t "${IMAGE}" "${REPO_ROOT}" >/dev/null

if [ "${CLUSTER_EXISTS}" = "true" ]; then
  echo "=== [2/7] Reusing existing kind cluster ${CLUSTER}; loading the image"
  kind load docker-image "${IMAGE}" --name "${CLUSTER}"
else
  echo "=== [2/7] Create kind cluster ${CLUSTER} and load the image"
  kind create cluster --name "${CLUSTER}" --wait 120s
  kind load docker-image "${IMAGE}" --name "${CLUSTER}"
fi

echo "=== [3/7] Apply deploy/k8s-ci/ manifests (namespace + redis)"
kubectl apply -f "${REPO_ROOT}/deploy/k8s-ci/00-namespace.yaml"
kubectl apply -f "${REPO_ROOT}/deploy/k8s-ci/01-redis.yaml"

echo "=== [4/7] Create app secret (CACHE_ENCRYPTION_KEY; required once REDIS_URL is set)"
kubectl create secret generic web-researcher-secrets \
  --namespace "${NS}" \
  --from-literal=CACHE_ENCRYPTION_KEY="$(openssl rand -hex 32)" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "=== [5/7] Apply app deployment (3 replicas) and wait for readiness"
kubectl apply -f "${REPO_ROOT}/deploy/k8s-ci/02-app.yaml"
kubectl -n "${NS}" wait --for=condition=Available deployment/web-researcher-mcp --timeout=180s
kubectl -n "${NS}" wait --for=condition=Ready pod -l app=web-researcher-mcp --timeout=180s
kubectl -n "${NS}" wait --for=condition=Ready pod -l app=redis --timeout=120s

# Portable read-into-array (avoids `mapfile`, absent from macOS's default
# bash 3.2 — GitHub Actions runners have bash 5.x, but this script is also
# meant to be runnable locally on macOS for pre-PR validation).
PODS=()
while IFS= read -r pod; do
  [ -n "${pod}" ] && PODS+=("${pod}")
done < <(kubectl -n "${NS}" get pods -l app=web-researcher-mcp -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
echo "    pods: ${PODS[*]}"
if [ "${#PODS[@]}" -lt 3 ]; then
  echo "!!! expected 3 pods, got ${#PODS[@]}"
  exit 1
fi

# port_forward_pod starts a background port-forward straight to one pod's own
# IP (not the Service), so each RPC in this script deterministically lands on
# the named pod rather than whichever replica kube-proxy happens to pick. Kills
# any previous forward first since PF_PORT is reused across pods.
port_forward_pod() {
  local pod="$1"
  kill_background_jobs
  wait >/dev/null 2>&1 || true
  kubectl -n "${NS}" port-forward "pod/${pod}" "${PF_PORT}:8080" >/tmp/pf-"${pod}".log 2>&1 &
  local pf_pid=$!
  # Poll /health/ready instead of a fixed sleep so this isn't flaky under
  # kind's variable CI startup latency.
  for _ in $(seq 1 50); do
    if curl -fsS "http://127.0.0.1:${PF_PORT}/health/ready" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  echo "!!! port-forward to ${pod} never became ready"
  cat /tmp/pf-"${pod}".log
  kill "${pf_pid}" >/dev/null 2>&1 || true
  return 1
}

rpc() {
  local method="$1" params="$2"
  curl -fsS \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}" \
    "http://127.0.0.1:${PF_PORT}/mcp/"
}

# extract_json pulls the JSON-RPC data payload out of either a plain
# application/json body or an SSE `data: {...}` frame (mirrors
# tests/e2e/http_e2e_test.go's parseRPCBody, in bash).
extract_json() {
  local body="$1"
  if echo "${body}" | jq -e . >/dev/null 2>&1; then
    echo "${body}"
    return
  fi
  echo "${body}" | grep '^data:' | head -1 | sed 's/^data: *//'
}

echo "=== [6/7] Drive sequential_search across 3 distinct pods"

echo "--- initialize against pod 1 (${PODS[0]})"
port_forward_pod "${PODS[0]}"
init_resp="$(rpc initialize '{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"k8s-ci-495","version":"1.0.0"}}')"
check "initialize on pod 1 succeeded" "$(echo "$(extract_json "${init_resp}")" | jq -e '.result' >/dev/null 2>&1; echo $?)"
curl -fsS -o /dev/null \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  "http://127.0.0.1:${PF_PORT}/mcp/" || true

echo "--- sequential_search step 1 on pod 1 (${PODS[0]}): create the session"
step1_params='{"name":"sequential_search","arguments":{"searchStep":"k8s-ci-495: establishing baseline on pod 1","stepNumber":1,"nextStepNeeded":true,"researchGoal":"prove cross-pod Redis session continuity (#495)"}}'
step1_resp="$(rpc tools/call "${step1_params}")"
step1_json="$(extract_json "${step1_resp}")"
session_id="$(echo "${step1_json}" | jq -r '.result.structuredContent.sessionId // empty')"
check "sequential_search step 1 returned a sessionId" "$([ -n "${session_id}" ] && echo 0 || echo 1)"
echo "    sessionId: ${session_id}"

echo "--- sequential_search step 2 on pod 2 (${PODS[1]}): same sessionId, different pod"
port_forward_pod "${PODS[1]}"
step2_params="{\"name\":\"sequential_search\",\"arguments\":{\"searchStep\":\"k8s-ci-495: continuing on pod 2 — session must have survived the pod switch\",\"stepNumber\":2,\"nextStepNeeded\":true,\"sessionId\":\"${session_id}\"}}"
step2_resp="$(rpc tools/call "${step2_params}")"
step2_json="$(extract_json "${step2_resp}")"
step2_is_error="$(echo "${step2_json}" | jq -r '.result.isError // false')"
check "sequential_search step 2 on a different pod did NOT error (session found)" "$([ "${step2_is_error}" = "false" ] && echo 0 || echo 1)"
step2_returned_id="$(echo "${step2_json}" | jq -r '.result.structuredContent.sessionId // empty')"
check "sequential_search step 2 echoed the SAME sessionId" "$([ "${step2_returned_id}" = "${session_id}" ] && echo 0 || echo 1)"

echo "--- get_research_session on pod 3 (${PODS[2]}): a THIRD pod, never touched by steps 1 or 2"
port_forward_pod "${PODS[2]}"
get_params="{\"name\":\"get_research_session\",\"arguments\":{\"sessionId\":\"${session_id}\"}}"
get_resp="$(rpc tools/call "${get_params}")"
get_json="$(extract_json "${get_resp}")"
get_is_error="$(echo "${get_json}" | jq -r '.result.isError // false')"
check "get_research_session on pod 3 did NOT error (session visible from a third pod)" "$([ "${get_is_error}" = "false" ] && echo 0 || echo 1)"
get_step_count="$(echo "${get_json}" | jq -r '.result.structuredContent.stepCount // 0')"
check "session recovered on pod 3 shows both recorded steps (stepCount == 2)" "$([ "${get_step_count}" = "2" ] && echo 0 || echo 1)"
get_goal="$(echo "${get_json}" | jq -r '.result.structuredContent.researchGoal // empty')"
check "session recovered on pod 3 has the researchGoal set from pod 1's step 1" "$([ "${get_goal}" = "prove cross-pod Redis session continuity (#495)" ] && echo 0 || echo 1)"

echo "=== [7/7] Results"
echo "  ${pass} passed, ${fail} failed"
if [ "${fail}" -gt 0 ]; then
  echo "--- pod logs (all 3 replicas) for diagnosis ---"
  for pod in "${PODS[@]}"; do
    echo "--- ${pod} ---"
    kubectl -n "${NS}" logs "${pod}" --tail=100 || true
  done
  exit 1
fi
echo "=== multi-pod Redis session continuity PASSED"
