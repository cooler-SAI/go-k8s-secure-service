#!/usr/bin/env bash
# Runtime-security E2E test: checks that the Tetragon policies in
# k8s/runtime-security/ kill forbidden behaviour in the server container and
# leave the real service alone.
#
#  0. The policies are applied, and a canary pod in each namespace must be
#     able to listen on the allowed ports (retried while Tetragon settles).
#  1. The policies are applied in the e2e namespace and the Deployment is
#     restarted under them: the pods must become Ready and stay up.
#  2. A harness binary (e2e/tetragon-harness) runs as /server in pods that
#     the same policies select and performs one action each. Forbidden
#     actions must end with SIGKILL (exit code 137); allowed ones must
#     survive.
#  3. `kubectl exec` into a selected container must fail.
#  4. Tetragon must have reported the kills.
#
# Prerequisites: smoke.sh has deployed the service into the e2e namespace,
# Tetragon is installed in kube-system, and the cluster is a kind cluster
# named $KIND_CLUSTER (the harness image is loaded into it).
set -euo pipefail

KIND_CLUSTER="${KIND_CLUSTER:-e2e}"
APP_NS=e2e
TEST_NS=runtime-e2e
IMAGE=tetragon-harness:e2e
POLICIES=(go-k8s-secure-service-exec go-k8s-secure-service-syscalls go-k8s-secure-service-network)
cd "$(dirname "$0")/.."

workdir=$(mktemp -d)
events_pids=()
dump_state() {
  echo "--- runtime-security test failed, cluster state ---"
  kubectl -n "$TEST_NS" get pods -o wide || true
  kubectl -n "$APP_NS" get pods -o wide || true
  # How each container ended: a SIGKILL from Tetragon and the kernel's OOM
  # killer both give exit code 137; the reason tells them apart.
  kubectl -n "$TEST_NS" get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.containerStatuses[0].state}{"\n"}{end}' || true
  kubectl -n kube-system exec ds/tetragon -c tetragon -- tetra tracingpolicy list || true
  if [ -s "$workdir/events.log" ]; then
    echo "--- Tetragon events (kprobes and exits) ---"
    cat "$workdir/events.log"
  fi
  # The compact format hides the kprobe arguments; the JSON copy shows what
  # Tetragon read, e.g. the local port of a socket that was killed on listen.
  if [ -s "$workdir/kprobes.json" ]; then
    echo "--- Tetragon kprobe events with arguments ---"
    jq -rR 'fromjson? | select(.process_kprobe) | .time as $t | .process_kprobe |
      [$t, "\(.process.pod.namespace // "-")/\(.process.pod.name // "-")",
       .process.binary, .function_name, .policy_name, (.action // "-"),
       (.args | tojson)] | @tsv' "$workdir/kprobes.json" 2>/dev/null ||
      cat "$workdir/kprobes.json"
  fi
  kubectl -n kube-system logs ds/tetragon -c tetragon --tail=50 || true
}
# On EXIT, not ERR: the checks below fail with an explicit `exit 1`, which
# does not trigger an ERR trap.
on_exit() {
  local rc=$?
  [ "${#events_pids[@]}" -gt 0 ] && kill "${events_pids[@]}" 2>/dev/null || true
  [ "$rc" -ne 0 ] && dump_state
  rm -rf "$workdir"
}
trap on_exit EXIT

tetra() { kubectl -n kube-system exec ds/tetragon -c tetragon -- tetra "$@"; }

kubectl -n kube-system rollout status ds/tetragon --timeout=180s

# --- Harness image: the test binary as /server (and a copy as /other). ---
CGO_ENABLED=0 GOOS=linux go build -trimpath -o "$workdir/server" ./e2e/tetragon-harness
cp "$workdir/server" "$workdir/other"
cat >"$workdir/Dockerfile" <<'EOF'
FROM scratch
COPY server /server
COPY other /other
USER 65532:65532
ENTRYPOINT ["/server"]
EOF
docker build -q -t "$IMAGE" "$workdir" >/dev/null
kind load docker-image "$IMAGE" --name "$KIND_CLUSTER"

# --- Policies ---
# Start from a clean test namespace so a rerun never sees old harness pods.
kubectl delete namespace "$TEST_NS" --ignore-not-found --wait --timeout=120s
kubectl create namespace "$TEST_NS"
for ns in "$APP_NS" "$TEST_NS"; do
  kubectl -n "$ns" apply -f k8s/runtime-security/
