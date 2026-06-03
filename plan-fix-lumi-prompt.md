# Plan: Fix Lumi Prompt Pollution

## Background

When Lumi forwards WeCom messages to Claude Code through ACP, it currently builds a single `session/prompt` text by concatenating:

- WeCom runtime instructions, such as `[LUMI_WECOM_SEND]`.
- Lumi runtime instructions, such as cron and `lumi im run` usage.
- Current Lumi context, such as workspace, channel, conversation, and WeCom IDs.
- Conversation summary in some agent-switch cases.
- The real user message, prefixed as `User: ...`.

This makes Claude Code's `UserPromptSubmit` hook receive Lumi's wrapper text instead of the user's actual question. Downstream systems that rely on the user prompt, such as Harness retrieval, can select the wrong playbook and produce longer or lower-quality data lookup paths.

Observed example:

- Real user message: `昨天粤西1区的销售额是多少?`
- Expected Harness behavior: select `playbooks/cmr/business/s-sale-amt.md` and run the direct sales amount query.
- Current Lumi behavior: the hook sees the full WeCom/Lumi wrapper, causing Harness to select a generic combo/multi-metric path.

## Current Code Shape

Important sources:

- `backend/internal/wecom/gateway.go`
  - Defines `wecomSourceInstruction`.
  - Passes it as `PromptPrefix`.
- `backend/internal/cron/agent_prompt.go`
  - `WithAgentToolInstructionsForContext()` returns `instructions + "\n\nUser: " + prompt`.
- `backend/internal/api/wecom_chat.go`
  - Builds one `promptText` from `PromptPrefix`, Lumi tool instructions, context summary, and `input.Message`.
  - Sends that merged text to ACP via `session/prompt`.
- Similar patterns also exist in WeChat, sandbox/device IM, and regular chat paths.

The ACP Claude adapter used by Lumi, `@agentclientprotocol/claude-agent-acp@0.30.0`, supports `_meta.systemPrompt` on `session/new`. In particular, `_meta.systemPrompt.append` can append custom instructions to the Claude Code preset system prompt. This gives Lumi a proper place to put runtime instructions without polluting user prompts.

## Target Design

Separate Lumi runtime instructions from the user's message at the protocol boundary.

`session/new` should carry Lumi runtime instructions:

```json
{
  "cwd": "/workspace",
  "mcpServers": [],
  "_meta": {
    "systemPrompt": {
      "append": "..."
    }
  }
}
```

`session/prompt` should carry only the actual user-turn content:

```json
{
  "sessionId": "...",
  "prompt": [
    {
      "type": "text",
      "text": "昨天粤西1区的销售额是多少?"
    }
  ]
}
```

This preserves Claude Code behavior while ensuring hooks see the true user prompt.

## Implementation Plan

### 1. Split prompt construction APIs

Refactor `backend/internal/cron/agent_prompt.go` so Lumi can build instruction text separately from user text.

Add an API equivalent to:

```go
func AgentToolInstructionsForContext(ctx ToolContext) string
```

This should return only the Lumi tool/runtime instructions, without appending `User: ...`.

Keep `WithAgentToolInstructionsForContext(prompt, ctx)` temporarily for compatibility, but update new IM paths to avoid using it for `session/prompt`.

### 2. Build IM system prompt append text

Create a small helper for IM runtime instructions, conceptually:

```go
func BuildIMSystemPromptAppend(prefix string, ctx lumicron.ToolContext) string
```

It should concatenate:

- Channel-specific source instruction, such as `wecomSourceInstruction`.
- Lumi cron/tool instructions from `AgentToolInstructionsForContext(ctx)`.

It must not append `User: ...`.

### 3. Inject system prompt at ACP session creation

Update local IM session creation paths, especially `backend/internal/api/wecom_chat.go`:

- When calling `session/new`, include:

```go
"_meta": map[string]any{
    "systemPrompt": map[string]string{
        "append": systemPromptAppend,
    },
},
```

- The append text should be computed from `input.PromptPrefix` and the same Lumi `ToolContext` currently used to wrap the prompt.
- Keep `mcpServers` and `cwd` unchanged.

Apply the same pattern to `wechat_chat.go` if scope allows, because it has the same prompt pollution pattern.

### 4. Keep `session/prompt` clean

