# go-k8s-secure-service

[![CI](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/ci.yml/badge.svg)](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/ci.yml)
[![Security Scan (Trivy)](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/trivy.yml/badge.svg)](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/trivy.yml)
![Go Version](https://img.shields.io/badge/Go-1.27.3-00ADD8?logo=go)
![Tests](https://img.shields.io/badge/tests-passing-brightgreen)
![Coverage](https://img.shields.io/badge/coverage-67.2%25-brightgreen)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/cooler-SAI/go-k8s-secure-service/badge)](https://scorecard.dev/viewer/?uri=github.com/cooler-SAI/go-k8s-secure-service)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A lightweight, secure Go HTTP service designed for containerized deployment and orchestration in Kubernetes environments.

**Contents:** [Status](#project-status) · [Overview](#overview) · [HTTP API](#http-api) · [Getting Started](#getting-started) · [Logging](#logging) · [Kubernetes](#kubernetes-integration) · [CI/CD](#cicd-pipelines) · [Troubleshooting](#troubleshooting) · [Roadmap](#roadmap--security-hardening) · [Security](#security)

---

## Project Status

| Metric / Aspect | Status | Details |
| :--- | :--- | :--- |
| **Development Lifecycle** | **Stable / Production-Ready Baseline** | Core microservice architecture, Kubernetes orchestration manifests, and security hardening are complete. |
| **Build & Test Suite** | **Passing (100%)** | Unit and integration tests verify handlers, routing, server timeout settings, and shutdown lifecycle with **67.2%** statement coverage. Race detection runs in CI. |
| **Security Scanning** | **Verified (Trivy SARIF)** | Automated weekly and PR/push scans (Trivy, plus CodeQL static analysis of the Go code) covering repository dependencies, container image, secret detection, and IaC/Kubernetes configurations (`CRITICAL,HIGH` threshold). |
| **Dependencies** | **Minimal (one direct)** | Built on the Go standard library (`net/http`, `os/signal`, `sync/atomic`) plus a single direct dependency, [`zerolog`](https://github.com/rs/zerolog), for structured logging. Dependencies are scanned by Trivy. |
| **Container Hardening** | **Distroless Non-Root (`65532:65532`)** | Multi-stage build (`golang:1.27.3-alpine` builder) deploying to `gcr.io/distroless/static-debian12:nonroot` with read-only rootfs and dropped Linux capabilities. |
| **Kubernetes Compliance** | **Production Baseline Manifests** | Hardened `Deployment` (probes, non-root `SecurityContext`, `GOMEMLIMIT`), `ClusterIP Service`, `PodDisruptionBudget`, least-privilege `NetworkPolicy` (default deny egress, restricted ingress), and CEL-based Kyverno `ValidatingPolicy`/`ImageValidatingPolicy` rules (non-root, non-privileged, probes, resource limits, image signature). |
| **CI/CD Automation** | **Active (GitHub Actions)** | Continuous integration enforcing formatting (`gofmt`), code vet (`go vet`), test suite with race detector, static binary build verification, offline Kyverno policy tests, a kind E2E smoke test under the `restricted` Pod Security profile with Kyverno enforcing (including live Cosign signature verification), and Trivy security scanning. |
| **Supply Chain** | **SBOM + Signed Releases** | Trivy-generated SPDX/CycloneDX SBOMs, and tagged releases pushed to GHCR with keyless Cosign signatures and SBOM attestations, verified at admission by Kyverno. |

---

## Overview

`go-k8s-secure-service` serves as a foundation for building resilient, cloud-native microservices in Go. It provides standard HTTP endpoints, built-in Kubernetes health check probes (`/livez` and `/readyz`), graceful shutdown handling via `SIGTERM`, and an enterprise-grade security baseline for containerized environments.

## Features

- **Root Endpoint (`/`)**: Returns a basic greeting (`Hello, World!`) with HTTP status 200 OK. Unknown paths return 404 Not Found.
- **Method Restriction**: All endpoints accept only `GET` (and `HEAD`); other methods receive `405 Method Not Allowed` with an `Allow` header.
- **Configurable Listen Port**: Reads the standard `PORT` environment variable (e.g., `PORT=8080` or `PORT=:8080`) to bind to custom ports, defaulting to `:8080` for container and Kubernetes environments.
- **Kubernetes Liveness Probe (`/livez`)**: Provides a dedicated health probe endpoint for orchestrators (e.g., Kubernetes `livenessProbe`) to monitor service vitality.
- **Kubernetes Readiness Probe (`/readyz`)**: Signals service readiness to receive incoming traffic (Kubernetes `readinessProbe`). Automatically switches to `503 Service Unavailable` on shutdown signals to immediately stop traffic ingress before connection draining completes.
- **Graceful Shutdown (`SIGTERM` / `SIGINT`)**: Listens for termination signals from Kubernetes (`SIGTERM`) or process interruptions (`SIGINT`), reporting not-ready and continuing to serve for a 5-second drain delay while endpoint removal propagates, then running a graceful shutdown (`srv.Shutdown()`) with a 10-second window for in-flight requests.
- **Hardened HTTP Timeouts**: Explicitly configures `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` to mitigate Slowloris and connection starvation attacks.
- **Structured Logging**: Emits JSON logs to stdout with `zerolog`, ready for container log collectors (Loki, Fluent Bit, Cloud Logging, etc.). `net/http` internal errors go through the same handler.
- **Hardened Container Image**: Uses a multi-stage build and a minimal distroless runtime, runs as a non-root user, and builds a static binary.
- **Comprehensive Unit Tests**: High-coverage test suite verifying handlers, routing, server timeout settings, and shutdown lifecycle.

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

---

## Project Structure

```text
go-k8s-secure-service/
├── .github/
│   └── dependabot.yml  # Weekly updates for Go modules, Actions and Docker images
├── .github/workflows/
│   ├── ci.yml          # gofmt, vet, race-enabled tests, build, Kyverno policy tests, kind E2E
│   ├── trivy.yml       # Trivy repo and container image scans, SBOM generation
│   ├── scorecard.yml   # OpenSSF Scorecard supply-chain analysis
│   ├── codeql.yml      # CodeQL static analysis (Go)
│   └── release.yml     # Tag release: GHCR push, SBOMs, keyless Cosign signing
├── .idea/              # IDE configuration
├── e2e/
│   ├── namespace.yaml     # Namespace enforcing the restricted Pod Security profile
│   ├── smoke.sh           # Deploys to a cluster and checks endpoints via port-forward
│   └── admission.sh       # Live Kyverno admission checks, incl. Cosign signature verification
├── k8s/
│   ├── deployment.yaml    # Hardened Deployment (security context, probes, resources)
│   ├── service.yaml       # ClusterIP Service exposing the HTTP port
│   ├── pdb.yaml           # PodDisruptionBudget keeping a replica up during disruptions
│   ├── networkpolicy.yaml # Default-deny NetworkPolicy allowing only HTTP ingress
│   └── policies/          # Kyverno ClusterPolicies and offline tests (tests/)
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
   go run main.go
   ```

3. The server will start listening on port `:8080` (or the port specified by `$PORT`):
   ```text
   {"level":"info","addr":"[::]:8080","time":"2026-10-16T01:42:19+03:00","message":"server starting"}
   ```

### Configuration and Environment Variables

The service supports configuring the listen port through the standard `PORT` environment variable. Operational timeouts and shutdown intervals use production-tuned defaults defined in `main.go`.

| Setting | Configuration | Default | Description |
| --- | --- | --- | --- |
| Listen port | `PORT` env var | `:8080` | Binds the HTTP server to the specified port on all network interfaces (e.g., `PORT=8080` or `PORT=:8080`). |
| Read-header timeout | Fixed (`main.go`) | 5 seconds | Limits the time allowed to read request headers. |
| Read timeout | Fixed (`main.go`) | 10 seconds | Limits the time allowed to read the full request. |
| Write timeout | Fixed (`main.go`) | 10 seconds | Limits the time allowed to write a response. |
| Idle timeout | Fixed (`main.go`) | 60 seconds | Limits how long an idle keep-alive connection remains open. |
| Shutdown drain delay | Fixed (`main.go`) | 5 seconds | Time the server keeps serving (while `/readyz` reports 503) after a termination signal, before closing the listener. |
| Shutdown timeout | Fixed (`main.go`) | 10 seconds | Maximum time allowed for in-flight requests to finish once the listener is closed. |

To change the timeout settings, update the corresponding server configuration in
`main.go`. The Kubernetes container port and probe ports should match the
configured listen port.

### Running Tests

Execute the unit test suite with coverage report:

```bash
go test -v -cover ./...
```

The router is also covered by a native Go fuzz test (`FuzzHandler`) that sends
arbitrary methods and request targets through `net/http`'s request parser into
the router and asserts that it never panics, never answers with a 5xx, and
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

---

## Logging

The service writes one JSON object per line to stdout using
[`zerolog`](https://github.com/rs/zerolog). Each line has `level`, `time`
(RFC 3339) and `message`, plus event-specific fields. A normal start and
`SIGTERM` shutdown looks like this:

```text
{"level":"info","addr":"[::]:8080","time":"2026-10-16T01:42:19+03:00","message":"server starting"}
{"level":"info","drain_delay":"5s","time":"2026-10-16T01:45:02+03:00","message":"shutdown signal received, reporting not-ready and draining"}
{"level":"info","shutdown_timeout":"10s","time":"2026-10-16T01:45:07+03:00","message":"drain delay elapsed, shutting down HTTP server"}
{"level":"info","time":"2026-10-16T01:45:07+03:00","message":"server gracefully stopped"}
```

Errors are logged at `level: "error"` with an `error` field. Internal
`net/http` errors carry `"component":"net/http"`. If the server cannot start
or shut down cleanly, it logs `server terminated with error` and exits with
code `1`.

To follow logs in a cluster, run `kubectl logs -f deploy/go-k8s-secure-service`.

---

## Kubernetes Integration

### Pod Lifecycle, Probes, and SIGTERM Handling

When Kubernetes scales down a deployment, performs a rolling update, or terminates a pod:
1. Kubernetes sends a `SIGTERM` signal to the container's PID 1 process.
2. In parallel, Kubernetes starts removing the pod from Service endpoints. That removal reaches kube-proxy, load balancers, and ingress controllers asynchronously, so new requests can still arrive for a few seconds.
3. `go-k8s-secure-service` catches `SIGTERM`, flips `/readyz` to `503 Service Unavailable` (`Not Ready`), and **keeps serving for a 5-second drain delay** so requests still being routed to the pod are handled instead of refused.
4. After the delay, `http.Server.Shutdown(shutdownCtx)` closes the listener and grants in-flight requests up to 10 seconds to finish processing cleanly.
5. The process exits cleanly with code 0 once all active connections drain.

The drain delay (5s) plus the shutdown timeout (10s) must stay below the pod's
`terminationGracePeriodSeconds` (30s in `k8s/deployment.yaml`); otherwise the
kubelet sends `SIGKILL` before draining finishes.

### Kubernetes Manifests

The manifests live in [`k8s/`](k8s/):

- [`deployment.yaml`](k8s/deployment.yaml): runs two replicas as UID/GID
  `65532`, disables service-account token mounting, uses a read-only root
  filesystem, drops Linux capabilities, sets resource requests and limits,
  configures the `/livez` and `/readyz` probes, and sets `GOMEMLIMIT` to ~90%
  of the memory limit so the Go GC works harder before the container is
  OOM-killed.
- [`service.yaml`](k8s/service.yaml): a `ClusterIP` Service routing port `80`
  to the container's named `http` port (`8080`).
- [`pdb.yaml`](k8s/pdb.yaml): a `PodDisruptionBudget` with `minAvailable: 1`,
  so node drains and cluster upgrades never evict both replicas at once.
- [`networkpolicy.yaml`](k8s/networkpolicy.yaml): default-deny for the
  service's pods. Ingress is allowed only on the `http` port, from pods in the
  same namespace and from the `ingress-nginx` namespace (change this to match
  your ingress controller or Gateway). All egress is denied, since the service
  makes no outbound calls. NetworkPolicy is enforced only if your CNI supports
  it (e.g. Calico, Cilium).
- [`policies/`](k8s/policies/): CEL-based Kyverno policies
  (`policies.kyverno.io/v1`, Kyverno 1.19+). Four `ValidatingPolicy`
  resources deny containers running as `runAsUser: 0`, deny privileged
  containers, and require liveness/readiness probes and CPU/memory limits.
  Kyverno auto-generates the same checks for Deployments, StatefulSets,
  DaemonSets, ReplicaSets, Jobs and CronJobs. System namespaces are
  excluded. They are not applied by `kubectl apply -f k8s/`; install
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
  cosign verify ghcr.io/cooler-sai/go-k8s-secure-service:v1.0.0 \
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
(405) through `kubectl port-forward`. CI runs it on a single-node
[kind](https://kind.sigs.k8s.io/) cluster on every push and PR, with Kyverno
and the policies in [`k8s/policies/`](k8s/policies/) installed, so the
Deployment is also admitted under them.

[`e2e/admission.sh`](e2e/admission.sh) then checks the policies against the
live cluster with server-side dry-runs:

- the Deployment with a signed release image from GHCR is admitted, which
  verifies its Cosign signature against Sigstore;
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
  server=ghcr.io/cooler-sai/go-k8s-secure-service:v1.0.0
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
| [`ci.yml`](.github/workflows/ci.yml) | push / PR to `main` | `gofmt` check, `go mod verify`, `go vet`, race-enabled tests with coverage, 30s of `FuzzHandler`, static build; Kyverno policy tests; kind E2E smoke test and live Kyverno admission test |
| [`trivy.yml`](.github/workflows/trivy.yml) | push / PR to `main`, weekly (Mon 06:00 UTC) | Repo scan (deps, IaC, secrets) and image scan with SARIF upload to Code Scanning; SPDX + CycloneDX SBOM artifact |
| [`codeql.yml`](.github/workflows/codeql.yml) | push / PR to `main`, weekly (Wed 07:00 UTC) | CodeQL static analysis of the Go code |
| [`scorecard.yml`](.github/workflows/scorecard.yml) | push to `main`, branch protection changes, weekly (Tue 06:30 UTC) | OpenSSF Scorecard supply-chain checks |
| [`release.yml`](.github/workflows/release.yml) | push of a `v*` tag | Push image to GHCR, keyless Cosign signature, SBOM attestations, GitHub Release with signed SBOMs |

Dependabot ([`.github/dependabot.yml`](.github/dependabot.yml)) opens weekly
update PRs for Go modules, GitHub Actions and Docker base images. All actions
and base images are pinned by SHA/digest.

To cut a release:

```bash
git tag v1.0.1
git push origin v1.0.1
```

---

## Troubleshooting

| Symptom | Likely cause and fix |
| :--- | :--- |
| `ErrImagePull` / `ImagePullBackOff` on kind or a local cluster | The cluster nodes don't have the local image. Run `kind load docker-image go-k8s-secure-service:v1.0.0 --name <cluster>`, or deploy the GHCR image ([above](#deploying-a-signed-release-image)). |
| Pods rejected by admission with a Kyverno message | A manifest change broke a policy (root user, privileged, missing probes or limits), or a GHCR image is unsigned. Run `kyverno test k8s/policies/tests` locally to reproduce. |
| Service unreachable from another namespace | The NetworkPolicy allows ingress only from the same namespace and `ingress-nginx`. Add your client or ingress namespace to `k8s/networkpolicy.yaml`. |
| `/readyz` returns `503 Not Ready` | Expected while the pod drains after `SIGTERM`. Kubernetes removes the pod from endpoints. |
| Pod `OOMKilled` | The 64Mi memory limit is too low for your load. Raise `resources.limits.memory` and keep `GOMEMLIMIT` at ~90% of it. |
| Pod `SIGKILL`ed before draining finishes | `terminationGracePeriodSeconds` must exceed drain delay + shutdown timeout (5s + 10s). |
| `failed to listen on :8080` | The port is already in use. Set another one, e.g. `PORT=9090 go run main.go`, and update the container port and probes to match. |

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

---

## Security

Found a vulnerability? Please follow [`SECURITY.md`](SECURITY.md) and report it privately instead of opening a public issue.

---

## License

This project is licensed under the MIT License. See [`LICENSE`](LICENSE) for details.
