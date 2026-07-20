# syntax=docker/dockerfile:1.18.0

FROM python:3.13.11-slim-bookworm@sha256:20080e807bfc404f8450b185cf0fc95d553462673598549613735f70a5b4d5d0 AS build

ENV UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy \
    UV_PROJECT_ENVIRONMENT=/opt/venv

WORKDIR /app

# Keep uv itself pinned and install the LLM workspace package non-editably. The
# generated gateway client is a local workspace dependency, so both package
# trees are present before the frozen sync.
RUN pip install --no-cache-dir uv==0.8.15
COPY pyproject.toml uv.lock ./
COPY services/llm/ services/llm/
COPY gen/python/ gen/python/
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --frozen --no-dev --package llm --no-editable

FROM python:3.13.11-slim-bookworm@sha256:20080e807bfc404f8450b185cf0fc95d553462673598549613735f70a5b4d5d0

ARG VERSION=dev
ARG REVISION=none
ARG CREATED=unknown

LABEL org.opencontainers.image.source="https://github.com/mhosseinab/market-ops" \
      org.opencontainers.image.title="market-ops-llm" \
      org.opencontainers.image.description="Read and Draft-only market operations LLM plane" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.created="$CREATED"

ENV PATH="/opt/venv/bin:$PATH" \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

RUN groupadd --gid 65532 marketops \
    && useradd --uid 65532 --gid 65532 --no-create-home --home-dir /nonexistent marketops

COPY --from=build --chown=65532:65532 /opt/venv /opt/venv

USER 65532:65532
EXPOSE 8100
ENTRYPOINT ["/opt/venv/bin/uvicorn"]
CMD ["llm.asgi:app", "--host", "0.0.0.0", "--port", "8100"]
