# go-k8s-secure-service

[![CI](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/ci.yml/badge.svg)](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/ci.yml)
[![Security Scan (Trivy)](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/trivy.yml/badge.svg)](https://github.com/cooler-SAI/go-k8s-secure-service/actions/workflows/trivy.yml)
![Go Version](https://img.shields.io/badge/Go-1.27.2-00ADD8?logo=go)
![Tests](https://img.shields.io/badge/tests-passing-brightgreen)
![Coverage](https://img.shields.io/badge/coverage-70.3%25-brightgreen)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A lightweight, secure Go HTTP service designed for containerized deployment and orchestration in Kubernetes environments.

---

## Project Status

| Metric / Aspect | Status | Details |
| :--- | :--- | :--- |
| **Development Lifecycle** | **Stable / Production-Ready Baseline** | Core microservice architecture, Kubernetes orchestration manifests, and security hardening are complete. |
| **Build & Test Suite** | **Passing (100%)** | Unit and integration tests verify handlers, routing, server timeout settings, and shutdown lifecycle with **70.3%** statement coverage. Race detection runs in CI. |
| **Security Scanning** | **Verified (Trivy SARIF)** | Automated weekly and PR/push scans covering repository dependencies, container image, secret detection, and IaC/Kubernetes configurations (`CRITICAL,HIGH` threshold). |
| **Dependencies** | **Minimal (one direct)** | Built on the Go standard library (`net/http`, `os/signal`, `sync/atomic`) plus a single direct dependency, [`zerolog`](https://github.com/rs/zerolog), for structured logging. Dependencies are scanned by Trivy. |
| **Container Hardening** | **Distroless Non-Root (`65532:65532`)** | Multi-stage build (`golang:1.27.2-alpine` builder) deploying to `gcr.io/distroless/static-debian12:nonroot` with read-only rootfs and dropped Linux capabilities. |
| **Kubernetes Compliance** | **Production Baseline Manifests** | Hardened `Deployment` (probes, non-root `SecurityContext`, `GOMEMLIMIT`), `ClusterIP Service`, `PodDisruptionBudget`, and least-privilege `NetworkPolicy` (default deny egress, restricted ingress). |
| **CI/CD Automation** | **Active (GitHub Actions)** | Continuous integration enforcing formatting (`gofmt`), code vet (`go vet`), test suite with race detector, static binary build verification, and Trivy security scanning. |

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

## Project Structure

```text
go-k8s-secure-service/
├── .github/workflows/
│   ├── ci.yml          # gofmt, vet, race-enabled tests, and build
│   └── trivy.yml       # Trivy repo and container image security scans
├── .idea/              # IDE configuration
├── k8s/
│   ├── deployment.yaml    # Hardened Deployment (security context, probes, resources)
│   ├── service.yaml       # ClusterIP Service exposing the HTTP port
│   ├── pdb.yaml           # PodDisruptionBudget keeping a replica up during disruptions
│   └── networkpolicy.yaml # Default-deny NetworkPolicy allowing only HTTP ingress
├── .aiignore           # AI tool ignore rules
├── .gitignore          # Git ignore rules
├── .dockerignore       # Excludes development files from the build context
├── Dockerfile          # Multi-stage hardened container build
├── go.mod              # Go module definition and dependencies
├── LICENSE             # MIT License
├── main.go             # Application entry point, server configuration, and HTTP handlers
├── main_test.go        # Unit and integration test suite
├── trivy.yaml          # Shared Trivy scanner configuration
└── README.md           # Project documentation
```

---

## Getting Started

### Prerequisites

- [Go](https://go.dev/dl/) 1.27.2 or later
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
   {"time":"2026-10-16T01:42:19.704+03:00","level":"INFO","msg":"server starting","addr":"[::]:8080"}
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
- [`policies/`](k8s/policies/): Kyverno `ClusterPolicy` rules that deny
  containers running as `runAsUser: 0`, deny privileged containers, and
  require liveness/readiness probes and CPU/memory limits. System namespaces
  are excluded. They are not applied by `kubectl apply -f k8s/`; install
  Kyverno and apply them with `kubectl apply -f k8s/policies/`. Policy
  tests (`kyverno test k8s/policies/tests`) run offline against fixture pods
  and `deployment.yaml`, and CI runs them on every push and PR.
  `verify-image-signature.yaml` additionally requires images from
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
  long-lived keys), and attaches both SBOMs as signed attestations.
- **Verify a signature manually**:
  ```bash
  cosign verify ghcr.io/cooler-sai/go-k8s-secure-service:v1.0.0 \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity-regexp '^https://github\.com/cooler-SAI/go-k8s-secure-service/\.github/workflows/release\.yml@refs/tags/v.*$'
  ```
- **Cluster enforcement**: the `verify-image-signature` Kyverno policy applies
  the same identity check at admission time.

Make the `go-k8s-secure-service:v1.0.0` image available to your cluster nodes
or update the manifest to use an image in your container registry, then apply
everything:

```bash
kubectl apply -f k8s/
```

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
- [x] **Admission Policies (Kyverno)**: `ClusterPolicy` rules for non-root, non-privileged containers, mandatory probes and resource limits, validated offline in CI with `kyverno test`.
- [x] **SBOM & Image Signing (Cosign)**: Trivy-generated SPDX/CycloneDX SBOMs in CI, keyless Cosign signing and SBOM attestations on release, and a Kyverno policy that verifies the signature at admission.

---

## License

This project is licensed under the MIT License. See [`LICENSE`](LICENSE) for details.
