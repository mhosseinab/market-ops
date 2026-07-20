# syntax=docker/dockerfile:1.18.0

FROM node:22.22.0-bookworm-slim@sha256:dd9d21971ec4395903fa6143c2b9267d048ae01ca6d3ea96f16cb30df6187d94 AS web-build

WORKDIR /src

RUN corepack enable && corepack prepare pnpm@11.13.1 --activate

COPY package.json pnpm-lock.yaml pnpm-workspace.yaml tsconfig.base.json ./
COPY apps/web/package.json apps/web/package.json
COPY packages/locale/package.json packages/locale/package.json
COPY gen/ts/package.json gen/ts/package.json
# The root prepare hook installs developer Git hooks. The production build has
# no .git directory, so suppress lifecycle scripts during install and then run
# the one native dependency setup the Vite build needs.
RUN --mount=type=cache,target=/pnpm/store \
    pnpm config set store-dir /pnpm/store \
    && pnpm --filter @market-ops/web... install --frozen-lockfile --ignore-scripts \
    && pnpm rebuild esbuild

COPY apps/web/ apps/web/
COPY packages/locale/ packages/locale/
COPY gen/ts/ gen/ts/
COPY runbooks/ runbooks/

ENV VITE_GATEWAY_BASE_URL=/api
RUN pnpm --filter @market-ops/web build

FROM caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d AS caddy-runtime

RUN chown -R 65532:65532 /config /data

# Copying the pinned Caddy filesystem into a fresh final config intentionally
# avoids inheriting upstream OCI labels (including its Apache license label),
# which do not describe this unlicensed repository.
FROM scratch

ARG VERSION=dev
ARG REVISION=none
ARG CREATED=unknown

LABEL org.opencontainers.image.source="https://github.com/mhosseinab/market-ops" \
      org.opencontainers.image.title="market-ops-caddy" \
      org.opencontainers.image.description="TLS ingress and static web application for market-ops" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.created="$CREATED"

ENV XDG_CONFIG_HOME=/config \
    XDG_DATA_HOME=/data

COPY --from=caddy-runtime / /
COPY --chown=65532:65532 deploy/caddy/Caddyfile /etc/caddy/Caddyfile
COPY --chown=65532:65532 --from=web-build /src/apps/web/dist/ /srv/web/

USER 65532:65532
EXPOSE 80 443 443/udp
ENTRYPOINT ["caddy"]
CMD ["run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
