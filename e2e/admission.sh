#!/usr/bin/env bash
# Admission E2E test: checks that a live cluster enforces the Kyverno policies
# in k8s/policies/, including the Cosign signature check. Offline
# `kyverno test` cannot cover that check because it never contacts the
# registry or Sigstore.
#
# All requests are server-side dry-runs, so nothing is deployed.
#
# Prerequisites: Kyverno installed, k8s/policies/ applied and Ready.
set -euo pipefail

NAMESPACE=admission-e2e
# Any image signed by .github/workflows/release.yml on a v* tag works. Pinned
# by digest so the test does not depend on a mutable tag.
SIGNED_IMAGE="${SIGNED_IMAGE:-ghcr.io/cooler-sai/go-k8s-secure-service:v1.0.3@sha256:3c413a3bdd375ba3a3fcc84d2cf87d6c58ac66f71dab1b510257784b17fe5ee2}"
cd "$(dirname "$0")/.."

workdir=$(mktemp -d)
cleanup() {
  # Always put the real signature policy back, even if a check failed.
  kubectl apply -f k8s/policies/verify-image-signature.yaml >/dev/null 2>&1 || true
  rm -rf "$workdir"
}
trap cleanup EXIT

wait_policy() {
  kubectl wait --for=jsonpath='{.status.conditionStatus.ready}'=true \
    imagevalidatingpolicy/verify-image-signature --timeout=60s >/dev/null
}

dry_run() { # dry_run <file>: prints the API server's answer, fails if denied
  kubectl -n "$NAMESPACE" apply --dry-run=server -f "$1" 2>&1
}

expect_admitted() { # expect_admitted <name> <file>
  local name=$1 file=$2 out
  if ! out=$(dry_run "$file"); then
    echo "FAIL $name: expected admission, got:" >&2
    echo "$out" >&2
    exit 1
  fi
  echo "ok   $name: admitted"
}

expect_denied() { # expect_denied <name> <file> <policy>...
  local name=$1 file=$2 out want
  shift 2
  # A just-applied policy can take a few seconds to reach the webhook.
  for _ in $(seq 1 10); do
    if ! out=$(dry_run "$file"); then break; fi
    out=""
    sleep 3
  done
  if [ -z "$out" ]; then
    echo "FAIL $name: expected denial, but it was admitted" >&2
    exit 1
  fi
  for want in "$@"; do
    if ! grep -q -- "$want" <<<"$out"; then
      echo "FAIL $name: denial does not mention $want:" >&2
      echo "$out" >&2
      exit 1
    fi
  done
  echo "ok   $name: denied by $*"
}

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# 1. The real Deployment with a signed release image passes every policy,
#    including signature verification against Fulcio and Rekor.
signed="$workdir/signed-deployment.yaml"
sed "s#image: go-k8s-secure-service:v1.0.0#image: ${SIGNED_IMAGE}#" k8s/deployment.yaml >"$signed"
grep -qF "image: ${SIGNED_IMAGE}" "$signed"
expect_admitted "signed release image" "$signed"

# 2. The same image is rejected when the policy expects a different signer, so
#    the signer identity is really checked, not just that a signature exists.
wrong_signer="$workdir/wrong-signer-policy.yaml"
sed '/subjectRegExp/s#cooler-SAI/#someone-else/#' k8s/policies/verify-image-signature.yaml >"$wrong_signer"
grep -q 'someone-else/' "$wrong_signer"
kubectl apply -f "$wrong_signer" >/dev/null
wait_policy
expect_denied "signed image, wrong expected signer" "$signed" verify-image-signature
kubectl apply -f k8s/policies/verify-image-signature.yaml >/dev/null
wait_policy

# 3. An insecure pod is rejected by each of the other policies.
insecure="$workdir/insecure-pod.yaml"
cat >"$insecure" <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: insecure
spec:
  containers:
    - name: app
      image: busybox:1.37
      securityContext:
        runAsUser: 0
        privileged: true
EOF
expect_denied "insecure pod" "$insecure" \
  disallow-root-user disallow-privileged-containers require-probes require-resource-limits

echo "Admission E2E test passed"