done
# Without kprobe_multi (kernels before 5.18) every listed system call is a
# separate kprobe, so the syscalls policy can take a minute or two to load.
for _ in $(seq 1 90); do
  list=$(tetra tracingpolicy list 2>/dev/null || true)
  ready=0
  for p in "${POLICIES[@]}"; do
    for ns in "$APP_NS" "$TEST_NS"; do
      if grep -E "\b${p}\b" <<<"$list" | grep -w "$ns" | grep -qw enabled; then
        ready=$((ready + 1))
      fi
    done
  done
  if [ "$ready" -eq $((${#POLICIES[@]} * 2)) ]; then break; fi
  sleep 2
done
echo "$list"
if [ "$ready" -ne $((${#POLICIES[@]} * 2)) ]; then
  echo "FAIL not all tracing policies are enabled" >&2
  exit 1
fi
echo "ok   ${#POLICIES[@]} tracing policies enabled in $APP_NS and $TEST_NS"

# Record every policy match and process exit for the failure report. Not
# filtered by namespace: a process killed before Tetragon knows its pod
# (runc's init) carries no namespace.
kubectl -n kube-system exec ds/tetragon -c tetragon -- tetra getevents -o compact \
  --timestamps --color never -e PROCESS_KPROBE -e PROCESS_EXIT >"$workdir/events.log" 2>&1 &
events_pids+=($!)
kubectl -n kube-system exec ds/tetragon -c tetragon -- tetra getevents -o json \
  -e PROCESS_KPROBE >"$workdir/kprobes.json" 2>&1 &
events_pids+=($!)

# --- Harness pods: the test binary as /server in a pod the policies select. ---
run_harness() { # run_harness <namespace> <name> <args...>: starts a selected pod
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
      image: ${IMAGE}
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
      livenessProbe:
        tcpSocket:
          port: 8080
      readinessProbe:
        tcpSocket:
          port: 8080
EOF
}

outcome() { # outcome <namespace> <name>: prints "survived", "exit:<code>" or "timeout"
  local ns=$1 name=$2 code
  for _ in $(seq 1 90); do
    code=$(kubectl -n "$ns" get pod "$name" \
      -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}' 2>/dev/null || true)
    if [ -n "$code" ]; then echo "exit:$code"; return; fi
    if kubectl -n "$ns" logs "$name" 2>/dev/null | grep -qx survived; then echo survived; return; fi
    sleep 1
  done
  echo timeout
}

# --- 0. Wait until the policies have settled. ---
# For a short time after a namespaced policy shows as "enabled", Tetragon can
# kill a listen on an allowed port. In CI the real server was SIGKILLed on its
# first listen about 0.7s after the policies were enabled, and the restarted
# container ran fine. Production pods that start right after a policy is
# applied can hit this too (they recover with one restart).
# A canary listens on both allowed ports (:8080, then :9090) in each
# namespace and is retried until it survives, so the checks below test the
# policies in their settled state. Killed canaries are counted by Tetragon,
# so step 4 compares its counters with the values taken after this step.
wait_policies_settled() { # wait_policies_settled <namespace>
  local ns=$1 attempt got
  for attempt in $(seq 1 10); do
    # In $APP_NS the canary briefly joins the Service's endpoints; nothing
    # sends traffic to the Service at this point.
    run_harness "$ns" policy-canary listen 9090
    got=$(outcome "$ns" policy-canary)
    kubectl -n "$ns" delete pod policy-canary --wait=true >/dev/null
    case "$got" in
      survived)
        echo "ok   policies settled in $ns (canary listened on :8080 and :9090, attempt $attempt)"
        return
        ;;
      exit:137)
        echo "note policy canary in $ns was killed on attempt $attempt, retrying" >&2
        sleep 3
        ;;
      *)
        echo "FAIL policy canary in $ns: $got" >&2
        exit 1
        ;;
    esac
  done
  echo "FAIL policies in $ns still kill a listen on an allowed port after 10 attempts" >&2
  exit 1
}
for ns in "$APP_NS" "$TEST_NS"; do
  wait_policies_settled "$ns"
done
settled_list=$(tetra tracingpolicy list)

# --- 1. The real service keeps working under the policies. ---
kubectl -n "$APP_NS" rollout restart deployment/go-k8s-secure-service
kubectl -n "$APP_NS" rollout status deployment/go-k8s-secure-service --timeout=120s
# Let the probes run a few times under enforcement.
sleep 20
restarts=$(kubectl -n "$APP_NS" get pods -l app=go-k8s-secure-service \
  -o jsonpath='{range .items[*]}{.status.containerStatuses[0].restartCount}{"\n"}{end}' | sort -u)
not_ready=$(kubectl -n "$APP_NS" get pods -l app=go-k8s-secure-service \
  -o jsonpath='{range .items[*]}{.status.containerStatuses[0].ready}{"\n"}{end}' | grep -vx true || true)
if [ "$restarts" != "0" ] || [ -n "$not_ready" ]; then
  echo "FAIL service pods restarted or are not ready under the policies" >&2
  kubectl -n "$APP_NS" get pods -o wide >&2
  exit 1
fi
echo "ok   service runs under the policies (Ready, no restarts)"

# --- 2. Harness pods, one forbidden or allowed action each. ---
expect() { # expect <name> killed|survived <description>
  local name=$1 want=$2 desc=$3 got
  got=$(outcome "$TEST_NS" "$name")
  case "$want:$got" in
    killed:exit:137 | survived:survived) echo "ok   $desc: $want" ;;
    *)
      echo "FAIL $desc: want $want, got $got" >&2
      kubectl -n "$TEST_NS" logs "$name" >&2 || true
      exit 1
      ;;
  esac
}

api_ip=$(kubectl -n default get svc kubernetes -o jsonpath='{.spec.clusterIP}')

run_harness "$TEST_NS" idle idle
run_harness "$TEST_NS" exec-other exec /other
run_harness "$TEST_NS" listen-other listen 7777
run_harness "$TEST_NS" connect-api connect "${api_ip}:443"
run_harness "$TEST_NS" connect-otlp connect 127.0.0.1:4318
run_harness "$TEST_NS" ptrace ptrace

expect idle survived "baseline (listen :8080, no action)"
expect exec-other killed "execve of /other"
expect listen-other killed "listen on :7777"
expect connect-api killed "outbound TCP to ${api_ip}:443"
expect connect-otlp survived "outbound TCP to :4318 (OTLP)"
expect ptrace killed "ptrace system call"

# --- 3. kubectl exec into a selected container. ---
if out=$(kubectl -n "$TEST_NS" exec idle -c server -- /other idle 2>&1); then
  echo "FAIL kubectl exec was not blocked: $out" >&2
  exit 1
fi
echo "ok   kubectl exec blocked: $(tail -n1 <<<"$out")"
if [ "$(outcome "$TEST_NS" idle)" != survived ] ||
  [ "$(kubectl -n "$TEST_NS" get pod idle -o jsonpath='{.status.containerStatuses[0].restartCount}')" != 0 ]; then
  echo "FAIL the blocked exec affected the running server" >&2
  exit 1
fi
echo "ok   server process unaffected by the blocked exec"

# --- 4. Tetragon's own counters. ---
# `tetra tracingpolicy list` rows: ID NAME DOMAIN STATE FILTERID NAMESPACE
# SENSORS KERNELMEMORY MODE NPOST NENFORCE NMONITOR. KERNELMEMORY contains a
# space ("1.94 MB"), so the counters are read from the end of the row.
# Every policy must have enforced in the test namespace, and none may have
# fired for the real service. Both are counted from the values taken after
# step 0, so killed policy canaries do not count.
list=$(tetra tracingpolicy list)
echo "$list"
counter() { # counter <list> <policy> <namespace> NPOST|NENFORCE
  awk -v p="$2" -v ns="$3" -v col="$4" '
    NR > 1 && $2 == p && $6 == ns { print (col == "NPOST" ? $(NF-2) : $(NF-1)) }' <<<"$1"
}
for p in "${POLICIES[@]}"; do
  enforced=$(counter "$list" "$p" "$TEST_NS" NENFORCE)
  enforced_before=$(counter "$settled_list" "$p" "$TEST_NS" NENFORCE)
  if [ -z "$enforced" ] || [ "$((enforced - ${enforced_before:-0}))" -lt 1 ]; then
    echo "FAIL Tetragon counted no enforcement for $p in $TEST_NS" >&2
    exit 1
  fi
  posted=$(counter "$list" "$p" "$APP_NS" NPOST)
  posted_before=$(counter "$settled_list" "$p" "$APP_NS" NPOST)
  if [ -z "$posted" ] || [ "$((posted - ${posted_before:-0}))" -ne 0 ]; then
    echo "FAIL $p fired $((${posted:-0} - ${posted_before:-0})) time(s) for the real service in $APP_NS" >&2
    exit 1
  fi
done
echo "ok   every policy enforced in $TEST_NS, none fired for the service in $APP_NS"

kubectl delete namespace "$TEST_NS" --wait=false >/dev/null
echo "Runtime-security E2E test passed"
