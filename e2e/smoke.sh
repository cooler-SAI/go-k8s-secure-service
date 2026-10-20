#!/usr/bin/env bash
# E2E smoke test: deploys the manifests into a namespace that enforces the
# Pod Security "restricted" profile, waits for the pods to become Ready and
# calls the endpoints through kubectl port-forward.
#
# Prerequisites: a running cluster (e.g. kind) that already has the image
# go-k8s-secure-service:v1.0.0 loaded (kind load docker-image ...).
set -euo pipefail

NAMESPACE=e2e
LOCAL_PORT="${LOCAL_PORT:-18080}"
cd "$(dirname "$0")/.."

pf_pid=""
cleanup() {
  [ -n "$pf_pid" ] && kill "$pf_pid" 2>/dev/null || true
}
dump_state() {
  echo "--- smoke test failed, cluster state ---"
  kubectl -n "$NAMESPACE" get pods,svc,events -o wide || true
  kubectl -n "$NAMESPACE" describe pods || true
  kubectl -n "$NAMESPACE" logs -l app=go-k8s-secure-service --tail=50 || true
}
trap cleanup EXIT
trap 'dump_state' ERR

kubectl apply -f e2e/namespace.yaml

# A server-side dry-run against the restricted namespace fails loudly if the
# pod template violates the profile (the live apply below would only warn).
kubectl -n "$NAMESPACE" apply --dry-run=server -f k8s/deployment.yaml 2>&1 | tee /tmp/psa-dryrun.txt
if grep -qi "would violate" /tmp/psa-dryrun.txt; then
  echo "Deployment violates the restricted Pod Security profile" >&2
  exit 1
fi

kubectl -n "$NAMESPACE" apply -f k8s/deployment.yaml -f k8s/service.yaml -f k8s/pdb.yaml -f k8s/networkpolicy.yaml
kubectl -n "$NAMESPACE" rollout status deployment/go-k8s-secure-service --timeout=120s
kubectl -n "$NAMESPACE" wait --for=condition=Ready pod -l app=go-k8s-secure-service --timeout=60s

replicas=$(kubectl -n "$NAMESPACE" get pods -l app=go-k8s-secure-service --field-selector=status.phase=Running -o name | wc -l)
if [ "$replicas" -ne 2 ]; then
  echo "expected 2 running pods, got $replicas" >&2
  exit 1
fi

kubectl -n "$NAMESPACE" port-forward svc/go-k8s-secure-service "${LOCAL_PORT}:80" >/dev/null 2>&1 &
pf_pid=$!

for _ in $(seq 1 30); do
  if curl -fs -o /dev/null "http://127.0.0.1:${LOCAL_PORT}/livez"; then break; fi
  sleep 1
done

check() { # check <method> <path> <expected status> [expected body]
  local method=$1 path=$2 want=$3 body=${4:-} out code
  out=$(mktemp)
  code=$(curl -s -o "$out" -w '%{http_code}' -X "$method" "http://127.0.0.1:${LOCAL_PORT}${path}")
  if [ "$code" != "$want" ]; then
    echo "FAIL $method $path: status $code, want $want" >&2
    exit 1
  fi
  if [ -n "$body" ] && ! grep -qx "$body" "$out"; then
    echo "FAIL $method $path: body $(cat "$out"), want $body" >&2
    exit 1
  fi
  echo "ok   $method $path -> $code"
}

check GET  /        200 "Hello, World!"
check GET  /livez   200 "OK"
check GET  /readyz  200 "OK"
check GET  /missing 404
check POST /        405

echo "E2E smoke test passed"
