# go-k8s-secure-service

A lightweight, secure Go HTTP service designed for containerized deployment and orchestration in Kubernetes environments.

---

## Overview

`go-k8s-secure-service` serves as a foundation for building resilient, cloud-native microservices in Go. It provides standard HTTP endpoints, built-in Kubernetes health check probes (`/livez` and `/readyz`), graceful shutdown handling via `SIGTERM`, and an enterprise-grade security baseline for containerized environments.

## Features

- **Root Endpoint (`/`)**: Returns a basic greeting (`Hello, World!`) with HTTP status 200 OK. Unknown paths return 404 Not Found.
- **Method Restriction**: All endpoints accept only `GET` (and `HEAD`); other methods receive `405 Method Not Allowed` with an `Allow` header.
- **Kubernetes Liveness Probe (`/livez`)**: Provides a dedicated health probe endpoint for orchestrators (e.g., Kubernetes `livenessProbe`) to monitor service vitality.
- **Kubernetes Readiness Probe (`/readyz`)**: Signals service readiness to receive incoming traffic (Kubernetes `readinessProbe`). Automatically switches to `503 Service Unavailable` on shutdown signals to immediately stop traffic ingress before connection draining completes.
- **Graceful Shutdown (`SIGTERM` / `SIGINT`)**: Listens for termination signals from Kubernetes (`SIGTERM`) or process interruptions (`SIGINT`), reporting not-ready and continuing to serve for a 5-second drain delay while endpoint removal propagates, then running a graceful shutdown (`srv.Shutdown()`) with a 10-second window for in-flight requests.
- **Hardened HTTP Timeouts**: Explicitly configures `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` to mitigate Slowloris and connection starvation attacks.
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
│   ├── deployment.yaml # Hardened Deployment (security context, probes, resources)
│   ├── service.yaml    # ClusterIP Service exposing the HTTP port
│   └── pdb.yaml        # PodDisruptionBudget keeping a replica up during disruptions
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

3. The server will start listening on port `:8080`:
   ```text
   2026/10/06 01:24:31 Server starting on :8080...
   ```

### Configuration and Environment Variables

The service currently uses fixed settings defined in `main.go`; it does not read
configuration from environment variables or command-line flags.

| Setting | Current value | Description |
| --- | --- | --- |
| Listen address | `:8080` | Binds the HTTP server to port 8080 on all network interfaces. |
| Read-header timeout | 5 seconds | Limits the time allowed to read request headers. |
| Read timeout | 10 seconds | Limits the time allowed to read the full request. |
| Write timeout | 10 seconds | Limits the time allowed to write a response. |
| Idle timeout | 60 seconds | Limits how long an idle keep-alive connection remains open. |
| Shutdown drain delay | 5 seconds | Time the server keeps serving (while `/readyz` reports 503) after a termination signal, before closing the listener. |
| Shutdown timeout | 10 seconds | Maximum time allowed for in-flight requests to finish once the listener is closed. |

To change these settings, update the corresponding server configuration in
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
- [ ] **Structured Logging**: Replace standard logging with structured JSON logging (`slog`).
- [x] **Containerization & Hardening**: Multi-stage distroless image, static binary, non-root runtime user, and hardened Kubernetes `SecurityContext` example.
- [ ] **Network Policy**: Add a least-privilege Kubernetes `NetworkPolicy` example.

---

## License

This project is licensed under the MIT License. See [`LICENSE`](LICENSE) for details.
