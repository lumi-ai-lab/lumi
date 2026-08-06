# Local Layer-0 image: lumi sandbox base + PI (host-aligned versions) + harness-data
# installed into /workspace with --data-auth.
#
# Build (token via BuildKit secret, never baked into layers):
#   ./docker/sandbox-harness/build.sh
#
# Tag: lumi-harness-auth
# Note: avoid "# syntax=docker/dockerfile:1" so build does not pull docker/dockerfile from Docker Hub.

# Rebuild device-executor so HostAuth envelope write matches current Lumi (auth-2).
# The published lumi-sandbox base may lag behind host lumi binary.
# Global ARGs (must be before FROM to be usable in FROM lines).
ARG GO_VERSION=1.25
ARG BASE_IMAGE=ghcr.io/lumi-ai-lab/lumi-sandbox:latest

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS executor-builder
ARG TARGETARCH
WORKDIR /src
COPY backend/go.mod backend/go.sum ./backend/
COPY gotray/go.mod gotray/go.sum ./gotray/
COPY web/go.mod ./web/
RUN cd backend && go mod download
COPY gotray ./gotray
COPY backend ./backend
RUN cd backend \
    && CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
      go build -buildvcs=false -trimpath -ldflags="-s -w" \
      -o /out/device-executor ./cmd/device-executor

FROM ${BASE_IMAGE}

# Override base image executor with auth-2 capable binary.
COPY --from=executor-builder /out/device-executor /usr/local/bin/device-executor
RUN chmod 0755 /usr/local/bin/device-executor

# Match host / ~/.lumi/lumi.config.json PI stack.
ENV NPM_CONFIG_PREFIX=/lumi/runtime/npm
ENV NPM_CONFIG_CACHE=/lumi/runtime/npm-cache
ENV PATH=/lumi/runtime/npm/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

RUN npm install -g \
      pi-acp@0.0.33 \
      @earendil-works/pi-coding-agent@0.83.0

WORKDIR /workspace

# Latest harness-data installer; GITHUB_TOKEN only from BuildKit secret (not ENV).
# Use bash: base image /bin/sh is dash (no pipefail).
RUN --mount=type=secret,id=github_token \
    bash -ec '\
      token="$(tr -d "[:space:]" < /run/secrets/github_token)"; \
      if [ -z "${token}" ] || [ "${token}" = "gho_replace_me" ]; then \
        echo "github_token secret is empty or still the example placeholder" >&2; \
        exit 1; \
      fi; \
      GITHUB_TOKEN="${token}" npx --yes @lumi-ai-lab/harness-data install \
        --dir /workspace \
        --yes \
        --agent pi \
        --data-auth \
        --git-protocol https \
    '

# Production-strict authz: Host envelope only (install --data-auth defaults allow_local_blob true).
# allow_local_blob:false alone is not enough — fixture files remain cat-able by the model
# (false success as local-test-user). Physically remove them and drop local-only yaml keys.
RUN sed -i 's/^  allow_local_blob: true$/  allow_local_blob: false/' \
      /workspace/config/harness-config.yaml \
    && rm -f /workspace/config/dev-auth.blob \
             /workspace/config/fixtures/local-test-auth.blob \
    && sed -i '/blob_file:/d;/dev_user_id:/d' /workspace/config/harness-config.yaml || true \
    && grep -A8 '^authz:' /workspace/config/harness-config.yaml \
    && grep -q 'allow_local_blob: false' /workspace/config/harness-config.yaml \
    && test ! -f /workspace/config/dev-auth.blob \
    && test ! -f /workspace/config/fixtures/local-test-auth.blob

# Keep base ENTRYPOINT (tini + device-executor). Layer-0 smoke:
#   docker run --rm -it --entrypoint bash lumi-harness-auth
