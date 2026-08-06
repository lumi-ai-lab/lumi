#!/usr/bin/env bash
# Local Docker smoke for Host _auth → PI context → harness inject path.
# Does NOT touch the remote server. Run after image build:
#   ./docker/sandbox-harness/smoke-host-auth.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
IMAGE="${IMAGE_TAG:-lumi-harness-auth:latest}"
PLATFORM="${PLATFORM:-linux/amd64}"

echo "==> image: $IMAGE (platform $PLATFORM)"

docker run --rm --platform "$PLATFORM" \
  -v "$ROOT/backend/internal/acppatch/assets/pi-acp-0.0.33-dist-index.js:/tmp/pi-acp-dist.index.js:ro" \
  --entrypoint bash "$IMAGE" -lc '
set -euo pipefail
export PATH=/lumi/runtime/npm/bin:$PATH
AG=/lumi/runtime/npm/lib/node_modules/@earendil-works/pi-coding-agent
PI=/lumi/runtime/npm/lib/node_modules/pi-acp

echo "=== 1) apply pi-acp hostAuth artifact ==="
cp /tmp/pi-acp-dist.index.js "$PI/dist/index.js"
test "$(grep -c extractHostAuthFromMeta "$PI/dist/index.js")" -ge 1

echo "=== 2) apply pi-coding-agent in-process patches (same anchors as acppatch) ==="
node <<'"'"'NODE'"'"'
const fs = require("fs");
const path = require("path");
const AG = "/lumi/runtime/npm/lib/node_modules/@earendil-works/pi-coding-agent";
const patches = [
  {
    rel: "dist/modes/rpc/rpc-mode.js",
    old: `                void session
                    .prompt(command.message, {
                    images: command.images,
                    streamingBehavior: command.streamingBehavior,
                    source: "rpc",
                    preflightResult: (didSucceed) => {
                        if (didSucceed) {
                            preflightSucceeded = true;
                            output(success(id, "prompt"));
                        }
                    },
                })`,
    neu: `                void session
                    .prompt(command.message, {
                    images: command.images,
                    streamingBehavior: command.streamingBehavior,
                    source: "rpc",
                    hostAuth: command.hostAuth,
                    preflightResult: (didSucceed) => {
                        if (didSucceed) {
                            preflightSucceeded = true;
                            output(success(id, "prompt"));
                        }
                    },
                })`,
  },
  {
    rel: "dist/core/agent-session.js",
    old: `    async prompt(text, options) {
        const expandPromptTemplates = options?.expandPromptTemplates ?? true;
        const preflightResult = options?.preflightResult;`,
    neu: `    async prompt(text, options) {
        // Lumi hostAuth: bind encrypted auth for this turn onto the extension runner
        // so context handlers can read event._auth without a file envelope.
        if (this._extensionRunner) {
            if (options?.hostAuth && typeof options.hostAuth._auth === "string") {
                this._extensionRunner.setTurnHostAuth(options.hostAuth);
            }
            else {
                this._extensionRunner.clearTurnHostAuth();
            }
        }
        const expandPromptTemplates = options?.expandPromptTemplates ?? true;
        const preflightResult = options?.preflightResult;`,
  },
  {
    rel: "dist/core/extensions/runner.js",
    old: `    async emitContext(messages) {
        const ctx = this.createContext();
        let currentMessages = structuredClone(messages);
        for (const ext of this.extensions) {
            const handlers = ext.handlers.get("context");
            if (!handlers || handlers.length === 0)
                continue;
            for (const handler of handlers) {
                try {
                    const event = { type: "context", messages: currentMessages };
                    const handlerResult = await handler(event, ctx);`,
    neu: `    setTurnHostAuth(hostAuth) {
        if (hostAuth && typeof hostAuth._auth === "string" && hostAuth._auth.startsWith("qdm1enc.") &&
            typeof hostAuth._auth_user_id === "string" && hostAuth._auth_user_id.trim()) {
            this._turnHostAuth = {
                _auth: hostAuth._auth,
                _auth_user_id: String(hostAuth._auth_user_id).trim(),
            };
        }
        else {
            this._turnHostAuth = undefined;
        }
    }
    clearTurnHostAuth() {
        this._turnHostAuth = undefined;
    }
    async emitContext(messages) {
        const ctx = this.createContext();
        let currentMessages = structuredClone(messages);
        for (const ext of this.extensions) {
            const handlers = ext.handlers.get("context");
            if (!handlers || handlers.length === 0)
                continue;
            for (const handler of handlers) {
                try {
                    const event = { type: "context", messages: currentMessages };
                    if (this._turnHostAuth) {
                        event._auth = this._turnHostAuth._auth;
                        event._auth_user_id = this._turnHostAuth._auth_user_id;
                    }
                    const handlerResult = await handler(event, ctx);`,
  },
];
for (const p of patches) {
  const f = path.join(AG, p.rel);
  let t = fs.readFileSync(f, "utf8");
  if (!t.includes(p.old)) {
    if (p.rel.includes("rpc") && t.includes("hostAuth: command.hostAuth")) {
      console.log("already patched", p.rel);
      continue;
    }
    if (p.rel.includes("agent-session") && t.includes("setTurnHostAuth(options.hostAuth)")) {
      console.log("already patched", p.rel);
      continue;
    }
    if (p.rel.includes("runner") && t.includes("setTurnHostAuth")) {
      console.log("already patched", p.rel);
      continue;
    }
    console.error("FAIL missing anchor", p.rel);
    process.exit(1);
  }
  fs.writeFileSync(f, t.replace(p.old, p.neu));
  console.log("patched", p.rel);
}
NODE

