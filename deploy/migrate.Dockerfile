FROM golang:1.25

RUN apt-get update -qq && \
    apt-get install -y -qq --no-install-recommends postgresql-client curl >/dev/null && \
    rm -rf /var/lib/apt/lists/*

RUN mkdir -p /tmp/gobin
ENV PATH="/tmp/gobin:${PATH}"
ENV GOBIN="/tmp/gobin"

RUN go install github.com/pressly/goose/v3/cmd/goose@v3.27.2 && \
    go install github.com/riverqueue/river/cmd/river@v0.40.0 && \
    curl -sSL https://taskfile.dev/install.sh | sh -s -- -b /tmp/gobin
