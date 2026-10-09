# syntax=docker/dockerfile:1

# =========================================================
# Builder stage
# Use lightweight Alpine-based Go 1.24 image
# =========================================================
FROM golang:1.24-alpine AS builder

# Set working directory for build
WORKDIR /app

# Copy dependency definition files first for efficient Docker layer caching
# The wildcard pattern go.sum* prevents build failures if go.sum does not exist yet
COPY go.mod go.sum* ./

# Download dependencies (this layer is cached until go.mod/go.sum changes)
RUN go mod download

# Copy application source code
COPY . .

# Compile binary:
# - CGO_ENABLED=0: statically linked binary without C runtime dependencies (required for distroless static)
# - GOOS=linux: target operating system Linux
# - -trimpath: removes host filesystem absolute paths from stack traces and binary metadata
# - -ldflags="-s -w": strips symbol table (-s) and DWARF debugging information (-w) to minimize size
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /app/server .

# =========================================================
# Final runtime stage
# Distroless static base: only the binary, CA certificates, and tzdata.
# Contains no shell, package managers, or standard system utilities, minimizing attack surface.
# =========================================================
FROM gcr.io/distroless/static-debian12:nonroot

# Runtime working directory
WORKDIR /app

# Copy compiled binary from builder stage
COPY --from=builder /app/server /app/server

# Explicitly set non-root user (UID:GID 65532:65532) to satisfy security scanners (Trivy, Hadolint)
USER nonroot:nonroot

# Expose service HTTP port
EXPOSE 8080

# Run binary directly in JSON exec form without a shell
# Ensures process runs as PID 1 and correctly handles SIGTERM/SIGINT signals for graceful shutdown
ENTRYPOINT ["/app/server"]