echo "=== 3) marker counts (must all be >0) ==="
PI_N=$(grep -c extractHostAuthFromMeta "$PI/dist/index.js" || true)
RPC_N=$(grep -c "hostAuth: command.hostAuth" "$AG/dist/modes/rpc/rpc-mode.js" || true)
AG_N=$(grep -c setTurnHostAuth "$AG/dist/core/extensions/runner.js" || true)
EV_N=$(grep -c "event._auth" "$AG/dist/core/extensions/runner.js" || true)
SESSION_N=$(grep -c "setTurnHostAuth(options.hostAuth)" "$AG/dist/core/agent-session.js" || true)
echo "PI=$PI_N RPC=$RPC_N AG=$AG_N EV=$EV_N SESSION=$SESSION_N"
test "$PI_N" -ge 1
test "$RPC_N" -ge 1
test "$AG_N" -ge 1
test "$EV_N" -ge 1
test "$SESSION_N" -ge 1

echo "=== 4) authz yaml strict ==="
grep -q "allow_local_blob: false" /workspace/config/harness-config.yaml
test ! -f /workspace/config/dev-auth.blob

echo "=== 5) harness inject with host _auth (no LLM) ==="
node --input-type=module <<'"'"'NODE'"'"'
import { createHash } from "node:crypto";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { resolveAuthBlob, loadAuthzConfig } from "/workspace/.pi/extensions/qdm-harness/authz-config.mjs";
import { applyAuthzToToolCall } from "/workspace/.pi/extensions/qdm-harness/authz-inject.mjs";
import { loadLumiHostAuth, lumiEnvelopePath } from "/workspace/.pi/extensions/qdm-harness/lumi-envelope.mjs";

const projectRoot = "/workspace";
const config = loadAuthzConfig(projectRoot);
if (config.mode !== "on" || config.allowLocalBlob !== false) {
  console.error("unexpected authz config", config);
  process.exit(1);
}

const fakeBlob = "qdm1enc." + "B".repeat(48);
const hostUser = "pengmingde01";
const acpSessionId = "019fd64f-82b8-7f8f-9316-6e780a2b84e5";
const piSessionId = "pi-internal-different-id";

// 5a host path
const host = resolveAuthBlob({
  projectRoot, config,
  hostAuth: fakeBlob,
  hostUserId: hostUser,
  sessionId: acpSessionId,
});
if (!host.ok || host.source !== "host") {
  console.error("host resolve failed", host);
  process.exit(1);
}

