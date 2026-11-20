#!/usr/bin/env bash
# Chaos test on a local kind cluster: the service under constant load while
# its pods are replaced and attacked, with live RED metrics and Tetragon's
# kill events as evidence.
#
#   1. Setup (idempotent): kind cluster, image, Tetragon, the manifests from
#      k8s/ and the policies from k8s/runtime-security/ in namespace "chaos".
#      A canary pod in each namespace must listen on :8080 and :9090
#      without being killed (retried while Tetragon settles) before the
#      Deployment is restarted under the policies.
#   2. Load: k6 (chaos/k6-load.js) runs as a Job next to the service and
#      sends RATE requests/s through the Service for DURATION. Any non-200,
#      5xx or transport error fails the run.
#   3. Live RED metrics: chaos/red-watch.sh scrapes every pod's :9090,
#      including Terminating ones, and prints them as the test runs.
#   4. Rollouts: ROLLOUTS x `kubectl rollout restart` under load. Every old
#      pod gets SIGTERM, reports not-ready, drains for 5 s and exits.
#   5. Attack on the live pods: `kubectl exec` into each serving container.
#      Tetragon must kill it while the server keeps serving.
#   6. Attack from inside a pod (RCE simulation): the distroless service has
#      no shell to abuse, so pods running e2e/tetragon-harness as /server,
#      with the service's labels and policies, perform what an attacker with
#      code execution would do next: reverse shell, bind shell, a dropped
#      binary, ptrace. Each must end with SIGKILL (exit code 137).
#   7. Verdict: k6 thresholds, 5xx seen by the pods, restarts, and
#      Tetragon's kills per policy and namespace, written to $RESULTS_DIR.
#
# Requires: docker, kind, kubectl, helm, go. Runs on Linux and Git Bash.
# Env: KIND_CLUSTER (chaos), RATE (200), DURATION (150s), ROLLOUTS (2),
#      RESULTS_DIR (chaos/results/<timestamp>), SKIP_BUILD (unset).
set -euo pipefail
export MSYS_NO_PATHCONV=1 # Git Bash: don't rewrite /server, /api/... into Windows paths

KIND_CLUSTER="${KIND_CLUSTER:-chaos}"
NS=chaos
ATTACK_NS=chaos-attack
RATE="${RATE:-200}"
DURATION="${DURATION:-150s}"
ROLLOUTS="${ROLLOUTS:-2}"
IMAGE=go-k8s-secure-service:v1.0.0
HARNESS_IMAGE=tetragon-harness:e2e
K6_BASE_IMAGE=grafana/k6:1.3.0
K6_IMAGE=chaos-k6:1.3.0
TETRAGON_CHART_VERSION=1.8.0
POLICIES=(go-k8s-secure-service-exec go-k8s-secure-service-syscalls go-k8s-secure-service-network)
cd "$(dirname "$0")/.."
RESULTS_DIR="${RESULTS_DIR:-chaos/results/$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$RESULTS_DIR"

log() { echo "==> $(date +%T) $*" | tee -a "$RESULTS_DIR/timeline.log"; }
fail() { echo "FAIL $*" | tee -a "$RESULTS_DIR/timeline.log" >&2; failures=$((failures + 1)); }
failures=0
# Relative to the repository, not /tmp: with MSYS_NO_PATHCONV, native Windows
# tools (go.exe, docker.exe) would not understand an MSYS /tmp path.
workdir=$(mktemp -d "$RESULTS_DIR/.work.XXXXXX")
watch_pid="" events_pid=""
cleanup() {
  touch "$workdir/stop"
  [ -n "$watch_pid" ] && wait "$watch_pid" 2>/dev/null || true
  [ -n "$events_pid" ] && kill "$events_pid" 2>/dev/null || true
  rm -rf "$workdir"
}
trap cleanup EXIT

tetra() { kubectl -n kube-system exec ds/tetragon -c tetragon -- tetra "$@"; }

# --- 1. Setup ---------------------------------------------------------------
if ! kind get clusters 2>/dev/null | grep -qx "$KIND_CLUSTER"; then
  log "creating kind cluster $KIND_CLUSTER"
  kind create cluster --name "$KIND_CLUSTER" --config e2e/kind-config.yaml
fi
kubectl config use-context "kind-$KIND_CLUSTER" >/dev/null

