# IM `/agent` 持久化切换 Agent 方案

## Summary

为 WeCom 和 WeChat 增加统一控制命令 `/agent`：

- `/agent`：列出当前 IM 会话可用 Agent，并标记当前 active Agent。
- `/agent <agent_id>`：持久化切换当前 IM conversation 的 active Agent。
- 普通消息：发送给当前 IM conversation 的 active Agent；新 conversation 默认使用渠道配置里的 `agentId`。
- 不支持 `/agents`；不复用 `@agent`，避免和 IM 平台 mention 冲突。

## Key Changes

- Agent 可用范围使用当前 IM 绑定 workspace 的 `agents` 白名单；如果 workspace `agents` 为空，则使用全局 `config.Agents`。
- 控制命令只在 `strings.TrimSpace(message)` 后的开头匹配：
  - 精确 `/agent`：只回复列表，不写入会话历史，不发送给 Agent。
  - 精确 `/agent <id>`：只切换并回复状态，不写入会话历史，不发送给 Agent。
  - `/agent <id> <extra text>`：回复格式错误提示，不切换、不发送。
- 当前 active Agent 按 IM conversation 持久化，复用已有隐藏会话存储里的 `ActiveAgent` 字段；不要写回渠道配置 `agentId`。
- 当没有已持久化 active Agent，或持久化的 Agent 已不可用时，回退到渠道默认 `agentId`；如果默认也不可用，返回配置错误。
- 切换 Agent 后，下一条普通消息使用新的 Agent；如果该 conversation 已有历史消息，给新 Agent 携带最近对话摘要，复用 Web 的 `GetContextSummary(..., 10)` 行为。

## Implementation Details

- 在 WeCom 和 WeChat 服务层各自增加 IM agent command resolver，或抽一个 internal 公共 helper，输入为：原始文本、conversationID、workspace、默认 agent、全局 agents、conversation store。
- 在入站 `handleInboundMessage` 中，完成 workspace 校验后、附件处理和 runner 调用前处理 `/agent` 命令；命中控制命令后直接通过当前渠道回复并 return。
- 普通消息路径中，将 `cfg.AgentID` 改为 resolved active Agent：
  - 先读取隐藏会话 `ActiveAgent`。
  - 不存在则用 `cfg.AgentID`。
  - 校验 resolved Agent 是否在当前 workspace 可用 Agent 集合内。
- 在 IM chat runtime `RunWeComChat` / `RunWeChatChat` 中，运行前设置 conversation active Agent 为 `input.AgentID`；切换 Agent 且已有历史时，把 context summary 加到 prompt 前。
- 回复文案统一：
  - `/agent`：

    ```text
    当前 Agent：claude

    可用 Agent：
    * claude 当前
    * codex

    切换：/agent <id>
    ```

  - 成功切换：

    ```text
    已切换当前 Agent 为 codex。
    ```

  - 未找到或不允许：

    ```text
    未找到可用 Agent：foo

    可用 Agent：claude, codex
    ```

  - 格式错误：

    ```text
    格式：/agent 或 /agent <id>
    ```

## Test Plan

- WeCom gateway tests:
  - `/agent` 返回列表，不调用 runner。
  - `/agent codex` 持久化切换，后续普通消息使用 `codex`。
  - `/agent codex hello` 返回格式错误，不调用 runner。
  - workspace agents 只允许白名单内 Agent；空白名单时允许全局 agents。
- WeChat gateway tests 覆盖同等行为。
- Runtime tests:
  - 切换 Agent 后普通消息设置 `ActiveAgent`。
  - 切换到新 Agent 且已有历史时，prompt 包含 `[Previous conversation context]`。
- 回归测试：
  - 普通 IM 消息在未使用 `/agent` 时仍使用渠道默认 `agentId`。
  - 附件处理、媒体发送协议、cron 路径不受控制命令影响。

## Assumptions

- v1 不新增设置页字段，也不新增渠道级 `agentIds` 配置。
- `/agent` 控制命令不进入会话历史，也不发送给 Agent。
- `/agent <id>` 是持久化切换，不支持同条消息临时指定 Agent。
- `@agent` 在 IM 渠道继续作为普通文本处理，不做 Agent 路由。
