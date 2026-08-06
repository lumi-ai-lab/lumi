#!/usr/bin/env bash
# Tag and push a local lumi-harness-auth image to GHCR (linux/amd64).
#
# Prerequisites:
#   - Local image built (./docker/sandbox-harness/build.sh) or already present
#   - docker login ghcr.io (or gh auth with write:packages)
#
# Usage:
#   ./docker/sandbox-harness/publish.sh
#   IMAGE_TAG=v0.0.1 ./docker/sandbox-harness/publish.sh
#   LOCAL_IMAGE=lumi-harness-auth REMOTE_TAG=latest ./docker/sandbox-harness/publish.sh
#   PUSH_LATEST=1 IMAGE_TAG=v0.0.1 ./docker/sandbox-harness/publish.sh

set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
LOCAL_IMAGE="${LOCAL_IMAGE:-lumi-harness-auth}"
REGISTRY="${REGISTRY:-ghcr.io}"
IMAGE_NAME="${IMAGE_NAME:-lumi-ai-lab/lumi-harness-auth}"
REMOTE="${REGISTRY}/${IMAGE_NAME}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
PUSH_LATEST="${PUSH_LATEST:-0}"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi

if ! docker image inspect "${LOCAL_IMAGE}" >/dev/null 2>&1; then
  cat >&2 <<EOF
local image ${LOCAL_IMAGE} not found.

Build first:
  ./docker/sandbox-harness/build.sh
EOF
  exit 1
fi

arch="$(docker image inspect "${LOCAL_IMAGE}" --format '{{.Architecture}}')"
os="$(docker image inspect "${LOCAL_IMAGE}" --format '{{.Os}}')"
if [[ "${os}/${arch}" != "linux/amd64" ]]; then
  echo "warning: image is ${os}/${arch}; Linux x86 servers need linux/amd64" >&2
fi

# Login to GHCR when possible (idempotent). Prefer GHCR_TOKEN / GITHUB_TOKEN, then gh.
if [[ -n "${GHCR_TOKEN:-${GITHUB_TOKEN:-}}" ]]; then
  user="${GHCR_USERNAME:-${GITHUB_USER:-$(gh api user -q .login 2>/dev/null || echo github)}}"
  echo "${GHCR_TOKEN:-${GITHUB_TOKEN}}" | docker login "${REGISTRY}" -u "${user}" --password-stdin
elif command -v gh >/dev/null 2>&1; then
  echo "logging into ${REGISTRY} via gh auth token..."
  gh auth token | docker login "${REGISTRY}" -u "$(gh api user -q .login)" --password-stdin
else
  echo "note: ensure you are logged in: docker login ${REGISTRY}"
fi
echo "Publishing ${LOCAL_IMAGE} → ${REMOTE}:${IMAGE_TAG}"
docker tag "${LOCAL_IMAGE}" "${REMOTE}:${IMAGE_TAG}"
docker push "${REMOTE}:${IMAGE_TAG}"

if [[ "${PUSH_LATEST}" == "1" && "${IMAGE_TAG}" != "latest" ]]; then
  echo "Also tagging ${REMOTE}:latest"
  docker tag "${LOCAL_IMAGE}" "${REMOTE}:latest"
  docker push "${REMOTE}:latest"
fi

# Immutable digest line for operators
digest="$(docker image inspect "${REMOTE}:${IMAGE_TAG}" --format '{{index .RepoDigests 0}}' 2>/dev/null || true)"
echo
echo "OK: ${REMOTE}:${IMAGE_TAG}"
if [[ -n "${digest}" ]]; then
  echo "Digest: ${digest}"
fi
echo
echo "Server pull:"
echo "  docker pull ${REMOTE}:${IMAGE_TAG}"
echo "Use with Lumi:"
echo "  lumi wecom run --kind sandbox --image ${REMOTE}:${IMAGE_TAG} ..."
echo
echo "Note: after pull, seed host workspace from image /workspace (bind mount overwrites it)."
echo "  See ${ROOT}/README.md"
