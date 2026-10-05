# go-k8s-secure-service

A lightweight, secure Go HTTP service designed for containerized deployment and orchestration in Kubernetes environments.

---

## Overview

`go-k8s-secure-service` serves as a foundation for building resilient, cloud-native microservices in Go. It provides standard HTTP endpoints, built-in Kubernetes health check probes, and a clean baseline for applying enterprise security best practices in containerized clusters.

## Features

- **Root Endpoint (`/`)**: Returns a basic greeting (`Hello, World!`) with HTTP status 200 OK.
- **Kubernetes Liveness Probe (`/livez`)**: Provides a dedicated health probe endpoint for orchestrators (e.g., Kubernetes `livenessProbe`) to monitor service vitality.
- **Standard HTTP Server**: Listens on port `:8080` with standard error logging on startup and request handling.

---

## Project Structure

```text
go-k8s-secure-service/
├── .idea/              # IDE configuration
├── .gitignore          # Git ignore rules
├── go.mod              # Go module definition and dependencies
├── main.go             # Application entry point and HTTP handlers
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
   Server starting on :8080...
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

---

## Kubernetes Integration

### Liveness Probe Example

You can configure Kubernetes to probe the `/livez` endpoint in your Deployment manifest:

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
```

---

## Roadmap & Security Hardening

- [x] **Go Modules**: Initialize `go.mod` for dependency management and version reproducibility.
- [ ] **Readiness Probe**: Add `/readyz` for traffic readiness verification.
- [ ] **Graceful Shutdown**: Implement clean signal handling (`SIGINT`, `SIGTERM`) using `context` to prevent dropped in-flight requests during rolling updates.
- [ ] **HTTP Timeouts**: Configure explicit `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` on `http.Server` to mitigate Slowloris / denial-of-service attacks.
- [ ] **Structured Logging**: Replace standard `fmt` output with structured JSON logging (`slog`).
- [ ] **Containerization & Hardening**:
  - Multi-stage Dockerfile based on `distroless` or `scratch`.
  - Non-root user execution (`runAsNonRoot: true`, `readOnlyRootFilesystem: true`).
  - Kubernetes `SecurityContext` and `NetworkPolicy` profiles.
- [ ] **Automated Testing & CI/CD**: Unit testing with `net/http/httptest` and GitHub Actions workflow.

---

## License

This project is licensed under the MIT License.
