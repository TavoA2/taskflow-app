# syntax=docker/dockerfile:1

# Builder: full Go toolchain, discarded after the build. Version matches .tool-versions and go.mod.
FROM golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src

# Dependencies first so this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
# Static binary (no libc), reproducible paths, no debug symbols, version stamped in.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/taskflow-api ./cmd/taskflow-api

# Runtime: no shell, no package manager, CA certs and tzdata only.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
COPY --from=build /out/taskflow-api /taskflow-api
# Numeric UID/GID of distroless "nonroot": Kubernetes runAsNonRoot can only verify a numeric user.
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/taskflow-api", "healthcheck"]
ENTRYPOINT ["/taskflow-api"]
CMD ["serve"]
