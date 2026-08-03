# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY gotray/go.mod gotray/go.sum ./gotray/
COPY web/go.mod ./web/
COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download

COPY gotray ./gotray
COPY backend ./backend
RUN cd backend \
    && test "${TARGETOS}" = "linux" \
    && case "${TARGETARCH}" in amd64|arm64) ;; *) exit 1 ;; esac \
    && CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
      go build -buildvcs=false -trimpath -ldflags="-s -w" \
      -o /out/device-executor ./cmd/device-executor \
    && machine="$(od -An -tu2 -j18 -N2 /out/device-executor | xargs)" \
    && case "${TARGETARCH}:${machine}" in amd64:62|arm64:183) ;; *) exit 1 ;; esac

FROM node:22.22.2-bookworm-slim AS runtime

ENV NODE_ENV=production
ENV NPM_CONFIG_UPDATE_NOTIFIER=false
ENV NPM_CONFIG_FUND=false
ENV NPM_CONFIG_AUDIT=false
ENV NPM_CONFIG_PREFIX=/lumi/runtime/npm
ENV NPM_CONFIG_CACHE=/lumi/runtime/npm-cache
ENV PATH=/lumi/runtime/npm/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       bash \
       ca-certificates \
       curl \
       git \
       openssh-client \
       tini \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/device-executor /usr/local/bin/device-executor

RUN mkdir -p /workspace /lumi/device-executor /lumi/runtime/npm/bin /lumi/runtime/npm/lib/node_modules /lumi/runtime/npm-cache /root/.claude /root/.codex /root/.config /root/.pi \
    && chmod 0755 /usr/local/bin/device-executor

WORKDIR /workspace

ENTRYPOINT ["/usr/bin/tini", "--", "device-executor"]
CMD ["--help"]