if [ -z "${SKIP_BUILD:-}" ]; then
  log "building $IMAGE and $HARNESS_IMAGE"
  docker build -q -t "$IMAGE" . >/dev/null
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$workdir/server" ./e2e/tetragon-harness
  cp "$workdir/server" "$workdir/other"
  printf 'FROM scratch\nCOPY server /server\nCOPY other /other\nUSER 65532:65532\nENTRYPOINT ["/server"]\n' >"$workdir/Dockerfile"
  docker build -q -t "$HARNESS_IMAGE" "$workdir" >/dev/null
  # A local single-platform copy of the k6 image: `kind load` fails on
  # multi-platform images, and the node then depends on Docker Hub.
  echo "FROM $K6_BASE_IMAGE" | docker build -q -t "$K6_IMAGE" - >/dev/null
fi
kind load docker-image "$IMAGE" "$HARNESS_IMAGE" "$K6_IMAGE" --name "$KIND_CLUSTER" >/dev/null

if ! helm -n kube-system status tetragon >/dev/null 2>&1; then
  log "installing Tetragon $TETRAGON_CHART_VERSION"
  helm repo add cilium https://helm.cilium.io >/dev/null 2>&1 || true
  helm repo update cilium >/dev/null
  helm install tetragon cilium/tetragon --version "$TETRAGON_CHART_VERSION" \
    --namespace kube-system --set tetragon.hostProcPath=/procHost --wait --timeout 5m
fi
kubectl -n kube-system rollout status ds/tetragon --timeout=180s >/dev/null

log "deploying the service and the Tetragon policies"
for ns in "$NS" "$ATTACK_NS"; do
  sed "s/name: e2e/name: $ns/" e2e/namespace.yaml | kubectl apply -f - >/dev/null
  kubectl -n "$ns" apply -f k8s/runtime-security/ >/dev/null
done
kubectl -n "$NS" apply -f k8s/deployment.yaml -f k8s/service.yaml -f k8s/pdb.yaml \
  -f k8s/networkpolicy.yaml >/dev/null
