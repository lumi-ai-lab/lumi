# RequesterContext Sandbox E2E Demo

This fixture verifies the boundary between Lumi's generic authorization envelope and a domain-owned consumer.

## Assets

- `testdata/wecom-requesters.json` is a secret-free WeCom Policy v2 fixture.
- `testdata/lumi.config.json` is a PI-only Lumi configuration fixture.
- `consumer` is a fail-closed QDM scope consumer intended to run as a PI tool inside a Lumi Sandbox.
- `sandbox/Dockerfile` replaces only `device-executor` in an existing Sandbox image for branch-level E2E testing.

The committed policy is bot-agnostic and uses a placeholder UserID. Render a runtime copy outside the workspace with the exact WeCom `from.userid`; the authenticated BotID comes from Lumi's runtime configuration and the bot secret must never enter the policy.

`render-runtime-policy.mjs` renders that private copy from environment variables. `start-sandbox-e2e.sh` reads a private `runtime.env`, keeps credentials out of the repository, and starts a PI-only Sandbox instance.

The committed Sandbox agent fixture runs PI as numeric UID/GID `2001:2002` with requester-context reader group `2003`. The launcher enables the paired host security settings; Sandbox manager maps the host root to `/lumi/runtime/requester-context` while preserving reader GID `2003`.

## Build

From `backend`:

```bash
go test ./integration/requestercontext/...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/requester-context-consumer ./integration/requestercontext/consumer
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/device-executor ./cmd/device-executor
```

## Runtime layout

Prepare a private staging directory with this layout. The files containing credentials or real identities are ignored by the fixture's `.gitignore` and must not be committed.

```text
requestercontext/
├── lumi
├── lumi.config.json
├── runtime.env                     # mode 0600
├── policy/
│   └── wecom-requesters.runtime.json
└── workspace/
    └── requester-context-consumer
```

`runtime.env` contains only the private runtime values:

```bash
LUMI_BOT_ID='...'
LUMI_BOT_SECRET='...'
LUMI_TEST_USER_ID='exact body.from.userid'
```

Render the one-user policy and start the isolated instance:

```bash
set -a
source ./runtime.env
set +a
node ./render-runtime-policy.mjs \
  ./testdata/wecom-requesters.json \
  ./policy/wecom-requesters.runtime.json
./start-sandbox-e2e.sh
```

> **Docker isolation:** Sandbox container discovery is currently daemon-global. A Lumi process using a fresh `LUMI_HOME` treats Sandbox containers absent from its own runtime store as orphans and removes them. Each invocation therefore generates a unique Sandbox Workspace ID, and the start script refuses to run when it finds any pre-existing Lumi Sandbox container—even one carrying the legacy fixed E2E Workspace ID. Use a clean, dedicated Docker daemon/host for this fixture. The escape hatch `LUMI_E2E_ALLOW_FOREIGN_SANDBOX_REMOVAL=1` is intended only for a controlled environment where every existing Lumi Sandbox container is disposable.

## Sandbox invocation

Copy the binary into the sandbox workspace and ask PI to execute it while handling the WeCom request:

```bash
./requester-context-consumer \
  --session-id '<raw ACP session ID>' \
  --capability qdm.cmr.query \
  --claim-namespace qdm.scope \
  --manage-area-id area-demo \
  --category-level1-id category-demo
```

The consumer reads `LUMI_REQUESTER_CONTEXT_DIR` and `LUMI_WORKSPACE_ID` from the PI process environment. The caller must also pass the exact, untrimmed ACP session ID through `--session-id`; the consumer hashes that raw value and opens only the corresponding envelope. It never enumerates the `0710` Agent directory.

The selected envelope is validated for:

- Envelope v1, RequesterContext v2, TTL, WorkspaceID, AgentID, and session binding;
- WeCom identity and policy revision structure;
- the required capability;
- the domain-owned `qdm.scope` schema and requested scope values.

Success is emitted as JSON without the raw requester identity. Missing, expired, mismatched, malformed, or out-of-scope authorization exits non-zero.

## Release coupling

This boundary spans the host Lumi manager and the device-executor embedded in the Sandbox image. A release containing this fix must publish both the Lumi artifacts and a rebuilt Sandbox image; deploying only one side leaves the secure runtime contract incomplete.
