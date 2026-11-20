// k6 load for the chaos test (chaos/run.sh). Runs inside the cluster, in the
// service's namespace, and calls the Service by its cluster DNS name, so
// requests take the same path as real in-cluster clients: kube-proxy picks a
// ready endpoint for each new connection, and keep-alive connections stay on
// the pod they were opened to until that pod closes them.
//
// A constant arrival rate (not a fixed number of VUs) keeps the offered load
// steady while pods are replaced: a slow or failing pod cannot throttle the
// test into sending fewer requests.
//
// Every request that is not a 200 with the expected body is logged with its
// timestamp, so a failure can be matched to the rollout timeline. The
// thresholds fail the run (k6 exit code 99) on a single failed request.
//
// Environment: TARGET (URL), RATE (requests/s), DURATION (e.g. "150s").
import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

const TARGET = __ENV.TARGET || 'http://go-k8s-secure-service/';
const RATE = parseInt(__ENV.RATE || '200', 10);
const DURATION = __ENV.DURATION || '150s';

const status5xx = new Counter('status_5xx');
const transportErrors = new Counter('transport_errors');

export const options = {
  scenarios: {
    steady: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
  },
  thresholds: {
    http_req_failed: ['rate==0'],
    checks: ['rate==1'],
    status_5xx: ['count==0'],
    transport_errors: ['count==0'],
    dropped_iterations: ['count==0'],
  },
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
};

export default function () {
  const res = http.get(TARGET, { timeout: '5s' });
  const ok = check(res, {
    'status is 200': (r) => r.status === 200,
    'body is Hello, World!': (r) => r.body === 'Hello, World!\n',
  });
  // Added on every request, so the counters always have samples and their
  // thresholds are always evaluated.
  status5xx.add(res.status >= 500 ? 1 : 0);
  transportErrors.add(res.status === 0 ? 1 : 0);
  if (!ok) {
    console.error(JSON.stringify({
      t: new Date().toISOString(),
      status: res.status,
      error: res.error,
      error_code: res.error_code,
    }));
  }
}

function metric(data, name, field) {
  const m = data.metrics[name];
  return m && m.values[field] !== undefined ? m.values[field] : 0;
}

// One machine-readable line for chaos/run.sh, plus a short human summary.
export function handleSummary(data) {
  const result = {
    requests: metric(data, 'http_reqs', 'count'),
    rps: Math.round(metric(data, 'http_reqs', 'rate') * 10) / 10,
    failed: metric(data, 'http_req_failed', 'passes'),
    status_5xx: metric(data, 'status_5xx', 'count'),
    transport_errors: metric(data, 'transport_errors', 'count'),
    dropped_iterations: metric(data, 'dropped_iterations', 'count'),
    p95_ms: Math.round(metric(data, 'http_req_duration', 'p(95)') * 100) / 100,
    p99_ms: Math.round(metric(data, 'http_req_duration', 'p(99)') * 100) / 100,
    max_ms: Math.round(metric(data, 'http_req_duration', 'max') * 100) / 100,
  };
  const lines = [
    `requests           ${result.requests} (${result.rps}/s)`,
    `failed requests    ${result.failed}`,
    `5xx responses      ${result.status_5xx}`,
    `transport errors   ${result.transport_errors}`,
    `dropped iterations ${result.dropped_iterations}`,
    `latency p95 / p99  ${result.p95_ms} ms / ${result.p99_ms} ms (max ${result.max_ms} ms)`,
    `K6_RESULT ${JSON.stringify(result)}`,
  ];
  return { stdout: lines.join('\n') + '\n' };
}
