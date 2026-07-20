# syntax=docker/dockerfile:1.18.0

FROM python:3.13.14-alpine3.24@sha256:399babc8b49529dabfd9c922f2b5eea81d611e4512e3ed250d75bd2e7683f4b0 AS build

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
    uv sync --frozen --no-dev --package market-ops-llm --no-editable

FROM python:3.13.14-alpine3.24@sha256:399babc8b49529dabfd9c922f2b5eea81d611e4512e3ed250d75bd2e7683f4b0

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

COPY --from=build --chown=65532:65532 /opt/venv /opt/venv

USER 65532:65532
EXPOSE 8100
ENTRYPOINT ["/opt/venv/bin/uvicorn"]
CMD ["llm.asgi:app", "--host", "0.0.0.0", "--port", "8100"]