kubectl -n "$ATTACK_NS" delete pods --all --wait=false >/dev/null
kubectl -n "$NS" delete job k6 --ignore-not-found --wait >/dev/null
# Without kprobe_multi (kernels before 5.18) the syscalls policy can take a
# minute or two to load.
for _ in $(seq 1 90); do
  list=$(tetra tracingpolicy list 2>/dev/null || true)
  ready=$(awk -v a="$NS" -v b="$ATTACK_NS" '$4 == "enabled" && ($6 == a || $6 == b)' <<<"$list" | wc -l)
  [ "$ready" -eq $((${#POLICIES[@]} * 2)) ] && break
  sleep 2
done
[ "$ready" -eq $((${#POLICIES[@]} * 2)) ] || { echo "$list"; fail "tracing policies not enabled"; exit 1; }

# Pods running e2e/tetragon-harness as /server with the service's
# labels, so the policies select them.
harness_pod() { # harness_pod <namespace> <name> <harness args...>: a pod the policies select
  local ns=$1 name=$2 args
  shift 2
  args=$(printf '"%s",' "$@")
  kubectl -n "$ns" apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${name}
  labels:
    app: go-k8s-secure-service
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    runAsGroup: 65532
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: server
      image: ${HARNESS_IMAGE}
      imagePullPolicy: Never
      args: [${args%,}]
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities:
          drop: [ALL]
      resources:
        limits:
          cpu: 100m
          memory: 32Mi
EOF
}

# For a short time after a namespaced policy shows as "enabled", Tetragon can
# kill a listen on an allowed port (seen in CI: the server killed on its first
# listen on :8080, ~0.7 s after the policies were enabled). Before the
# Deployment is restarted, a canary in each namespace listens on :8080 and
# :9090 and is retried until it survives. This happens before
# policies-before.txt is taken, so killed canaries don't count in the verdict.
for ns in "$NS" "$ATTACK_NS"; do
  settled=""
  for attempt in $(seq 1 10); do
    # In $NS the canary briefly joins the Service's endpoints; k6 is not
    # running yet.
    harness_pod "$ns" policy-canary listen 9090
    got=""
    for _ in $(seq 1 90); do
      code=$(kubectl -n "$ns" get pod policy-canary \
        -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}' 2>/dev/null || true)
      if [ -n "$code" ]; then got="exit:$code"; break; fi
      if kubectl -n "$ns" logs policy-canary 2>/dev/null | grep -qx survived; then got=survived; break; fi
      sleep 1
    done
    kubectl -n "$ns" delete pod policy-canary --wait=true >/dev/null
    case "$got" in
      survived) settled=$attempt; break ;;
      exit:137) log "policy canary in $ns killed on attempt $attempt, retrying"; sleep 3 ;;
      *) fail "policy canary in $ns: ${got:-timeout}"; exit 1 ;;
    esac
  done
  [ -n "$settled" ] || { fail "policies in $ns still kill a listen on an allowed port"; exit 1; }
  log "policies settled in $ns (canary listened on :8080 and :9090, attempt $settled)"
done

# Start from fresh pods (restart counters at 0) under the policies.
kubectl -n "$NS" rollout restart deployment/go-k8s-secure-service >/dev/null
kubectl -n "$NS" rollout status deployment/go-k8s-secure-service --timeout=120s >/dev/null
tetra tracingpolicy list >"$RESULTS_DIR/policies-before.txt"
log "setup done: 2 replicas Ready, ${#POLICIES[@]} policies enforcing in $NS and $ATTACK_NS"

# --- Evidence collectors ----------------------------------------------------
# Kprobe events have no namespace when the killed process is runc's init
# (kubectl exec), so the stream is not filtered by namespace. HTTP probes
# start no processes, so a kind node produces few other exits.
kubectl -n kube-system exec ds/tetragon -c tetragon -- \
  tetra getevents -o json -e PROCESS_KPROBE -e PROCESS_EXIT >"$RESULTS_DIR/tetragon-events.json" 2>/dev/null &
events_pid=$!

RED_WATCH_STOP="$workdir/stop" ./chaos/red-watch.sh "$NS" 2 | tee "$RESULTS_DIR/red.log" &
watch_pid=$!

# --- 2. Load ----------------------------------------------------------------
log "starting k6: $RATE req/s for $DURATION"
kubectl -n "$NS" create configmap k6-script --from-file=chaos/k6-load.js \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$NS" apply -f - >/dev/null <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: k6
spec:
  backoffLimit: 0
  template:
    metadata:
      labels:
        app: k6
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 12345
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: k6
          image: ${K6_IMAGE}
          imagePullPolicy: Never
          args: [run, --quiet, --no-color, /scripts/k6-load.js]
          env:
            - { name: TARGET, value: "http://go-k8s-secure-service/" }
            - { name: RATE, value: "${RATE}" }
            - { name: DURATION, value: "${DURATION}" }
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
          resources:
            limits:
              cpu: "1"
              memory: 256Mi
          volumeMounts:
            - { name: script, mountPath: /scripts }
            - { name: tmp, mountPath: /tmp }
      volumes:
        - name: script
          configMap:
            name: k6-script
        - name: tmp
          emptyDir: {}
EOF
kubectl -n "$NS" wait --for=condition=Ready pod -l app=k6 --timeout=180s >/dev/null
log "k6 running, warming up for 20 s"
sleep 20

# --- 4. Rollouts under load -------------------------------------------------
for i in $(seq 1 "$ROLLOUTS"); do
  log "rollout $i/$ROLLOUTS: kubectl rollout restart"
  kubectl -n "$NS" rollout restart deployment/go-k8s-secure-service >/dev/null
  kubectl -n "$NS" rollout status deployment/go-k8s-secure-service --timeout=180s >/dev/null
  log "rollout $i/$ROLLOUTS: new pods Ready, old pods draining"
  sleep 20
done

# --- 5. kubectl exec into the live, serving pods ----------------------------
pods=$(kubectl -n "$NS" get pods -l app=go-k8s-secure-service \
  --field-selector=status.phase=Running -o jsonpath='{.items[*].metadata.name}')
for pod in $pods; do
  log "attack: kubectl exec -it $pod -- /bin/sh"
  if out=$(kubectl -n "$NS" exec "$pod" -c server -- /bin/sh -c id 2>&1); then
    fail "kubectl exec into $pod was not blocked: $out"
  else
    log "blocked: $(tail -n1 <<<"$out" | sed 's/.*OCI runtime/OCI runtime/')"
  fi
done

# --- 6. RCE simulation: post-exploitation actions inside a pod --------------
api_ip=$(kubectl -n default get svc kubernetes -o jsonpath='{.spec.clusterIP}')
log "attack: RCE simulation in $ATTACK_NS (reverse shell, bind shell, dropped binary, ptrace)"
attack() { harness_pod "$ATTACK_NS" "$@"; }
attack reverse-shell connect "${api_ip}:443"
attack bind-shell listen 4444
attack dropped-binary exec /other
attack ptrace ptrace
for name in reverse-shell bind-shell dropped-binary ptrace; do
  code=""
  for _ in $(seq 1 90); do
    code=$(kubectl -n "$ATTACK_NS" get pod "$name" \
      -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}' 2>/dev/null || true)
    [ -n "$code" ] && break
    sleep 1
  done
  # The harness prints "<action> returned: ..." once the call comes back. A
  # process that Tetragon killed inside the kernel function never gets there.
  out=$(kubectl -n "$ATTACK_NS" logs "$name" 2>/dev/null || true)
  if [ "$code" = 137 ] && grep -q '^running action' <<<"$out" && ! grep -q 'returned' <<<"$out"; then
    log "killed: $name (exit code 137, SIGKILL; the call never returned to user space)"
  else
    fail "$name: want exit code 137, got '${code:-still running}'"
    kubectl -n "$ATTACK_NS" logs "$name" >&2 || true
  fi
done

# --- Wait for k6 ------------------------------------------------------------
log "waiting for k6 to finish"
for _ in $(seq 1 300); do
  [ -n "$(kubectl -n "$NS" get job k6 -o jsonpath='{.status.succeeded}{.status.failed}')" ] && break
  sleep 2
done
k6_pod=$(kubectl -n "$NS" get pods -l app=k6 -o jsonpath='{.items[0].metadata.name}')
kubectl -n "$NS" logs "$k6_pod" >"$RESULTS_DIR/k6.log" 2>&1
k6_exit=$(kubectl -n "$NS" get pod "$k6_pod" -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}')

touch "$workdir/stop"
wait "$watch_pid" 2>/dev/null || true
watch_pid=""
sleep 2
kill "$events_pid" 2>/dev/null || true
events_pid=""
tetra tracingpolicy list >"$RESULTS_DIR/policies-after.txt"

# --- 7. Verdict -------------------------------------------------------------
# Compact, human-readable view of the enforcement events: every kprobe event
# plus the SIGKILL exits.
grep -E '^\{"process_kprobe"|"signal":"SIGKILL"' "$RESULTS_DIR/tetragon-events.json" |
  kubectl -n kube-system exec -i ds/tetragon -c tetragon -- \
    tetra getevents -o compact --timestamps --color never >"$RESULTS_DIR/tetragon-compact.log" || true

# One row per kprobe event (each is a policy match with action SIGKILL).
# Tetragon fills in the pod only for processes in its process cache. runc's
# init (kubectl exec) is never in it, and on Docker Desktop (WSL2) no
# container process is, because the kernel's PIDs and the node's /proc live
# in different PID namespaces; those rows show "-" and are attributed by
# the per-namespace counters below.
awk '
  function field(s, re,    m) { if (match(s, re)) { m = substr(s, RSTART, RLENGTH); sub(/^[^:]*:"?/, "", m); sub(/"$/, "", m); return m } return "" }
  function podname(s,   m) { if (match(s, /"pod":\{"namespace":"[^"]+","name":"[^"]+"/)) { m = substr(s, RSTART + 20, RLENGTH - 21); sub(/","name":"/, "/", m); return m } return "-" }
  function last_time(s,   t) { t = ""; while (match(s, /,"time":"[^"]+"/)) { t = substr(s, RSTART + 9, RLENGTH - 10); s = substr(s, RSTART + RLENGTH) } return t }
  BEGIN { printf "%-30s  %-7s  %-24s  %-30s  %s\n", "TIME (UTC)", "PID", "POD", "POLICY", "HOOK" }
  /^\{"process_kprobe"/ {
    printf "%-30s  %-7s  %-24s  %-30s  %s\n", last_time($0), field($0, "\"pid\":[0-9]+"), podname($0),
      field($0, "\"policy_name\":\"[^\"]+\""), field($0, "\"function_name\":\"[^\"]+\"")
  }' "$RESULTS_DIR/tetragon-events.json" >"$RESULTS_DIR/kills.txt"

# Per policy and namespace: events (NPOST) and kills (NENFORCE) during the
# test, from `tetra tracingpolicy list` before and after. KERNELMEMORY holds
# a space ("1.94 MB"), so the counters are read from the end of the row.
awk '
  NR == FNR { if (FNR > 1) { post[$2 " " $6] = $(NF-2); enf[$2 " " $6] = $(NF-1) } next }
  FNR == 1 { printf "%-32s  %-13s  %6s  %8s\n", "POLICY", "NAMESPACE", "EVENTS", "SIGKILLS" }
  FNR > 1 { printf "%-32s  %-13s  %6d  %8d\n", $2, $6, $(NF-2) - post[$2 " " $6], $(NF-1) - enf[$2 " " $6] }
' "$RESULTS_DIR/policies-before.txt" "$RESULTS_DIR/policies-after.txt" | sort -k2,2 -k1,1 >"$RESULTS_DIR/counters.txt"
kills() { awk -v p="$1" -v ns="$2" '$1 == p && $2 == ns { print $4 }' "$RESULTS_DIR/counters.txt"; }

k6_result=$(grep -o 'K6_RESULT .*' "$RESULTS_DIR/k6.log" | cut -d' ' -f2- || true)
k6_failed_lines=$(grep -c 'level=error' "$RESULTS_DIR/k6.log" || true)
restarts=$(kubectl -n "$NS" get pods -l app=go-k8s-secure-service \
  -o jsonpath='{range .items[*]}{.status.containerStatuses[0].restartCount}{"\n"}{end}' | sort -u | tr '\n' ' ')
# "new" rows hold a pod's totals at its first scrape, the others deltas, so
# the sum covers every request the pods served while being watched.
red_5xx=$(awk '$6 ~ /^[0-9]+$/ { s += $6 } END { print s + 0 }' "$RESULTS_DIR/red.log")
exec_attempts=$(wc -w <<<"$pods")

[ "$k6_exit" = 0 ] || fail "k6 exited with $k6_exit (thresholds crossed)"
[ "$k6_failed_lines" = 0 ] || fail "k6 logged $k6_failed_lines failed requests"
[ "$red_5xx" = 0 ] || fail "pods reported $red_5xx 5xx responses"
[ "$restarts" = "0 " ] || fail "service containers restarted: $restarts"
# In the service's namespace only kubectl exec may trip a policy (runc's init
# calls keyctl); the server itself must never.
[ "$(kills go-k8s-secure-service-syscalls "$NS")" -ge "$exec_attempts" ] ||
  fail "fewer Tetragon kills in $NS than kubectl exec attempts ($exec_attempts)"
for p in go-k8s-secure-service-exec go-k8s-secure-service-network; do
  [ "$(kills "$p" "$NS")" = 0 ] || fail "$p killed a process in $NS"
done
for p in "${POLICIES[@]}"; do
  [ "$(kills "$p" "$ATTACK_NS")" -ge 1 ] || fail "$p killed nothing in $ATTACK_NS"
done

{
  echo "# Chaos test $(date -u +%FT%TZ)"
  echo
  echo "Load: $RATE req/s for $DURATION, $ROLLOUTS rollout(s), kubectl exec into each pod, 4 RCE actions."
  echo
  echo '## k6'
  echo '```'
  grep -v '^K6_RESULT' "$RESULTS_DIR/k6.log" | grep -v '^time=' || true
  echo '```'
  echo
  echo "Service restarts during the attack: $restarts; 5xx counted by the pods: $red_5xx"
  echo
  echo '## Tetragon: kills per policy and namespace'
  echo '```'
  cat "$RESULTS_DIR/counters.txt"
  echo '```'
  echo
  echo '## Tetragon: events (tetra getevents)'
  echo '```'
  cat "$RESULTS_DIR/kills.txt"
  echo '```'
  echo
  echo "Verdict: $([ "$failures" = 0 ] && echo PASS || echo "FAIL ($failures)")"
} >"$RESULTS_DIR/summary.md"

echo
cat "$RESULTS_DIR/summary.md"
echo
echo "Evidence in $RESULTS_DIR: timeline.log red.log k6.log tetragon-events.json tetragon-compact.log kills.txt counters.txt"
[ "$failures" = 0 ]
