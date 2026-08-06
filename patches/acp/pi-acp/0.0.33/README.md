# pi-acp 0.0.33 Lumi patches

Maintained **in the Lumi repo** (same model as `patches/acp/pi-acp/0.0.27`).

## Why

1. **multi-session**: upstream closes all sessions after `session/new` / `session/load`, which breaks Lumi IM (one ACP sessionId per conversation). Only call `closeAllExcept` when `PI_ACP_SINGLE_LIVE_SESSION=true`.
2. **hostAuth**: Lumi sends ACP `session/prompt` with `_meta._auth` / `_meta._auth_user_id`. Upstream drops `_meta` and only forwards message/images to PI. This patch forwards Host auth into the PI RPC so harness extensions can bind **without a file envelope**.

## Pin

- Package: `pi-acp@0.0.33` (production / sandbox Dockerfile)
- Companion agent: `@earendil-works/pi-coding-agent@0.83.0` (see `patches/agent/pi-coding-agent/0.83.0`)

## Files

| File | Purpose |
| --- | --- |
| `host-auth-and-multi-session.patch` | Unified diff vs official `dist/index.js` |
| `dist.index.js` | Full patched `dist/index.js` (applied by `acppatch`) |
| `original-dist-index.sha256` | Official npm tarball `dist/index.js` sha256 |
| `patched-dist-index.sha256` | Patched artifact sha256 |

## How Lumi applies

`backend/internal/acppatch` installs `pi-acp@0.0.33` into the Lumi npm prefix, verifies package version, then **replaces** `dist/index.js` with the embedded/patched artifact and writes a marker under `.lumi-patches/`.

## Regenerate (after editing `/Users/pengmd/c/pi-acp` on tag `v0.0.33`)

```bash
cd /Users/pengmd/c/pi-acp
git checkout v0.0.33   # or lumi/host-auth-0.0.33 branch
# edit sources, then:
npm run build

WORKDIR=$(mktemp -d)
cd "$WORKDIR"
npm pack pi-acp@0.0.33
mkdir original patched
tar -xzf pi-acp-0.0.33.tgz -C original --strip-components=1
cp -R original/. patched/
cp /Users/pengmd/c/pi-acp/dist/index.js patched/dist/index.js
diff -u original/dist/index.js patched/dist/index.js \
  | sed 's|^--- original/dist/index.js|--- a/dist/index.js|; s|^+++ patched/dist/index.js|+++ b/dist/index.js|' \
  > host-auth-and-multi-session.patch

cp host-auth-and-multi-session.patch \
  /Users/pengmd/c/lumi/patches/acp/pi-acp/0.0.33/
cp /Users/pengmd/c/pi-acp/dist/index.js \
  /Users/pengmd/c/lumi/patches/acp/pi-acp/0.0.33/dist.index.js
cp /Users/pengmd/c/pi-acp/dist/index.js \
  /Users/pengmd/c/lumi/backend/internal/acppatch/assets/pi-acp-0.0.33-dist-index.js
shasum -a 256 original/dist/index.js | awk '{print $1}' \
  > /Users/pengmd/c/lumi/patches/acp/pi-acp/0.0.33/original-dist-index.sha256
shasum -a 256 patched/dist/index.js | awk '{print $1}' \
  > /Users/pengmd/c/lumi/patches/acp/pi-acp/0.0.33/patched-dist-index.sha256
```

## Remove when

Upstream pi-acp:

- keeps multiple live sessions by default (or equivalent config), and
- forwards ACP prompt `_meta._auth` into PI for extensions,

and Lumi has upgraded past 0.0.33 with those behaviors.
