# syntax=docker/dockerfile:1

# =========================================================
# Builder stage
# Lightweight Alpine-based Go 1.27 image, pinned by digest for reproducible builds.
# Keep the tag in sync with the go directive in go.mod.
# =========================================================
FROM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS builder

WORKDIR /src

ENV GOTOOLCHAIN=local

# Resolve dependencies first so this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=readonly go build \
    -buildvcs=false \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/server .

# =========================================================
# Final runtime stage
# Distroless static base: only the binary, CA certificates, and tzdata.
# Contains no shell, package managers, or standard system utilities, minimizing attack surface.
# =========================================================
FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

# OCI metadata. GHCR uses image.source to link the package to this repository.
LABEL org.opencontainers.image.source="https://github.com/cooler-SAI/go-k8s-secure-service" \
      org.opencontainers.image.description="Hardened Go HTTP service for Kubernetes" \
      org.opencontainers.image.licenses="MIT"

COPY --from=builder --chown=65532:65532 /out/server /server

USER 65532:65532

# 8080: application traffic; 9090: Prometheus /metrics (internal only).
EXPOSE 8080 9090

ENTRYPOINT ["/server"]
