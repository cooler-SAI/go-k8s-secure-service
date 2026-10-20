# syntax=docker/dockerfile:1

# =========================================================
# Builder stage
# Lightweight Alpine-based Go 1.27 image, pinned by digest for reproducible builds.
# Keep the tag in sync with the go directive in go.mod.
# =========================================================
FROM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS builder

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
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=builder --chown=65532:65532 /out/server /server

USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/server"]
