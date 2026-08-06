# pi-coding-agent 0.83.0 Lumi hostAuth patches

Maintained **in the Lumi repo** alongside `patches/acp/pi-acp/*`.

## Why

`pi-acp` can forward `hostAuth` on the PI RPC `prompt` command, but extensions run **inside** `@earendil-works/pi-coding-agent`. Upstream 0.83.0 builds `context` events as `{ type, messages }` only, so harness never sees `_auth`.

These thin patches:

1. Pass `command.hostAuth` into `session.prompt` options (RPC mode).
2. Store turn hostAuth on the extension runner for the duration of the prompt.
3. Attach `_auth` / `_auth_user_id` on every `context` event so `qdm-harness` can bind in memory.

## Pin

- `@earendil-works/pi-coding-agent@0.83.0` (sandbox Dockerfile / production)

## Files

| Patch | Target |
| --- | --- |
| `dist_modes_rpc_rpc-mode.js.patch` | `dist/modes/rpc/rpc-mode.js` |
| `dist_core_agent-session.js.patch` | `dist/core/agent-session.js` |
| `dist_core_extensions_runner.js.patch` | `dist/core/extensions/runner.js` |

Apply from the package root (`node_modules/@earendil-works/pi-coding-agent`) with `patch -p0`.

## Apply (device-executor / setup)

`backend/internal/acppatch` (or setup) installs the pin, then applies each `.patch` with `patch -p0` and writes `.lumi-patches/pi-coding-agent-0.83.0-host-auth.json`.

## Remove when

Upstream PI agent accepts prompt `hostAuth` and surfaces it on extension `context` events.
