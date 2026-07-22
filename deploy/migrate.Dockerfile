FROM golang:1.26

RUN apt-get update -qq && \
    apt-get install -y -qq --no-install-recommends postgresql-client curl >/dev/null && \
    rm -rf /var/lib/apt/lists/*

RUN mkdir -p /usr/local/bin && \
    curl -sSL https://taskfile.dev/install.sh | sh -s -- -b /usr/local/bin