const event = { toolName: "bash", input: { command: "qdm-metric-cli auth describe" } };
const r = applyAuthzToToolCall(event, {
  mode: "on",
  blob: host.blob,
  allowLocalBlob: false,
  metricCliPath: "/workspace/bin/qdm-metric-cli",
});
if (r?.block) {
  console.error("unexpected block when host bound", r);
  process.exit(1);
}
if (!String(event.input.command).includes("--auth-blob") || !event.input.command.includes(fakeBlob)) {
  console.error("inject missing blob", event.input.command);
  process.exit(1);
}
console.log("host inject OK:", event.input.command.slice(0, 80) + "...");

// 5b unbound must block (this is what production SHOULD have done — not raw CLI error)
const event2 = { toolName: "bash", input: { command: "qdm-metric-cli auth describe" } };
const r2 = applyAuthzToToolCall(event2, { mode: "on", blob: null, allowLocalBlob: false });
if (!r2?.block) {
  console.error("unbound must block", r2);
  process.exit(1);
}
console.log("unbound block OK:", r2.reason.slice(0, 80));

// 5c envelope: written under ACP session id, lookup with PI id fails (production footgun)
const dir = join(tmpdir(), "lumi-smoke-envelope-" + process.pid);
mkdirSync(dir, { recursive: true });
const envPath = lumiEnvelopePath(dir, acpSessionId);
writeFileSync(envPath, JSON.stringify({
  version: 1,
  sessionId: acpSessionId,
  _auth: fakeBlob,
  _auth_user_id: hostUser,
  expiresAt: new Date(Date.now() + 600_000).toISOString(),
}));
const okAcp = loadLumiHostAuth({ env: { LUMI_REQUESTER_CONTEXT_DIR: dir }, sessionId: acpSessionId });
const badPi = loadLumiHostAuth({ env: { LUMI_REQUESTER_CONTEXT_DIR: dir }, sessionId: piSessionId });
if (!okAcp.ok) {
  console.error("envelope ACP lookup failed", okAcp);
  process.exit(1);
}
if (badPi.ok) {
  console.error("envelope PI id should not match ACP-keyed file", badPi);
  process.exit(1);
}
console.log("envelope sessionId sensitivity OK (ACP hit, PI miss)");
rmSync(dir, { recursive: true, force: true });

// 5d hash of production envelope name (sanity)
const hex = createHash("sha256").update(acpSessionId, "utf8").digest("hex");
console.log("ACP envelope filename would be:", hex + ".json");
NODE

echo "=== 6) simulate setTurnHostAuth → emitContext event shape ==="
node <<'"'"'NODE'"'"'
// Minimal clone of patched runner contract
class Runner {
  setTurnHostAuth(hostAuth) {
    if (hostAuth && typeof hostAuth._auth === "string" && hostAuth._auth.startsWith("qdm1enc.") &&
        typeof hostAuth._auth_user_id === "string" && hostAuth._auth_user_id.trim()) {
      this._turnHostAuth = {
        _auth: hostAuth._auth,
        _auth_user_id: String(hostAuth._auth_user_id).trim(),
      };
    } else {
      this._turnHostAuth = undefined;
    }
  }
  emitContextEvent(messages) {
    const event = { type: "context", messages };
    if (this._turnHostAuth) {
      event._auth = this._turnHostAuth._auth;
      event._auth_user_id = this._turnHostAuth._auth_user_id;
    }
    return event;
  }
}
const r = new Runner();
r.setTurnHostAuth({ _auth: "qdm1enc.xxx", _auth_user_id: "pengmingde01" });
const ev = r.emitContextEvent([{ role: "user", content: "我有什么数据权限" }]);
if (!ev._auth || ev._auth_user_id !== "pengmingde01") {
  console.error("context event missing hostAuth", ev);
  process.exit(1);
}
console.log("context event hostAuth OK");
NODE

echo ""
echo "SMOKE PASS: patches + host inject + unbound block + envelope sid"
echo "If production still fails, verify runtime has RPC=$RPC_N (not only AG/EV)."
'
