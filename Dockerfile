# syntax=docker/dockerfile:1

# =========================================================
# Builder stage
# Lightweight Alpine-based Go 1.27 image, pinned by digest for reproducible builds.
# Keep the tag in sync with the go directive in go.mod.
# =========================================================
FROM golang:1.27.3-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS builder

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
FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99

COPY --from=builder --chown=65532:65532 /out/server /server

USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/server"]
