# go-k8s-secure-service

[![CI](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/ci.yml/badge.svg)](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/ci.yml)
[![Security Scan (Trivy)](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/trivy.yml/badge.svg)](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/trivy.yml)
![Go Version](https://img.shields.io/badge/Go-1.27.3-00ADD8?logo=go)
![Tests](https://img.shields.io/badge/tests-passing-brightgreen)
![Coverage](https://img.shields.io/badge/coverage-77.0%25-brightgreen)
[![Release](https://img.shields.io/github/v/release/cooler-SAI/go-k8s-secure-service?sort=semver)](https://github.com/cooler-SAI/go-k8s-secure-service/releases/latest)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/cooler-SAI/go-k8s-secure-service/badge)](https://scorecard.dev/viewer/?uri=github.com/cooler-SAI/go-k8s-secure-service)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A lightweight, secure Go HTTP service designed for containerized deployment and orchestration in Kubernetes environments.

**Contents:** [Status](#project-status) · [Overview](#overview) · [HTTP API](#http-api) · [Getting Started](#getting-started) · [Logging](#logging) · [Metrics](#metrics) · [Tracing](#tracing-opentelemetry) · [Kubernetes](#kubernetes-integration) · [CI/CD](#cicd-pipelines) · [Troubleshooting](#troubleshooting) · [Roadmap](#roadmap--security-hardening) · [Security](#security-info)

---

## Project Status

| Metric / Aspect | Status | Details |
| :--- | :--- | :--- |
| **Development Lifecycle** | **Stable / Production-Ready Baseline** | Core microservice architecture, Kubernetes orchestration manifests, and security hardening are complete. |
| **Build & Test Suite** | **Passing (100%)** | Unit and integration tests verify handlers, routing, server timeout settings, metrics, trace propagation, and shutdown lifecycle with **77.0%** statement coverage. Race detection runs in CI. |
| **Security Scanning** | **Verified (Trivy SARIF)** | Automated weekly and PR/push scans (Trivy, plus CodeQL static analysis of the Go code) covering repository dependencies, container image, secret detection, and IaC/Kubernetes configurations (`CRITICAL,HIGH` threshold). |
| **Dependencies** | **Small, scanned** | Go standard library plus [`zerolog`](https://github.com/rs/zerolog) for structured logging and the official [OpenTelemetry Go SDK](https://opentelemetry.io/docs/languages/go/) (`otel`, `otel/sdk`, `otelhttp`, OTLP/HTTP exporter) for tracing. Prometheus metrics are implemented without `client_golang`. All modules are scanned by Trivy and updated by Dependabot. |
| **Observability** | **RED Metrics + OpenTelemetry Tracing** | Request rate, errors and duration at `/metrics` on a separate internal port (`:9090`) that is not part of the Service or Ingress. Distributed tracing with W3C Trace Context (`traceparent`) propagation on incoming and outgoing requests, OTLP/HTTP export, and `trace_id`/`span_id` in request logs. |
| **Container Hardening** | **Distroless Non-Root (`65532:65532`)** | Multi-stage build (`golang:1.27.3-alpine` builder) deploying to `gcr.io/distroless/static-debian13:nonroot` with read-only rootfs and dropped Linux capabilities. |
| **Kubernetes Compliance** | **Production Baseline Manifests** | Hardened `Deployment` (probes, non-root `SecurityContext`, `GOMEMLIMIT`), `ClusterIP Service`, `PodDisruptionBudget`, least-privilege `NetworkPolicy` (default deny egress, restricted ingress for traffic and metrics), and CEL-based Kyverno `ValidatingPolicy`/`ImageValidatingPolicy` rules (non-root, non-privileged, probes, resource limits, image signature). |
| **CI/CD Automation** | **Active (GitHub Actions)** | Continuous integration enforcing formatting (`gofmt`), module verification, code vet (`go vet`), test suite with race detector, router fuzzing, static binary build verification, offline Kyverno policy tests (checksum-verified CLI), a kind E2E smoke test under the `restricted` Pod Security profile with Kyverno enforcing (including live Cosign signature verification), and Trivy security scanning. |
| **Supply Chain** | **SBOM + Signed Releases** | Trivy-generated SPDX/CycloneDX SBOMs, and tagged releases pushed to GHCR with keyless Cosign signatures and SBOM attestations, verified at admission by Kyverno. |

---

## Overview

`go-k8s-secure-service` serves as a foundation for building resilient, cloud-native microservices in Go. It provides standard HTTP endpoints, built-in Kubernetes health check probes (`/livez` and `/readyz`), Prometheus RED metrics on a separate internal port, OpenTelemetry distributed tracing with W3C `traceparent` propagation, graceful shutdown handling via `SIGTERM`, and an enterprise-grade security baseline for containerized environments.

## Features

- **Root Endpoint (`/`)**: Returns a basic greeting (`Hello, World!`) with HTTP status 200 OK. Unknown paths return 404 Not Found.
- **Method Restriction**: All endpoints accept only `GET` (and `HEAD`); other methods receive `405 Method Not Allowed` with an `Allow` header.
- **Configurable Listen Port**: Reads the standard `PORT` environment variable (e.g., `PORT=8080` or `PORT=:8080`) to bind to custom ports, defaulting to `:8080` for container and Kubernetes environments.
- **Kubernetes Liveness Probe (`/livez`)**: Provides a dedicated health probe endpoint for orchestrators (e.g., Kubernetes `livenessProbe`) to monitor service vitality.
- **Kubernetes Readiness Probe (`/readyz`)**: Signals service readiness to receive incoming traffic (Kubernetes `readinessProbe`). Automatically switches to `503 Service Unavailable` on shutdown signals to immediately stop traffic ingress before connection draining completes.
- **Graceful Shutdown (`SIGTERM` / `SIGINT`)**: Listens for termination signals from Kubernetes (`SIGTERM`) or process interruptions (`SIGINT`), reporting not-ready and continuing to serve for a 5-second drain delay while endpoint removal propagates, then running a graceful shutdown (`srv.Shutdown()`) with a 10-second window for in-flight requests.
- **Hardened HTTP Timeouts**: Explicitly configures `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` to mitigate Slowloris and connection starvation attacks.
- **Prometheus Metrics (RED)**: Request rate, errors and duration per route at `/metrics`, served on a separate internal port (`METRICS_PORT`, default `:9090`) so it is never exposed through the Service or an Ingress. No `client_golang` dependency. See [Metrics](#metrics).
- **Distributed Tracing (OpenTelemetry)**: Continues the caller's trace from the W3C `traceparent` header (or starts a new one), and provides an instrumented HTTP client that injects `traceparent` into outgoing requests. Spans are exported over OTLP/HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. See [Tracing](#tracing-opentelemetry).
- **Structured Logging**: Emits JSON logs to stdout with `zerolog`, ready for container log collectors (Loki, Fluent Bit, Cloud Logging, etc.). `net/http` and OpenTelemetry internal errors go through the same logger, and request error logs carry `trace_id`/`span_id`.
- **Hardened Container Image**: Uses a multi-stage build and a minimal distroless runtime, runs as a non-root user, and builds a static binary.
- **Comprehensive Unit Tests**: Test suite verifying handlers, routing, metrics, end-to-end trace propagation (incoming `traceparent` → server span → client span → downstream `traceparent`), OTLP export, server timeout settings, and shutdown lifecycle.

---

## HTTP API

All responses are `text/plain`. Every route accepts `GET` and `HEAD` only.

| Endpoint | Purpose | Normal response | Other responses |
| :--- | :--- | :--- | :--- |
| `GET /` | Root / greeting | `200` `Hello, World!` | — |
| `GET /livez` | Liveness probe: the process is running | `200` `OK` | — |
| `GET /readyz` | Readiness probe: the pod should receive traffic | `200` `OK` | `503` `Not Ready` after `SIGTERM`/`SIGINT` while draining |
| Any other path | — | — | `404` `404 page not found` |
| Known path, other method (e.g. `POST /`) | — | — | `405 Method Not Allowed` with `Allow: GET, HEAD` |

`/livez` intentionally stays `200` during shutdown, so Kubernetes does not
restart a pod that is draining.

Requests may carry the W3C Trace Context headers `traceparent`/`tracestate`
and `baggage`; the service continues that trace. They are optional and never
change the response.

The metrics server listens on its own port (`METRICS_PORT`, default `:9090`)
and serves a single route:

| Endpoint | Purpose | Normal response | Other responses |
| :--- | :--- | :--- | :--- |
| `GET :9090/metrics` | Prometheus scrape target | `200` Prometheus text format (`text/plain; version=0.0.4`) | `404` for other paths, `405` for other methods |

`/metrics` is not served on the traffic port: `GET :8080/metrics` returns `404`.

---

## Project Structure

```text
go-k8s-secure-service/
├── .github/
│   ├── dependabot.yml  # Weekly updates for Go modules, Actions and Docker images
│   └── workflows/
│       ├── ci.yml          # gofmt, vet, race-enabled tests, build, Kyverno policy tests, kind E2E
│       ├── trivy.yml       # Trivy repo and container image scans, SBOM generation
│       ├── scorecard.yml   # OpenSSF Scorecard supply-chain analysis
│       ├── codeql.yml      # CodeQL static analysis (Go)
│       └── release.yml     # Tag release: GHCR push, SBOMs, keyless Cosign signing, GitHub Release
├── .idea/              # IDE configuration
├── e2e/
│   ├── namespace.yaml     # Namespace enforcing the restricted Pod Security profile
│   ├── smoke.sh           # Deploys to a cluster and checks endpoints via port-forward
│   └── admission.sh       # Live Kyverno admission checks, incl. Cosign signature verification
├── k8s/
│   ├── deployment.yaml    # Hardened Deployment (security context, probes, resources, OTel env)
│   ├── service.yaml       # ClusterIP Service exposing the HTTP port
│   ├── pdb.yaml           # PodDisruptionBudget keeping a replica up during disruptions
│   ├── networkpolicy.yaml # Default-deny NetworkPolicy: HTTP ingress, metrics from monitoring only
│   └── policies/          # CEL-based Kyverno ValidatingPolicy/ImageValidatingPolicy and offline tests (tests/)
├── .aiignore           # AI tool ignore rules
├── .gitignore          # Git ignore rules
├── .dockerignore       # Excludes development files from the build context
├── Dockerfile          # Multi-stage hardened container build
├── go.mod              # Go module definition and dependencies
├── go.sum              # Dependency checksums
├── LICENSE             # MIT License
├── SECURITY.md         # Security policy and vulnerability reporting
├── main.go             # Application entry point, server configuration, and HTTP handlers
├── main_test.go        # Unit and integration test suite
├── metrics.go          # Prometheus RED metrics middleware and /metrics exposition (stdlib only)
├── metrics_test.go     # Metrics, histogram and label-cardinality tests
├── tracing.go          # OpenTelemetry setup, traced handler, outbound client, log correlation
├── tracing_test.go     # Trace propagation, span naming, probe filtering and OTLP export tests
├── trivy.yaml          # Shared Trivy scanner configuration
└── README.md           # Project documentation
```

---

## Getting Started

### Prerequisites

- [Go](https://go.dev/dl/) 1.27.3 or later
- Docker with BuildKit support (enabled by default in current Docker releases)
- `curl` or any API client (optional, for testing endpoints)

### Running Locally

1. Clone the repository:
   ```bash
   git clone https://github.com/cooler-SAI/go-k8s-secure-service.git
   cd go-k8s-secure-service
   ```

2. Run the application directly:
   ```bash
   go run .
   ```

3. The service starts listening for traffic on `:8080` (or `$PORT`) and for
   Prometheus scrapes on `:9090` (or `$METRICS_PORT`):
   ```text
   {"level":"info","exporting":false,"time":"2026-11-05T22:48:55+03:00","message":"tracing configured"}
   {"level":"info","server":"metrics","addr":"[::]:9090","time":"2026-11-05T22:48:55+03:00","message":"server starting"}
   {"level":"info","server":"http","addr":"[::]:8080","time":"2026-11-05T22:48:55+03:00","message":"server starting"}
   ```

### Configuration and Environment Variables

The service is configured through environment variables: the traffic and metrics listen ports, and tracing through the standard `OTEL_*` variables. Operational timeouts and shutdown intervals use production-tuned defaults defined in `main.go` and `tracing.go`.

| Setting | Configuration | Default | Description |
| --- | --- | --- | --- |
| Listen port | `PORT` env var | `:8080` | Binds the HTTP server to the specified port on all network interfaces (e.g., `PORT=8080` or `PORT=:8080`). |
| Metrics port | `METRICS_PORT` env var | `:9090` | Binds the internal metrics server (`/metrics` only). Must differ from `PORT`. |
| Tracing | `OTEL_*` env vars | spans created, not exported | See [Tracing configuration](#tracing-configuration). |
| Read-header timeout | Fixed (`main.go`) | 5 seconds | Limits the time allowed to read request headers. |
| Read timeout | Fixed (`main.go`) | 10 seconds | Limits the time allowed to read the full request. |
| Write timeout | Fixed (`main.go`) | 10 seconds | Limits the time allowed to write a response. |
| Idle timeout | Fixed (`main.go`) | 60 seconds | Limits how long an idle keep-alive connection remains open. |
| Shutdown drain delay | Fixed (`main.go`) | 5 seconds | Time the server keeps serving (while `/readyz` reports 503) after a termination signal, before closing the listener. |
| Shutdown timeout | Fixed (`main.go`) | 10 seconds | Maximum time allowed for in-flight requests to finish once the listener is closed. |
| Trace flush timeout | Fixed (`tracing.go`) | 5 seconds | Maximum time to export buffered spans after the servers have stopped. |

To change the timeout settings, update the corresponding server configuration in
`main.go`; both servers use the same timeouts. The Kubernetes container ports,
probes and Prometheus annotations should match the configured ports.

### Running Tests

Execute the unit test suite with coverage report:

```bash
go test -v -cover ./...
```

The router is also covered by a native Go fuzz test (`FuzzHandler`) that sends
arbitrary methods and request targets through `net/http`'s request parser into
the metrics-instrumented router and asserts that it never panics, never answers with a 5xx, and
only returns `200` for the known paths with `GET`/`HEAD`. Its seed corpus runs
with the normal test suite; to fuzz for a while:

```bash
go test -run '^$' -fuzz FuzzHandler -fuzztime 60s .
```

### Pre-Push Checks

These commands mirror what CI runs, so a green local run should mean a green
pipeline (`-race` needs CGO and a C toolchain):

```bash
gofmt -l .                       # must print nothing
go mod verify
go vet ./...
go test -race -cover ./...
go test -run '^$' -fuzz FuzzHandler -fuzztime 30s .
kyverno test k8s/policies/tests  # needs the Kyverno CLI
trivy fs --config trivy.yaml .   # deps, IaC misconfigurations, secrets
```

On Windows with `core.autocrlf=true`, `gofmt -l .` lists every Go file because
of the CRLF line endings in the working copy; Git stores LF, which is what CI
checks. Use `git ls-files '*.go' | xargs -I{} sh -c 'git show :{} | gofmt -l'`
there, or check in WSL.

To scan a locally built image with the same settings, run
`trivy image --config trivy.yaml go-k8s-secure-service:local`.

### Building and Running the Container

Build the image from the repository root:

```bash
docker build -t go-k8s-secure-service:local .
```

Run it with a read-only root filesystem and no Linux capabilities:

```bash
docker run --rm \
  --read-only \
  --cap-drop=ALL \
  --security-opt=no-new-privileges \
  -p 8080:8080 \
  -p 9090:9090 \
  go-k8s-secure-service:local
```

The runtime image contains only the service binary and required runtime data;
it has no shell or package manager. The process runs as UID/GID `65532`, and
the image uses exec-form `ENTRYPOINT` so termination signals reach the Go
process directly. BuildKit cache mounts speed up repeated Go builds without
adding build caches to the final image.

### Verifying Endpoints

- **Root endpoint**:
  ```bash
  curl http://localhost:8080/
  # Output: Hello, World!
  ```

- **Liveness probe endpoint**:
  ```bash
  curl http://localhost:8080/livez
  # Output: OK
  ```

- **Readiness probe endpoint**:
  ```bash
  curl http://localhost:8080/readyz
  # Output: OK
  ```

- **Unknown path and wrong method**:
  ```bash
  curl -i http://localhost:8080/missing   # HTTP/1.1 404 Not Found
  curl -i -X POST http://localhost:8080/  # HTTP/1.1 405 Method Not Allowed, Allow: GET, HEAD
  ```

- **Metrics (internal port only)**:
  ```bash
  curl http://localhost:9090/metrics
  # Output: http_requests_total{method="GET",route="/",code="200"} 1 ...
  curl -i http://localhost:8080/metrics   # HTTP/1.1 404 Not Found
  ```

---

## Logging

The service writes one JSON object per line to stdout using
[`zerolog`](https://github.com/rs/zerolog). Each line has `level`, `time`
(RFC 3339) and `message`, plus event-specific fields. A normal start and
`SIGTERM` shutdown looks like this:

```text
{"level":"info","exporting":false,"time":"2026-10-16T01:42:19+03:00","message":"tracing configured"}
{"level":"info","server":"metrics","addr":"[::]:9090","time":"2026-10-16T01:42:19+03:00","message":"server starting"}
{"level":"info","server":"http","addr":"[::]:8080","time":"2026-10-16T01:42:19+03:00","message":"server starting"}
{"level":"info","drain_delay":"5s","time":"2026-10-16T01:45:02+03:00","message":"shutdown signal received, reporting not-ready and draining"}
{"level":"info","shutdown_timeout":"10s","time":"2026-10-16T01:45:07+03:00","message":"drain delay elapsed, shutting down HTTP server"}
{"level":"info","time":"2026-10-16T01:45:07+03:00","message":"server gracefully stopped"}
```

Errors are logged at `level: "error"` with an `error` field. Internal
`net/http` errors carry `"component":"net/http"`, and OpenTelemetry errors
(for example a collector that cannot be reached) are logged at `warn` with
`"component":"otel"`. Errors while handling a traced request also carry
`trace_id` and `span_id`, so a log line leads straight to its trace. If the server cannot start
or shut down cleanly, it logs `server terminated with error` and exits with
code `1`.

To follow logs in a cluster, run `kubectl logs -f deploy/go-k8s-secure-service`.

---

## Metrics

The service exposes RED metrics (Rate, Errors, Duration) in the Prometheus
text format at `/metrics` on the **internal metrics port** (`:9090`). The
traffic port (`:8080`) does not serve it, the Service lists only the traffic
port, and the NetworkPolicy admits the metrics port only from the
`monitoring` namespace, so metrics never leak through an Ingress.

| Metric | Type | Labels | Meaning |
| :--- | :--- | :--- | :--- |
| `http_requests_total` | counter | `method`, `route`, `code` | Requests served: **Rate**, and **Errors** via `code=~"5.."` |
| `http_request_duration_seconds` | histogram | `method`, `route` | Request latency (**Duration**), Prometheus default buckets 5ms–10s |
| `http_requests_in_flight` | gauge | — | Requests currently being served |

Labels have bounded cardinality, so clients cannot create unlimited series:

- `route` is the matched route pattern (`/`, `/livez`, `/readyz`), never the
  raw path; anything that matches no route (404, 405) is `unmatched`.
- `method` is one of the standard HTTP methods; anything else is `OTHER`.

The instrumentation is a small standard-library middleware
([`metrics.go`](metrics.go)) rather than `prometheus/client_golang`, which would
add several transitive modules (protobuf, procfs, ...) to the image and to the
Trivy scan. It
exports only the HTTP metrics above, not Go runtime (`go_*`) or `process_*`
metrics.

Example PromQL queries:

```promql
# Rate: requests per second by route
sum by (route) (rate(http_requests_total[5m]))

# Errors: share of 5xx responses
sum(rate(http_requests_total{code=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))

# Duration: p99 latency by route
histogram_quantile(0.99, sum by (le, route) (rate(http_request_duration_seconds_bucket[5m])))
```

Probe traffic (`/livez`, `/readyz`) is counted under its own routes; filter it
out with `route!~"/livez|/readyz"` when you only want user traffic.

### Scraping in Kubernetes

The pod template carries the common `prometheus.io/scrape`, `prometheus.io/port`
and `prometheus.io/path` annotations for annotation-based discovery. With the
Prometheus Operator, scrape the named `metrics` container port with a
`PodMonitor` (pods are scraped directly, because the Service intentionally has
no metrics port):

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: go-k8s-secure-service
spec:
  selector:
    matchLabels:
      app: go-k8s-secure-service
  podMetricsEndpoints:
    - port: metrics
      path: /metrics
```

If Prometheus runs in a namespace other than `monitoring`, update the metrics
rule in [`k8s/networkpolicy.yaml`](k8s/networkpolicy.yaml).

---

## Tracing (OpenTelemetry)

The service uses the [OpenTelemetry Go SDK](https://opentelemetry.io/docs/languages/go/)
for distributed tracing, with [W3C Trace Context](https://www.w3.org/TR/trace-context/)
propagation in both directions:

- **Incoming requests**: the `traceparent` (and `tracestate`, `baggage`)
  headers are extracted and the request's server span joins the caller's
  trace. Without them, a new trace is started.
- **Outgoing requests**: `newOutboundClient` in [`tracing.go`](tracing.go)
  returns an `http.Client` whose transport starts a client span and injects
  `traceparent` into the outgoing request, so the downstream service
  continues the same trace. The service makes no outbound calls today; use this
  client for any you add, and build requests with the incoming request's
  context:

  ```go
  req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
  resp, err := client.Do(req) // client := newOutboundClient(tr)
  ```

A request from an upstream service therefore produces one connected trace:

```text
upstream (span P) ──traceparent T/P──▶ server span S "GET /" ──▶ client span C ──traceparent T/C──▶ downstream
                                       (trace T, parent P)       (trace T, parent S)
```

Span names follow the HTTP semantic conventions: `GET /` for a matched route
(with an `http.route` attribute) and just the method (`GET`) when no route
matched, so raw request paths never become span names. The liveness and
readiness probes are not traced; they would produce a span every few seconds
per pod. `/metrics` runs on its own server and is not traced either.

### Tracing configuration

Tracing is configured with the standard
[OpenTelemetry environment variables](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/):

| Variable | Default | Effect |
| :--- | :--- | :--- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` (or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`) | unset | When set (e.g. `http://otel-collector:4318`), spans are batched and exported over OTLP/HTTP. When unset, spans are still created and propagated and trace IDs appear in logs, but nothing is exported. |
| `OTEL_SERVICE_NAME` | `go-k8s-secure-service` | `service.name` resource attribute. |
| `OTEL_RESOURCE_ATTRIBUTES` | — | Extra resource attributes; the Deployment sets `k8s.pod.name`, `k8s.namespace.name` and `k8s.node.name` from the downward API. |
| `OTEL_TRACES_SAMPLER` / `OTEL_TRACES_SAMPLER_ARG` | `parentbased_always_on` | Sampling, e.g. `parentbased_traceidratio` with `0.1` to keep 10% of new traces. |
| `OTEL_SDK_DISABLED` | `false` | `true` disables span recording; `traceparent` is still passed through unchanged from incoming to outgoing requests. |
| Other `OTEL_EXPORTER_OTLP_*` | SDK defaults | Headers, timeout, compression, TLS certificates, read by the exporter. |

To try it locally with a collector or Jaeger listening for OTLP/HTTP on 4318:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 go run .
curl -H 'traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01' http://localhost:8080/
```

Notes:

- **Exporting in Kubernetes** needs egress. The NetworkPolicy denies all
  egress by default, so to export spans set `OTEL_EXPORTER_OTLP_ENDPOINT` in
  [`k8s/deployment.yaml`](k8s/deployment.yaml) and enable the egress rule for
  the collector and DNS that is provided, commented out, in
  [`k8s/networkpolicy.yaml`](k8s/networkpolicy.yaml).
- **Untrusted `traceparent`**: with the default parent-based sampler, a
  client that sends `traceparent` with the sampled flag decides whether its
  request is sampled. If the service is reachable from the internet, let the
  ingress or gateway start (or reset) traces, or set
  `OTEL_TRACES_SAMPLER=traceidratio` so the service samples independently.
- **Shutdown**: buffered spans are flushed after both servers have stopped,
  for up to 5 seconds, so the spans of the last drained requests are not lost.
- **Footprint**: the SDK and the OTLP/HTTP exporter (which pulls in the
  protobuf and gRPC status packages for its wire format, though it does not use
  gRPC transport) grow the static binary from about 6 MB to about 16 MB.
  OpenTelemetry's own HTTP metrics are disabled, since [`metrics.go`](metrics.go)
  already records them.

---

## Kubernetes Integration

### Pod Lifecycle, Probes, and SIGTERM Handling

When Kubernetes scales down a deployment, performs a rolling update, or terminates a pod:
1. Kubernetes sends a `SIGTERM` signal to the container's PID 1 process.
2. In parallel, Kubernetes starts removing the pod from Service endpoints. That removal reaches kube-proxy, load balancers, and ingress controllers asynchronously, so new requests can still arrive for a few seconds.
3. `go-k8s-secure-service` catches `SIGTERM`, flips `/readyz` to `503 Service Unavailable` (`Not Ready`), and **keeps serving for a 5-second drain delay** so requests still being routed to the pod are handled instead of refused.
4. After the delay, `http.Server.Shutdown(shutdownCtx)` closes the listener and grants in-flight requests up to 10 seconds to finish processing cleanly.
5. The metrics server is shut down, then buffered trace spans are flushed to the collector (up to 5 seconds).
6. The process exits cleanly with code 0.

The drain delay (5s), the shutdown timeout (10s) and the trace flush timeout
(5s) together must stay below the pod's `terminationGracePeriodSeconds` (30s
in `k8s/deployment.yaml`); otherwise the kubelet sends `SIGKILL` before
draining finishes.

### Kubernetes Manifests

The manifests live in [`k8s/`](k8s/):

- [`deployment.yaml`](k8s/deployment.yaml): runs two replicas as UID/GID
  `65532`, disables service-account token mounting, uses a read-only root
  filesystem, drops Linux capabilities, sets resource requests and limits,
  configures the `/livez` and `/readyz` probes, and sets `GOMEMLIMIT` to ~90%
  of the memory limit so the Go GC works harder before the container is
  OOM-killed. It declares two named ports, `http` (`8080`) and `metrics`
  (`9090`), Prometheus scrape annotations, and the OpenTelemetry settings
  (`OTEL_SERVICE_NAME`, Kubernetes resource attributes from the downward API,
  and a commented-out `OTEL_EXPORTER_OTLP_ENDPOINT`).
- [`service.yaml`](k8s/service.yaml): a `ClusterIP` Service routing port `80`
  to the container's named `http` port (`8080`). The `metrics` port is
  deliberately left off, so an Ingress or Gateway pointing at this Service
  can never reach `/metrics`.
- [`pdb.yaml`](k8s/pdb.yaml): a `PodDisruptionBudget` with `minAvailable: 1`,
  so node drains and cluster upgrades never evict both replicas at once.
  `unhealthyPodEvictionPolicy: AlwaysAllow` lets pods that never became ready
  be evicted, so a broken rollout cannot block node drains.
- [`networkpolicy.yaml`](k8s/networkpolicy.yaml): default-deny for the
  service's pods. Ingress is allowed on the `http` port from pods in the
  same namespace and from the `ingress-nginx` namespace (change this to match
  your ingress controller or Gateway), and on the `metrics` port only from the
  `monitoring` namespace (change this to where Prometheus runs). All egress is denied, since the service
  makes no outbound calls; a commented-out rule shows how to allow OTLP
  export to a collector in the `observability` namespace plus DNS. NetworkPolicy is enforced only if your CNI supports
  it (e.g. Calico, Cilium).
- [`policies/`](k8s/policies/): CEL-based Kyverno policies
  (`policies.kyverno.io/v1`, Kyverno 1.19+). Four `ValidatingPolicy`
  resources deny containers running as `runAsUser: 0`, deny privileged
  containers, and require liveness/readiness probes and CPU/memory limits.
  Kyverno auto-generates the same checks for Deployments, StatefulSets,
  DaemonSets, ReplicaSets, Jobs and CronJobs. The `kube-system`,
  `kube-public`, `kube-node-lease` and `kyverno` namespaces are excluded. They are not applied by `kubectl apply -f k8s/`; install
  Kyverno and apply them with `kubectl apply -f k8s/policies/`. Policy
  tests (`kyverno test k8s/policies/tests`) run offline against fixture pods
  and `deployment.yaml`, and CI runs them on every push and PR.
  `verify-image-signature.yaml`, an `ImageValidatingPolicy`, additionally requires images from
  `ghcr.io/cooler-sai/go-k8s-secure-service` to carry a Cosign keyless
  signature from the release workflow (see below); it only matches that
  registry path, so locally built images are unaffected.

### Supply Chain: SBOM and Image Signing

- **SBOM**: CI generates a Software Bill of Materials for the container image
  with Trivy in both SPDX (`sbom.spdx.json`) and CycloneDX (`sbom.cdx.json`)
  formats, uploaded as the `sbom` workflow artifact on every push and PR
  (`.github/workflows/trivy.yml`).
- **Release**: pushing a `v*` tag runs `.github/workflows/release.yml`, which
  builds and pushes `ghcr.io/cooler-sai/go-k8s-secure-service:<tag>`, signs
  the image digest with Cosign keyless signing (Sigstore Fulcio + Rekor, no
  long-lived keys), and attaches both SBOMs as signed attestations. It also
  publishes a GitHub Release containing the SBOMs and a Sigstore bundle
  (`*.sigstore.json`) for each of them (`cosign verify-blob --bundle`).
- **Verify a signature manually**:
  ```bash
  cosign verify ghcr.io/cooler-sai/go-k8s-secure-service:v1.0.10 \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity-regexp '^https://github\.com/cooler-SAI/go-k8s-secure-service/\.github/workflows/release\.yml@refs/tags/v.*$'
  ```
- **Cluster enforcement**: the `verify-image-signature` Kyverno policy applies
  the same identity check at admission time.

### End-to-End Smoke Test

[`e2e/smoke.sh`](e2e/smoke.sh) deploys the manifests into a namespace that
enforces the Pod Security `restricted` profile
([`e2e/namespace.yaml`](e2e/namespace.yaml)), waits for both pods to become
Ready, and calls `/`, `/livez`, `/readyz`, an unknown path (404) and a `POST`
(405) through `kubectl port-forward`. It also checks that `/metrics` returns
404 through the Service and that the Service does not expose the metrics port,
then scrapes a pod's `metrics` port for the RED metric families. CI runs it on a single-node
[kind](https://kind.sigs.k8s.io/) cluster on every push and PR, with Kyverno
and the policies in [`k8s/policies/`](k8s/policies/) installed, so the
Deployment is also admitted under them.

[`e2e/admission.sh`](e2e/admission.sh) then checks the policies against the
live cluster with server-side dry-runs:

- the Deployment with a signed release image from GHCR (`v1.0.3`, pinned by
  digest; override with `SIGNED_IMAGE`) is admitted, which verifies its Cosign
  signature against Sigstore;
- the same image is rejected when the policy expects a different signer, so
  the signer identity is really checked;
- a root, privileged pod without probes or limits is rejected by each of the
  other four policies.

The offline `kyverno test` can't cover the signature check, because it never
contacts the registry. To run both locally:

```bash
docker build -t go-k8s-secure-service:v1.0.0 .
kind create cluster --name e2e
kind load docker-image go-k8s-secure-service:v1.0.0 --name e2e
helm install kyverno kyverno --repo https://kyverno.github.io/kyverno/ \
  --version 3.9.1 --namespace kyverno --create-namespace --wait
kubectl apply -f k8s/policies/   # rerun if Kyverno's webhook isn't accepting connections yet
kubectl wait --for=jsonpath='{.status.conditionStatus.ready}'=true \
  validatingpolicy,imagevalidatingpolicy --all --timeout=120s
./e2e/smoke.sh
./e2e/admission.sh
```

Make the `go-k8s-secure-service:v1.0.0` image available to your cluster nodes
or update the manifest to use an image in your container registry, then apply
everything:

```bash
kubectl apply -f k8s/
```

### Deploying a Signed Release Image

The manifest refers to a locally built `go-k8s-secure-service:v1.0.0`. To run
the signed image from GHCR, point the Deployment at a release tag:

```bash
kubectl apply -f k8s/
kubectl set image deployment/go-k8s-secure-service \
  server=ghcr.io/cooler-sai/go-k8s-secure-service:v1.0.10
kubectl rollout status deployment/go-k8s-secure-service
```

With the Kyverno policies installed, admission checks the Cosign signature.
Images that are unsigned or signed by anyone other than this repository's
release workflow are rejected.

To reach the service without an ingress:

```bash
kubectl port-forward svc/go-k8s-secure-service 8080:80
curl http://localhost:8080/
```

---

## CI/CD Pipelines

| Workflow | Triggers | What it does |
| :--- | :--- | :--- |
| [`ci.yml`](.github/workflows/ci.yml) | push / PR to `main` | `gofmt` check, `go mod verify`, `go vet`, race-enabled tests with coverage, 30s of `FuzzHandler`, static build; Kyverno policy tests (CLI pinned by SHA-256); kind E2E smoke test and live Kyverno admission test |
| [`trivy.yml`](.github/workflows/trivy.yml) | push / PR to `main`, weekly (Mon 06:00 UTC) | Repo scan (deps, IaC, secrets) and image scan with SARIF upload to Code Scanning; SPDX + CycloneDX SBOM artifact |
| [`codeql.yml`](.github/workflows/codeql.yml) | push / PR to `main`, weekly (Wed 07:00 UTC) | CodeQL static analysis of the Go code |
| [`scorecard.yml`](.github/workflows/scorecard.yml) | push to `main`, branch protection changes, weekly (Tue 06:30 UTC) | OpenSSF Scorecard supply-chain checks |
| [`release.yml`](.github/workflows/release.yml) | push of a `v*` tag | Push image to GHCR, keyless Cosign signature, SBOM attestations, GitHub Release with signed SBOMs |

Dependabot ([`.github/dependabot.yml`](.github/dependabot.yml)) opens weekly
update PRs for Go modules, GitHub Actions and Docker base images. All actions
and base images are pinned by SHA/digest.

To cut a release, push an annotated tag from an up-to-date `main` (changes
reach `main` only through pull requests). The release workflow creates the
GitHub Release itself, so don't create it by hand:

```bash
git switch main && git pull --ff-only
git tag -a v1.0.11 -m "v1.0.11"
git push origin v1.0.11
```

Published releases are listed on the
[Releases page](https://github.com/cooler-SAI/go-k8s-secure-service/releases).

---

## Troubleshooting

| Symptom | Likely cause and fix |
| :--- | :--- |
| `ErrImagePull` / `ImagePullBackOff` on kind or a local cluster | The cluster nodes don't have the local image. Run `kind load docker-image go-k8s-secure-service:v1.0.0 --name <cluster>`, or deploy the GHCR image ([above](#deploying-a-signed-release-image)). |
| Pods rejected by admission with a Kyverno message | A manifest change broke a policy (root user, privileged, missing probes or limits), or a GHCR image is unsigned. Run `kyverno test k8s/policies/tests` locally to reproduce. |
| Prometheus target down / scrape timeouts | The NetworkPolicy admits the metrics port only from the `monitoring` namespace. Change the namespace in `k8s/networkpolicy.yaml` to where Prometheus runs, and scrape the pod's `metrics` port (`9090`), not the Service. |
| `/metrics` returns `404` | You are calling the traffic port (`8080`) or the Service. Metrics are only on the pod's metrics port: `kubectl port-forward deploy/go-k8s-secure-service 9090:metrics`. |
| Service unreachable from another namespace | The NetworkPolicy allows ingress only from the same namespace and `ingress-nginx`. Add your client or ingress namespace to `k8s/networkpolicy.yaml`. |
| `/readyz` returns `503 Not Ready` | Expected while the pod drains after `SIGTERM`. Kubernetes removes the pod from endpoints. |
| Pod `OOMKilled` | The 64Mi memory limit is too low for your load. Raise `resources.limits.memory` and keep `GOMEMLIMIT` at ~90% of it. |
| Pod `SIGKILL`ed before draining finishes | `terminationGracePeriodSeconds` must exceed drain delay + shutdown timeout + trace flush (5s + 10s + 5s). |
| No traces in the backend | `OTEL_EXPORTER_OTLP_ENDPOINT` is unset (the `tracing configured` log line shows `"exporting":false`), or the NetworkPolicy blocks egress to the collector or DNS: enable the egress rule in `k8s/networkpolicy.yaml`. Export failures are logged with `"component":"otel"`. |
| Traces are not connected across services | The caller must send `traceparent`, and outgoing calls must use `newOutboundClient` with the incoming request's context (`http.NewRequestWithContext(r.Context(), ...)`). |
| `failed to listen on :8080` | The port is already in use. Set another one, e.g. `PORT=8081 go run .`, and update the container port and probes to match. |
| `failed to listen on :9090 for metrics` | The metrics port is in use (or equals `PORT`). Set another one, e.g. `METRICS_PORT=9091 go run .`, and update the `metrics` container port and `prometheus.io/port` annotation. |

---

## Roadmap & Security Hardening

- [x] **Go Modules**: Initialize `go.mod` for dependency management and version reproducibility.
- [x] **Graceful Shutdown**: Implement clean signal handling (`SIGINT`, `SIGTERM`) using `context` to prevent dropped in-flight requests during rolling updates.
- [x] **HTTP Timeouts**: Configure explicit `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` on `http.Server` to mitigate Slowloris / denial-of-service attacks.
- [x] **Automated Testing & CI/CD**: Unit testing with `net/http/httptest`; GitHub Actions runs `gofmt`, `go vet`, race-enabled tests, and a build on every push and PR (`.github/workflows/ci.yml`).
- [x] **Security Scanning**: Trivy scans dependencies, Dockerfile/Kubernetes misconfigurations, and secrets in the repo, plus the built container image, failing on HIGH/CRITICAL findings and uploading SARIF to GitHub Code Scanning. All actions are pinned to commit SHAs (`.github/workflows/trivy.yml`).
- [x] **Readiness Probe**: Add `/readyz` for traffic readiness verification.
- [x] **Structured Logging**: Structured JSON logging on stdout via `zerolog`, including `net/http` internal errors.
- [x] **Configurable Environment**: Support configuring the HTTP listen port via the standard `PORT` environment variable.
- [x] **Containerization & Hardening**: Multi-stage distroless image, static binary, non-root runtime user, and hardened Kubernetes `SecurityContext` example.
- [x] **Network Policy**: Least-privilege Kubernetes `NetworkPolicy`: ingress only on the HTTP port from the same namespace and the ingress controller, all egress denied.
- [x] **Admission Policies (Kyverno)**: CEL-based `ValidatingPolicy` rules (`policies.kyverno.io/v1`) for non-root, non-privileged containers, mandatory probes and resource limits, validated offline in CI with `kyverno test`.
- [x] **SBOM & Image Signing (Cosign)**: Trivy-generated SPDX/CycloneDX SBOMs in CI, keyless Cosign signing and SBOM attestations on release, and a Kyverno policy that verifies the signature at admission.
- [x] **E2E Smoke Test (kind)**: GitHub Actions deploys the manifests to a single-node kind cluster under the `restricted` Pod Security profile and checks the endpoints through port-forward, with Kyverno installed and a live admission test that verifies a signed release image.
- [x] **OpenSSF Scorecard**: Weekly and on-push Scorecard analysis (`.github/workflows/scorecard.yml`) with results published to the public badge and uploaded to GitHub Code Scanning.
- [x] **Fuzz Testing**: Native Go fuzz test (`FuzzHandler`) for the HTTP router, run for 30 seconds on every CI run.
- [x] **Dependency Updates**: Dependabot opens weekly PRs for Go modules, SHA-pinned GitHub Actions and digest-pinned Docker base images.
- [x] **Static Analysis (CodeQL)**: CodeQL scans the Go code on every push and PR and weekly, reporting to GitHub Code Scanning.
- [x] **Security Policy**: [`SECURITY.md`](SECURITY.md) with private vulnerability reporting.
- [x] **Signed Releases**: GitHub Releases ship SBOMs with keyless Sigstore bundles.
- [x] **Distributed Tracing (OpenTelemetry)**: W3C Trace Context propagation for incoming and outgoing requests, OTLP/HTTP export, Kubernetes resource attributes, and `trace_id`/`span_id` in request logs.
- [x] **Prometheus Metrics (RED)**: Request rate, errors and duration on a separate internal metrics port (`:9090`), kept off the Service and Ingress and restricted by NetworkPolicy, without adding `client_golang`.

---

## Security Info

Found a vulnerability? Please follow [`SECURITY.md`](SECURITY.md) and report it privately instead of opening a public issue.

---

## License

This project is licensed under the MIT License. See [`LICENSE`](LICENSE) for details.