Update WeCom prompt sending so `promptText` is based on the real user content only:

- Start from `input.Message`.
- Preserve attachment blocks, because they describe user-provided files.
- Do not prepend `input.PromptPrefix`.
- Do not call `WithAgentToolInstructionsForContext()` before `session/prompt`.
- Avoid adding `Current Lumi context` to the prompt body.

For agent-switch conversation summary, do not blindly prepend it to the user prompt if it would pollute hooks. Prefer one of these in order:

1. Inject the summary into a system prompt append when a new session is created.
2. If the ACP session is reused and no separate context channel exists, leave existing behavior unchanged only for the agent-switch path and mark it as follow-up technical debt.

For the first fix, the main success criterion is that normal WeCom turns no longer send Lumi wrapper text as user prompt.

### 5. Handle existing sessions

Because `_meta.systemPrompt.append` is applied on `session/new`, existing ACP sessions will not automatically receive the new Lumi instructions.

Add a minimal versioning strategy:

- Define a constant such as `imSystemPromptVersion`.
- Track the version alongside stored agent session IDs in memory.
- If a conversation's stored session was created with an older version, create a new ACP session.

For an initial deployment, it is acceptable to force new IM sessions after restart if persistent agent session IDs are not stored across process restarts.

### 6. Sandbox/device IM path

The sandbox path currently sends a `TaskExecutePayload` containing a single `Prompt` string to `device-executor`, which then forwards it to ACP as `session/prompt`.

To fully fix server deployments using sandbox/device mode, extend the task protocol:

```go
type TaskExecutePayload struct {
    ...
    Prompt             string `json:"prompt"`
    SystemPromptAppend string `json:"systemPromptAppend,omitempty"`
}
```

Then update `device-executor`:

- On `session/new`, include `_meta.systemPrompt.append` when `SystemPromptAppend` is present.
- On `session/prompt`, send only `Prompt`.

This is important for the 101 deployment, because the observed Lumi container uses the sandbox/device flow.

## Acceptance Criteria

For the WeCom question `昨天粤西1区的销售额是多少?`:

- Claude Code `UserPromptSubmit` hook receives only the real user message, or the real message plus user attachment block.
- Harness selects `playbooks/cmr/business/s-sale-amt.md`, not `playbooks/common/multi-metric-report.md`.
- The agent uses the direct CMR CLI path for sales amount instead of broad store search/ranking paths.
- Lumi still supports:
  - `[LUMI_WECOM_SEND]` media/file response protocol.
  - Cron task creation via `lumi cron add`.
  - IM intermediate file/image sending via `lumi im run`.
  - Existing MCP server injection.

## Test Plan

Add or update unit tests for:

- `AgentToolInstructionsForContext()` returns instructions without `User:`.
- `WithAgentToolInstructionsForContext()` remains compatible where still used.
- WeCom local chat `session/new` includes `_meta.systemPrompt.append`.
- WeCom `session/prompt` sends only `input.Message`.
- Sandbox/device task payload forwards `SystemPromptAppend` into `session/new`.
- Device executor sends clean `session/prompt`.

Add an integration-style test or captured fake ACP test for:

- WeCom input `昨天粤西1区的销售额是多少?`.
- Assert the recorded `session/prompt` JSON does not contain:
  - `You are replying to a WeCom user through Lumi.`
  - `You are running inside Lumi.`
  - `Current Lumi context:`
  - `User: 昨天粤西1区的销售额是多少?`
- Assert the recorded `session/prompt` does contain the clean message text.

Manual verification on 101:

1. Deploy the updated Lumi binary/device executor.
2. Restart the Lumi sandbox/container or force a new ACP session.
3. Ask in WeCom: `昨天粤西1区的销售额是多少?`
4. Inspect Claude transcript or hook logs.
5. Confirm Harness selects the single sales amount playbook and uses the direct CMR CLI command.

## Notes

- This is a Lumi-side fix. Harness should not need a Lumi-specific prompt normalizer for this case.
- If ACP behavior changes in the future, keep the desired invariant: Lumi runtime instructions must not be passed as user prompt text.
- If a fallback is needed before this full fix ships, Harness can temporarily detect the Lumi wrapper and extract the final `User:` block, but that should remain a compatibility workaround, not the primary design.
