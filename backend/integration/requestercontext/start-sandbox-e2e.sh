#!/usr/bin/env bash

set -euo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
runtime_env="$script_dir/runtime.env"
runtime_policy="$script_dir/policy/wecom-requesters.runtime.json"
lumi_bin="$script_dir/lumi"
config_path="$script_dir/lumi.config.json"
workspace_path="$script_dir/workspace"

e2e_run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$-${RANDOM}"
sandbox_id="requester-context-e2e-$e2e_run_id"

if [[ ! -f "$runtime_env" ]]; then
  echo "missing $runtime_env" >&2
  exit 1
fi
if [[ ! -f "$runtime_policy" ]]; then
  echo "missing $runtime_policy; render it with render-runtime-policy.mjs first" >&2
  exit 1
fi
if [[ ! -x "$lumi_bin" ]]; then
  echo "missing executable $lumi_bin" >&2
  exit 1
fi
if [[ ! -f "$config_path" ]]; then
  echo "missing $config_path" >&2
  exit 1
fi
if [[ ! -d "$workspace_path" ]]; then
  echo "missing workspace directory $workspace_path" >&2
  exit 1
fi

# Sandbox discovery is currently Docker-daemon global. This run uses a unique
# workspace ID, so no pre-existing Lumi Sandbox can belong to it. A Manager
# backed by this fixture's private LUMI_HOME would remove such containers as
# orphans; refuse to start unless the operator explicitly accepts that
# destructive test behavior.
existing_sandboxes=()
if command -v docker >/dev/null 2>&1; then
  while IFS= read -r container_id; do
    [[ -n "$container_id" ]] || continue
    workspace_id=$(docker inspect \
      --format '{{ index .Config.Labels "lumi.workspace_id" }}' \
      "$container_id" 2>/dev/null || true)
    [[ -n "$workspace_id" ]] || workspace_id="<missing-workspace-label>"
    existing_sandboxes+=("$workspace_id ($container_id)")
  done < <(docker ps -aq --filter 'label=lumi.runtime=sandbox')
fi
if (( ${#existing_sandboxes[@]} > 0 )) && [[ "${LUMI_E2E_ALLOW_FOREIGN_SANDBOX_REMOVAL:-0}" != "1" ]]; then
  echo "refusing to start: the Docker daemon already contains Lumi Sandbox containers that this unique E2E run does not own:" >&2
  printf '  - %s\n' "${existing_sandboxes[@]}" >&2
  echo "use a dedicated Docker daemon; the current Sandbox Manager removes containers absent from its own LUMI_HOME store" >&2
  exit 1
fi

set -a
source "$runtime_env"
set +a

if [[ -z "${LUMI_BOT_ID:-}" || -z "${LUMI_BOT_SECRET:-}" ]]; then
  echo "runtime.env must define LUMI_BOT_ID and LUMI_BOT_SECRET" >&2
  exit 1
fi

export LUMI_HOME="$script_dir/lumi-home"
export LUMI_PORT="${LUMI_E2E_PORT:-3004}"
export LUMI_WECOM_REQUESTER_CONFIG="$runtime_policy"
export LUMI_REQUESTER_CONTEXT_ROOT="$script_dir/secure-runtime/requester-context"
export LUMI_REQUESTER_CONTEXT_READER_GID="2003"

exec "$lumi_bin" wecom run \
  --config "$config_path" \
  --workspace "$workspace_path" \
  --kind sandbox \
  --agent pi \
  --agents pi \
  --sandbox-id "$sandbox_id" \
  --sandbox-warmup wait \
  --stream
