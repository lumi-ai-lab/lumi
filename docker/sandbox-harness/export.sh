#!/usr/bin/env bash
# Export local lumi-harness-auth (linux/amd64) as a gzip tarball for scp.
#
# Usage:
#   ./docker/sandbox-harness/export.sh
#   OUT=/tmp/lumi-harness-auth-amd64.tar.gz ./docker/sandbox-harness/export.sh
#   LOCAL_IMAGE=lumi-harness-auth:latest ./docker/sandbox-harness/export.sh
#
# On server:
#   scp ... then: gunzip -c lumi-harness-auth-amd64.tar.gz | docker load
#   # or: docker load -i lumi-harness-auth-amd64.tar.gz  (if not gzipped)

set -euo pipefail

LOCAL_IMAGE="${LOCAL_IMAGE:-lumi-harness-auth:latest}"
# Accept bare name without tag
if [[ "${LOCAL_IMAGE}" != *:* ]]; then
  LOCAL_IMAGE="${LOCAL_IMAGE}:latest"
fi
STAMP="$(date +%Y%m%d)"
OUT="${OUT:-/tmp/lumi-harness-auth-amd64-${STAMP}.tar.gz}"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi

if ! docker image inspect "${LOCAL_IMAGE}" >/dev/null 2>&1; then
  # try without :latest if user passed name only already handled
  if docker image inspect "${LOCAL_IMAGE%:*}" >/dev/null 2>&1; then
    LOCAL_IMAGE="${LOCAL_IMAGE%:*}"
  else
    echo "local image ${LOCAL_IMAGE} not found; run ./docker/sandbox-harness/build.sh first" >&2
    exit 1
  fi
fi

os="$(docker image inspect "${LOCAL_IMAGE}" --format '{{.Os}}')"
arch="$(docker image inspect "${LOCAL_IMAGE}" --format '{{.Architecture}}')"
if [[ "${os}/${arch}" != "linux/amd64" ]]; then
  echo "error: image is ${os}/${arch}; export is intended for linux/amd64 servers" >&2
  exit 1
fi

# Normalize repo tag in the tarball so server load gets a stable name
EXPORT_NAME="${EXPORT_NAME:-lumi-harness-auth:latest}"
if [[ "${LOCAL_IMAGE}" != "${EXPORT_NAME}" ]]; then
  docker tag "${LOCAL_IMAGE}" "${EXPORT_NAME}"
fi

echo "Exporting ${EXPORT_NAME} (${os}/${arch})"
echo "  → ${OUT}"

mkdir -p "$(dirname "${OUT}")"
# gzip -1 is faster; gzip -6 default better ratio. Use -1 for speed on large images.
docker save "${EXPORT_NAME}" | gzip -1 > "${OUT}"

bytes="$(wc -c < "${OUT}" | tr -d ' ')"
mib="$(awk -v b="${bytes}" 'BEGIN { printf "%.1f", b/1024/1024 }')"
echo
echo "OK: ${OUT} (${mib} MiB)"
echo
echo "Upload + load (replace USER@HOST):"
echo "  scp ${OUT} USER@HOST:/tmp/"
echo "  ssh USER@HOST 'gunzip -c /tmp/$(basename "${OUT}") | docker load'"
echo
echo "Then on server: docker images | grep lumi-harness-auth"
