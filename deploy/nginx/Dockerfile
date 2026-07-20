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

FROM nginx:1.30.4-alpine-slim@sha256:ddde39c6e51f02fde7410c2e9c234cf2d0a4c7bdbbe176aeb37d8ad7ab4eb58c

ARG VERSION=dev
ARG REVISION=none
ARG CREATED=unknown

LABEL org.opencontainers.image.source="https://github.com/mhosseinab/market-ops" \
      org.opencontainers.image.title="market-ops-nginx" \
      org.opencontainers.image.description="HTTP ingress and static web application for market-ops" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.created="$CREATED"

RUN rm -f /etc/nginx/conf.d/default.conf \
    && chown -R 101:101 /usr/share/nginx/html

COPY --chown=101:101 deploy/nginx/nginx.conf /etc/nginx/nginx.conf
COPY --chown=101:101 --from=web-build /src/apps/web/dist/ /usr/share/nginx/html/

USER 101:101
EXPOSE 8080
ENTRYPOINT ["/usr/sbin/nginx"]
CMD ["-g", "daemon off;"]
