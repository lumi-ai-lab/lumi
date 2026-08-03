#!/usr/bin/env bash

set -euo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
runtime_env="$script_dir/runtime.env"
runtime_policy="$script_dir/policy/wecom-requesters.runtime.json"
lumi_bin="$script_dir/lumi"
config_path="$script_dir/lumi.config.json"
workspace_path="$script_dir/workspace"

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

# Sandbox discovery is currently Docker-daemon global. A Manager backed by this
# fixture's private LUMI_HOME would remove Lumi Sandbox containers that are not
# present in its own runtime store. Refuse a shared daemon unless the operator
# has explicitly accepted that destructive test behavior.
expected_sandbox_workspace="cli-sandbox-requester-context-e2e"
foreign_sandboxes=()
if command -v docker >/dev/null 2>&1; then
  while IFS= read -r container_id; do
    [[ -n "$container_id" ]] || continue
    workspace_id=$(docker inspect \
      --format '{{ index .Config.Labels "lumi.workspace_id" }}' \
      "$container_id" 2>/dev/null || true)
    if [[ -n "$workspace_id" && "$workspace_id" != "$expected_sandbox_workspace" ]]; then
      foreign_sandboxes+=("$workspace_id")
    fi
  done < <(docker ps -aq --filter 'label=lumi.runtime=sandbox')
fi
if (( ${#foreign_sandboxes[@]} > 0 )) && [[ "${LUMI_E2E_ALLOW_FOREIGN_SANDBOX_REMOVAL:-0}" != "1" ]]; then
  echo "refusing to start: the Docker daemon contains Sandbox workspaces owned by another Lumi runtime:" >&2
  printf '  - %s\n' "${foreign_sandboxes[@]}" >&2
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

exec "$lumi_bin" wecom run \
  --config "$config_path" \
  --workspace "$workspace_path" \
  --kind sandbox \
  --agent pi \
  --agents pi \
  --sandbox-id requester-context-e2e \
  --sandbox-warmup wait \
  --stream
