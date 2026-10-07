# go-k8s-secure-service

A lightweight, secure Go HTTP service designed for containerized deployment and orchestration in Kubernetes environments.

---

## Overview

`go-k8s-secure-service` serves as a foundation for building resilient, cloud-native microservices in Go. It provides standard HTTP endpoints, built-in Kubernetes health check probes (`/livez` and `/readyz`), graceful shutdown handling via `SIGTERM`, and an enterprise-grade security baseline for containerized environments.

## Features

- **Root Endpoint (`/`)**: Returns a basic greeting (`Hello, World!`) with HTTP status 200 OK. Unknown paths return 404 Not Found.
- **Kubernetes Liveness Probe (`/livez`)**: Provides a dedicated health probe endpoint for orchestrators (e.g., Kubernetes `livenessProbe`) to monitor service vitality.
- **Kubernetes Readiness Probe (`/readyz`)**: Signals service readiness to receive incoming traffic (Kubernetes `readinessProbe`). Automatically switches to `503 Service Unavailable` on shutdown signals to immediately stop traffic ingress before connection draining completes.
- **Graceful Shutdown (`SIGTERM` / `SIGINT`)**: Listens for termination signals from Kubernetes (`SIGTERM`) or process interruptions (`SIGINT`), initiating a non-blocking graceful shutdown (`srv.Shutdown()`) with a 10-second drain window for in-flight requests.
- **Hardened HTTP Timeouts**: Explicitly configures `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` to mitigate Slowloris and connection starvation attacks.
- **Comprehensive Unit Tests**: High-coverage test suite verifying handlers, routing, server timeout settings, and shutdown lifecycle.

---

## Project Structure

```text
go-k8s-secure-service/
├── .idea/              # IDE configuration
├── .gitignore          # Git ignore rules
├── go.mod              # Go module definition and dependencies
├── main.go             # Application entry point, server configuration, and HTTP handlers
├── main_test.go        # Unit and integration test suite
└── README.md           # Project documentation
```

---

## Getting Started

### Prerequisites

- [Go](https://golang.org/dl/) (1.20+ recommended)
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
| Shutdown drain window | 10 seconds | Maximum time allowed for in-flight requests to finish after a termination signal. |

To change these settings, update the corresponding server configuration in
`main.go`. The Kubernetes container port and probe ports should match the
configured listen port.

### Running Tests

Execute the unit test suite with coverage report:

```bash
go test -v -cover ./...
```

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
2. `go-k8s-secure-service` catches `SIGTERM` and immediately flips `/readyz` to `503 Service Unavailable` (`Not Ready`). This prompts Kubernetes to promptly remove the pod from Service endpoints and ingress routing.
3. Simultaneously, `http.Server.Shutdown(shutdownCtx)` is triggered, preventing new TCP connections while granting in-flight requests up to 10 seconds to finish processing cleanly.
4. The process exits cleanly with code 0 once all active connections drain.

### Deployment Manifest Example

You can deploy the service and configure both the `/livez` and `/readyz` probes in your Deployment manifest:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: go-k8s-secure-service
  labels:
    app: go-k8s-secure-service
spec:
  replicas: 2
  selector:
    matchLabels:
      app: go-k8s-secure-service
  template:
    metadata:
      labels:
        app: go-k8s-secure-service
    spec:
      terminationGracePeriodSeconds: 30
      containers:
        - name: go-k8s-secure-service
          image: go-k8s-secure-service:latest
          ports:
            - containerPort: 8080
          livenessProbe:
            httpGet:
              path: /livez
              port: 8080
            initialDelaySeconds: 3
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /readyz
              port: 8080
            initialDelaySeconds: 2
            periodSeconds: 5
```

---

## Roadmap & Security Hardening

- [x] **Go Modules**: Initialize `go.mod` for dependency management and version reproducibility.
- [x] **Graceful Shutdown**: Implement clean signal handling (`SIGINT`, `SIGTERM`) using `context` to prevent dropped in-flight requests during rolling updates.
- [x] **HTTP Timeouts**: Configure explicit `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` on `http.Server` to mitigate Slowloris / denial-of-service attacks.
- [x] **Automated Testing & CI/CD**: Unit testing with `net/http/httptest`.
- [x] **Readiness Probe**: Add `/readyz` for traffic readiness verification.
- [ ] **Structured Logging**: Replace standard logging with structured JSON logging (`slog`).
- [ ] **Containerization & Hardening**:
  - Multi-stage Dockerfile based on `distroless` or `scratch`.
  - Non-root user execution (`runAsNonRoot: true`, `readOnlyRootFilesystem: true`).
  - Kubernetes `SecurityContext` and `NetworkPolicy` profiles.

---

## License

This project is licensed under the MIT License.
