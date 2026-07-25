# syntax=docker/dockerfile:1.18.0
#
# Production schema-migration runner.
#
# The `core` image is distroless/static with a single binary: it carries no
# shell and no goose, and `cmd/core` applies ONLY River's own internal job-queue
# schema at boot (internal/jobs.Migrate) — never the application schema. The 41
# goose migrations under services/core/migrations therefore need their own
# one-shot runner, and this is it.
#
# It is deliberately NOT deploy/migrate.Dockerfile. That image runs
# `task db:reset`, which DROPS and recreates the database and then loads
# development fixtures; it is correct for the disposable integration stack and
# catastrophic against production. This image can only migrate FORWARD: the
# entrypoint is goose itself and the command is `up`, so a stray argument
# cannot silently become `reset` or `down` — an operator must consciously
# override the command to reach either.
#
# The goose version is pinned to the same v3.27.2 the Taskfile installs, so the
# schema a developer applies locally and the schema a deploy applies are
# produced by one tool version.

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build

ARG TARGETARCH
ARG TARGETOS

# Static build: the runtime stage is distroless with no libc. goose's postgres
# driver is pure Go (pgx), so CGO is not needed. GOBIN is set explicitly because
# a cross-compiling `go install` otherwise lands the binary in
# $GOPATH/bin/$GOOS_$GOARCH/ and the COPY below would miss it.
#
# The no_* build tags are LOAD-BEARING, not tidiness. The goose CLI links every
# database driver it supports — ClickHouse, MySQL, MSSQL, SQLite, Vertica,
# Turso/libsql, YDB — and this deployment speaks postgres only. Shipping the
# rest means being scanned on their dependency trees: ydb-go-sdk pulls
# google.golang.org/grpc, whose v1.80.0 (goose's own minimum) carries
# GHSA-hrxh-6v49-42gf HIGH, which release.yml correctly refuses to publish.
# Excluding the unused drivers removes grpc from the binary entirely rather than
# upgrading it, drops the embedded dependency count from 55 to 11, and cuts the
# binary from 57MB to 17MB. Do not "simplify" these away — the build will start
# failing its own vulnerability gate again, and on advisories in databases this
# project never touches.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" GOBIN=/out \
    go install -tags='no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb' \
      github.com/pressly/goose/v3/cmd/goose@v3.27.2

FROM gcr.io/distroless/static-debian12:nonroot@sha256:aef9602f8710ec12bde19d593fed1f76c708531bb7aba205110f1029786ead7b

ARG VERSION=dev
ARG REVISION=none
ARG CREATED=unknown

LABEL org.opencontainers.image.source="https://github.com/mhosseinab/market-ops" \
      org.opencontainers.image.title="market-ops-goose" \
      org.opencontainers.image.description="Forward-only application schema migration runner" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.created="$CREATED"

COPY --from=build /out/goose /goose
COPY services/core/migrations/ /migrations/

# goose reads its connection and directory from the environment (GOOSE_DRIVER,
# GOOSE_DBSTRING, GOOSE_MIGRATION_DIR). That matters here: distroless has no
# shell, so a `postgres "$DATABASE_URL"` argument would never be expanded.
ENV GOOSE_DRIVER=postgres \
    GOOSE_MIGRATION_DIR=/migrations

USER nonroot:nonroot
ENTRYPOINT ["/goose"]
CMD ["up"]
