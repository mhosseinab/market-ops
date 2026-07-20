# syntax=docker/dockerfile:1.18.0

FROM --platform=$BUILDPLATFORM golang:1.25.12-bookworm@sha256:ea341baa9bd5ba6784f6d7161ace70544349a6242d54d34a0fbfd2c4d51c9d58 AS build

ARG TARGETARCH
ARG TARGETOS
ARG VERSION=dev
ARG REVISION=none
ARG CREATED=unknown

WORKDIR /src/services/core

# The core module uses local replacements for both generated Go clients. Copy
# their module manifests before the source so dependency download remains a
# stable cache layer.
COPY services/core/go.mod services/core/go.sum ./
COPY gen/go/go.mod /src/gen/go/go.mod
COPY gen/dkgo/go.mod /src/gen/dkgo/go.mod
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY services/core/ ./
COPY gen/go/ /src/gen/go/
COPY gen/dkgo/ /src/gen/dkgo/

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -buildvcs=false -trimpath \
      -ldflags="-s -w -X main.version=$VERSION -X main.commit=$REVISION -X main.buildTime=$CREATED" \
      -o /out/market-ops-core ./cmd/core

FROM gcr.io/distroless/static-debian12:nonroot@sha256:aef9602f8710ec12bde19d593fed1f76c708531bb7aba205110f1029786ead7b

ARG VERSION=dev
ARG REVISION=none
ARG CREATED=unknown

LABEL org.opencontainers.image.source="https://github.com/mhosseinab/market-ops" \
      org.opencontainers.image.title="market-ops-core" \
      org.opencontainers.image.description="Deterministic market operations gateway and domain core" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.created="$CREATED"

COPY --from=build /out/market-ops-core /market-ops-core

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/market-ops-core"]
