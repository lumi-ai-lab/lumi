#!/usr/bin/env bash
# Build local image: lumi-harness-auth
#
# Token file (gitignored, single line, no quotes):
#   docker/sandbox-harness/.github_token
#
# Usage:
#   ./docker/sandbox-harness/build.sh
#   BASE_IMAGE=ghcr.io/lumi-ai-lab/lumi-sandbox:v0.0.12 ./docker/sandbox-harness/build.sh

set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
# Repo root: Dockerfile multi-stage build needs backend/ + gotray/ + web/
REPO_ROOT="$(cd "${ROOT}/../.." && pwd)"
TOKEN_FILE="${ROOT}/.github_token"
IMAGE_TAG="${IMAGE_TAG:-lumi-harness-auth}"
PLATFORM="${PLATFORM:-linux/amd64}"
BASE_IMAGE="${BASE_IMAGE:-ghcr.io/lumi-ai-lab/lumi-sandbox:latest}"
DOCKERFILE="${ROOT}/Dockerfile"

if [[ ! -f "${TOKEN_FILE}" ]]; then
  cat >&2 <<EOF
missing ${TOKEN_FILE}

Copy the example and put a real GitHub token on a single line:
  cp ${ROOT}/.github_token.example ${TOKEN_FILE}
  # edit ${TOKEN_FILE}

The file is gitignored. Token is injected only as a BuildKit secret (not ENV in the image).
EOF
  exit 1
fi

# Reject empty / example placeholder before spending docker pull time.
token_preview="$(tr -d '[:space:]' < "${TOKEN_FILE}")"
if [[ -z "${token_preview}" || "${token_preview}" == "gho_replace_me" ]]; then
  echo "token in ${TOKEN_FILE} is empty or still the example placeholder" >&2
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi

# Proxy: prefer host.docker.internal so build containers can reach host Shadowrocket/etc.
# Example:
#   export http_proxy=http://host.docker.internal:1082
#   export https_proxy=http://host.docker.internal:1082
#   ./docker/sandbox-harness/build.sh
PROXY_URL="${HTTPS_PROXY:-${https_proxy:-${HTTP_PROXY:-${http_proxy:-}}}}"
# If user set 127.0.0.1 (host-only), rewrite for Docker build on Mac/Linux Desktop.
if [[ -n "${PROXY_URL}" && "${PROXY_URL}" == *127.0.0.1* ]]; then
  PROXY_URL="${PROXY_URL//127.0.0.1/host.docker.internal}"
  echo "note: rewrote proxy 127.0.0.1 -> host.docker.internal for docker build"
fi
NO_PROXY_VAL="${NO_PROXY:-${no_proxy:-localhost,127.0.0.1,host.docker.internal}}"

echo "Building ${IMAGE_TAG}"
echo "  base:     ${BASE_IMAGE}"
echo "  platform: ${PLATFORM}"
echo "  token:    ${TOKEN_FILE} (secret, not committed)"
if [[ -n "${PROXY_URL}" ]]; then
  echo "  proxy:    ${PROXY_URL}"
fi

export DOCKER_BUILDKIT=1
build_args=(
  --platform "${PLATFORM}"
  --build-arg "BASE_IMAGE=${BASE_IMAGE}"
  --secret "id=github_token,src=${TOKEN_FILE}"
  -t "${IMAGE_TAG}"
  -f "${DOCKERFILE}"
)
if [[ -n "${PROXY_URL}" ]]; then
  build_args+=(
    --build-arg "HTTP_PROXY=${PROXY_URL}"
    --build-arg "HTTPS_PROXY=${PROXY_URL}"
    --build-arg "http_proxy=${PROXY_URL}"
    --build-arg "https_proxy=${PROXY_URL}"
    --build-arg "NO_PROXY=${NO_PROXY_VAL}"
    --build-arg "no_proxy=${NO_PROXY_VAL}"
  )
fi

# Context is repo root so COPY backend/ gotray/ web/ works for executor rebuild.
docker build "${build_args[@]}" "${REPO_ROOT}"
echo
echo "OK: ${IMAGE_TAG}"
echo "Layer-0 smoke:"
echo "  docker run --rm -it --platform ${PLATFORM} --entrypoint bash ${IMAGE_TAG}"
