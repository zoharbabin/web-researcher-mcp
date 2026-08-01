# Local Kubernetes Parity Testing

Manifests under [`deploy/k8s-local/`](../deploy/k8s-local/) reproduce the
production topology (`docs/DEPLOYMENT.md`'s [Kubernetes](DEPLOYMENT.md#kubernetes)
section) on a local single-node cluster: multi-pod HPA scaling, OAuth 2.1 auth,
`REDIS_URL` cross-pod session/cache/rate-limit state, and namespace-scoped
`NetworkPolicy` isolation. Use this when a change needs to be exercised across
real pods (the stateless MCP transport, Redis-backed sessions, HPA behavior,
multi-tenant isolation) — for everything else, the zero-setup
[local HTTP](DEPLOYMENT.md#http-multi-client-web-apps) or
[`make docker-smoke`](DEPLOYMENT.md#docker) paths are faster and need no cluster.

Everything under `deploy/k8s-local/` is generic — no personal hostnames,
tokens, or machine-specific paths. The one thing you generate yourself is the
JWKS/JWT keypair, so no private key is ever committed to the repo.

## Prerequisites

Any local single-node Kubernetes distribution with an Ingress controller works
— Rancher Desktop, `kind`, `minikube`, k3d. The steps below assume Rancher
Desktop's bundled Traefik; substitute `ingressClassName`/annotations in
`04-ingress.yaml` for a different controller.

## Setup

```bash
# 1. Build the image and load it into your cluster's local image store
docker build -t web-researcher-mcp:local .
# kind:      kind load docker-image web-researcher-mcp:local
# minikube:  minikube image load web-researcher-mcp:local
# Rancher Desktop / k3d with containerd: image is already visible to the
# cluster after `docker build` — no load step needed.

# 2. Apply the base manifests
kubectl apply -f deploy/k8s-local/00-namespace.yaml
kubectl apply -f deploy/k8s-local/02-redis.yaml

# 3. Generate a throwaway RSA keypair + JWKS, and create the ConfigMap the
#    mock OAuth issuer serves it from (scripts/e2e-oauth-mint-jwt.py is the
#    same tool scripts/e2e-oauth-docker.sh uses for its Docker-only e2e pass)
WORK_DIR="$(mktemp -d)"
python3 scripts/e2e-oauth-mint-jwt.py init "$WORK_DIR" wrm-k8s-key
kubectl create configmap oauth-issuer-jwks \
  --namespace web-researcher \
  --from-file=jwks.json="$WORK_DIR/jwks.json"
kubectl apply -f deploy/k8s-local/01-oauth-issuer.yaml

# 4. Create the app secret (may be empty — the server falls back to
#    DuckDuckGo with no provider keys configured)
kubectl create secret generic web-researcher-secrets \
  --namespace web-researcher \
  --from-literal=GOOGLE_CUSTOM_SEARCH_API_KEY= \
  --from-literal=GOOGLE_CUSTOM_SEARCH_ID=

kubectl apply -f deploy/k8s-local/03-app.yaml

# 5. TLS cert + hostname for the ingress (self-signed; adjust the hostname if
#    you changed it in 04-ingress.yaml)
openssl req -x509 -nodes -newkey rsa:2048 -days 7 \
  -keyout "$WORK_DIR/tls.key" -out "$WORK_DIR/tls.crt" \
  -subj "/CN=web-researcher-mcp.local" \
  -addext "subjectAltName=DNS:web-researcher-mcp.local"
kubectl create secret tls web-researcher-tls \
  --namespace web-researcher \
  --cert="$WORK_DIR/tls.crt" --key="$WORK_DIR/tls.key"
kubectl apply -f deploy/k8s-local/04-ingress.yaml

echo "127.0.0.1  web-researcher-mcp.local" | sudo tee -a /etc/hosts
```

## Minting a client JWT

```bash
python3 scripts/e2e-oauth-mint-jwt.py mint "$WORK_DIR" my-local-user wrm-k8s-key \
  http://oauth-issuer.web-researcher.svc.cluster.local web-researcher-mcp
```

The printed JWT is an RS256 token signed with the keypair from step 3,
matching what `internal/auth/middleware.go` validates against the in-cluster
JWKS endpoint — it is only valid inside this cluster, never a real IdP token.

## Connecting a client

Port-forward the ingress (or use your cluster's LoadBalancer/NodePort), then
point an MCP client's `http` transport entry at
`https://web-researcher-mcp.local:<port>/mcp/` with
`Authorization: Bearer <minted-jwt>`. `.mcp.json` in this repo intentionally
stays STDIO-only and portable (see [Development Setup](../CONTRIBUTING.md#development-setup))
— add a client entry for this cluster in your own untracked MCP client config,
not in the tracked `.mcp.json`.

## Teardown

```bash
kubectl delete namespace web-researcher
sudo sed -i '' '/web-researcher-mcp.local/d' /etc/hosts   # macOS; drop the '' arg on Linux
```
