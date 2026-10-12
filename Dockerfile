# syntax=docker/dockerfile:1

# =========================================================
# Builder stage
# Use lightweight Alpine-based Go 1.24 image
# =========================================================
FROM golang:1.24-alpine AS builder

WORKDIR /src

ENV GOTOOLCHAIN=local

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
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder --chown=65532:65532 /out/server /server

USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/server"]
