#!/usr/bin/env bash
# Live RED metrics (Rate, Errors, Duration) per pod, scraped from each pod's
# :9090/metrics through the API server's pod proxy. Unlike kubectl
# port-forward, which is bound to one pod and breaks when that pod goes away,
# this follows the pods as a rollout replaces them, and keeps scraping a
# Terminating pod while it drains (the metrics server stops last).
#
# One line per pod and interval, for the traffic route "/" only (probes are
# excluded):
#
#   TIME      POD    STATE        RPS   2XX  5XX  IN-FLIGHT  P99
#   12:00:01  x7k2p  Terminating  98.5  197    0          1  <=5ms
#
# STATE is Ready, NotReady or Terminating. RPS, 2XX and 5XX are deltas since
# the previous scrape of that pod; P99 is the upper bound of the histogram
# bucket holding the 99th percentile in that interval. "gone" means the pod
# no longer answers, i.e. its process has exited.
#
# Usage: red-watch.sh [namespace] [interval-seconds]
# Runs until killed or until the file $RED_WATCH_STOP exists.
set -uo pipefail
export MSYS_NO_PATHCONV=1 # Git Bash: don't rewrite /api/... into a Windows path

NS="${1:-chaos}"
INTERVAL="${2:-2}"
STOP_FILE="${RED_WATCH_STOP:-}"
SELECTOR=app=go-k8s-secure-service

declare -A prev_total prev_2xx prev_5xx prev_buckets prev_time

# Prints "total 2xx 5xx inflight buckets" for route "/" from metrics on stdin;
# buckets is a comma-separated list of le=count pairs (cumulative).
parse() {
  awk '
    /^http_requests_total\{/ && /route="\/"/ {
      match($0, /code="[0-9]+"/); code = substr($0, RSTART + 6, 3)
      total += $NF
      if (code ~ /^2/) ok += $NF
      if (code ~ /^5/) err += $NF
    }
    /^http_requests_in_flight / { inflight = $NF }
    /^http_request_duration_seconds_bucket\{/ && /method="GET",route="\/"/ {
      match($0, /le="[^"]+"/); le = substr($0, RSTART + 4, RLENGTH - 5)
      b = b (b == "" ? "" : ",") le "=" $NF
    }
    END { printf "%d %d %d %d %s\n", total, ok, err, inflight, (b == "" ? "-" : b) }'
}

# p99 <prev-buckets> <cur-buckets>: bucket bound holding the interval's p99.
p99() {
  awk -v p="$1" -v c="$2" 'BEGIN {
    n = split(c, cur, ","); split(p, old, ",")
    for (i = 1; i <= n; i++) {
      split(cur[i], kv, "="); le[i] = kv[1]; d[i] = kv[2]
      if (old[i] != "") { split(old[i], ov, "="); d[i] -= ov[2] }
    }
    total = d[n]
    if (total <= 0) { print "-"; exit }
    for (i = 1; i <= n; i++) if (d[i] >= 0.99 * total) break
    if (le[i] == "+Inf") { print ">10s"; exit }
    printf "<=%gms\n", le[i] * 1000
  }'
}

printf '%-8s  %-5s  %-11s  %6s  %5s  %4s  %9s  %s\n' TIME POD STATE RPS 2XX 5XX IN-FLIGHT P99
while :; do
  [ -n "$STOP_FILE" ] && [ -e "$STOP_FILE" ] && break
  tick_start=$(date +%s)
  pods=$(kubectl -n "$NS" get pods -l "$SELECTOR" --no-headers \
    -o custom-columns=NAME:.metadata.name,DEL:.metadata.deletionTimestamp,READY:.status.containerStatuses[0].ready 2>/dev/null)
  while read -r name del ready; do
    [ -z "$name" ] && continue
    if [ "$del" != "<none>" ]; then state=Terminating
    elif [ "$ready" = true ]; then state=Ready
    else state=NotReady; fi
    short=${name##*-}
    now=$(date +%s.%N)
    if ! metrics=$(kubectl get --raw "/api/v1/namespaces/$NS/pods/$name:9090/proxy/metrics" 2>/dev/null); then
      printf '%-8s  %-5s  %-11s  %6s\n' "$(date +%T)" "$short" "$state" gone
      continue
    fi
    read -r total ok err inflight buckets <<<"$(parse <<<"$metrics")"
    if [ -n "${prev_time[$name]:-}" ]; then
      rps=$(awk -v a="$total" -v b="${prev_total[$name]}" -v t="$now" -v s="${prev_time[$name]}" \
        'BEGIN { printf "%.1f", (a - b) / (t - s) }')
      printf '%-8s  %-5s  %-11s  %6s  %5d  %4d  %9d  %s\n' "$(date +%T)" "$short" "$state" "$rps" \
        $((ok - prev_2xx[$name])) $((err - prev_5xx[$name])) "$inflight" \
        "$(p99 "${prev_buckets[$name]}" "$buckets")"
    else
      # First scrape of this pod: totals so far, no rate yet.
      printf '%-8s  %-5s  %-11s  %6s  %5d  %4d  %9d  %s\n' "$(date +%T)" "$short" "$state" new \
        "$ok" "$err" "$inflight" "-"
    fi
    prev_total[$name]=$total prev_2xx[$name]=$ok prev_5xx[$name]=$err
    prev_buckets[$name]=$buckets prev_time[$name]=$now
  done <<<"$pods"
  elapsed=$(($(date +%s) - tick_start))
  [ "$elapsed" -lt "$INTERVAL" ] && sleep $((INTERVAL - elapsed))
done
