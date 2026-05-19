# IM Debug Slash Commands 方案

## 概要

为 IM 渠道增加会话级 slash commands，让用户可以在当前 IM 会话中开启或关闭 Debug 输出。默认行为保持不变：Thinking 和 Tools Use 细节都不展示，只有用户显式开启后才展示。

支持的命令：

- `/debug`：查看当前 Debug 状态。
- `/debug thinking`：切换 Thinking 展示。
- `/debug tools`：切换 Tools Use 展示。
- `/debug all`：切换完整 Debug 模式。
- `/debug thinking on|off`：显式开启或关闭 Thinking 展示。
- `/debug tools on|off`：显式开启或关闭 Tools Use 展示。
- `/debug all on|off`：显式开启或关闭 Thinking + Tools Use。

默认状态为 `thinking=off, tools=off`。

## 命令行为

Debug 设置作用于当前 IM 会话，和现有 `/agent` 命令的会话级行为保持一致。

`/debug` 返回当前状态和简短用法：

```text
Debug 状态：thinking=off, tools=off

用法：/debug thinking|tools|all [on|off]
```

`/debug thinking` 只切换 Thinking 展示。

`/debug tools` 只切换 Tools Use 展示。

`/debug all` 作为“完整 Debug 模式”的开关：

- 如果当前 `thinking=on` 且 `tools=on`，执行后两者都关闭。
- 其他任何状态下，执行后两者都开启。

示例：

- `thinking=off, tools=off` + `/debug all` => `thinking=on, tools=on`
- `thinking=on, tools=off` + `/debug all` => `thinking=on, tools=on`
- `thinking=off, tools=on` + `/debug all` => `thinking=on, tools=on`
- `thinking=on, tools=on` + `/debug all` => `thinking=off, tools=off`

显式命令始终设置为指定值：

- `/debug all on` => `thinking=on, tools=on`
- `/debug all off` => `thinking=off, tools=off`
- `/debug thinking off` => `thinking=off`，`tools` 不变
- `/debug tools on` => `tools=on`，`thinking` 不变

主要语法使用 `on/off`。可以兼容 `true/false`、`enable/disable` 这类别名，但用户可见帮助文案只展示 `on/off`，避免复杂化。

非法格式返回固定帮助文案，并且不能启动模型运行。

## 实现改动

### 会话状态

在 `storage.StoredSession` 增加会话级 Debug 设置字段，例如：

```go
type IMDebugSettings struct {
    Thinking bool `json:"thinking,omitempty"`
    Tools    bool `json:"tools,omitempty"`
}
```

把该字段挂到 `StoredSession` 上，并使用 `omitempty` 保持向后兼容。旧的 session JSON 没有该字段时，应等价于 `thinking=false, tools=false`。

### Slash Command 处理

扩展 `backend/internal/imagent/command.go`，让 `HandleCommand` 同时支持现有 `/agent` 和新的 `/debug` 命令。

`/debug` 命令需要：

- 在需要持久化状态变更时，加载或创建当前 `StoredSession`。
- 保留已有 session 字段，例如 `Messages`、`ActiveAgent`、`WorkspaceID` 和时间戳。
- 设置变更时更新 `UpdatedAt`。
- 返回清晰的中文确认文案，例如：

```text
已开启 Debug Thinking：thinking=on, tools=off
```

或：

```text
已开启全部 Debug：thinking=on, tools=on
```

### IM Runtime 和 Gateway 输出

当前链路是：

- Runtime 会过滤 `agent_thought_chunk`，并把工具调用解析为结构化 `tool_call` 事件。
- Gateway 只从 `update.agent_message_chunk` 累计模型正文文本。
- 模型运行完成后，再把最终文本回复到 IM。

开启 Debug 后只改变对应开关控制的输出：

- `thinking=false` 时，保持当前 Thinking 过滤行为。
- `thinking=true` 时，把 Thinking 内容追加到最终 IM 回复中，使用简洁 Debug 块，例如：

```text
【Thinking】
...
```

- `tools=false` 时，Gateway 继续忽略 `tool_call` 事件。
- `tools=true` 时，Gateway 消费 `tool_call` 事件，并把简短工具摘要追加到最终 IM 回复。

Tools Use 摘要要保持克制，只展示工具名、状态，以及可用时的短输入、短输出或错误摘要。默认不要发送完整 stdout/stderr。

推荐摘要格式：

```text
【Tool】Read completed: backend/internal/api/im_chat.go
【Tool】Bash error: exit status 1
```

每条工具摘要需要硬截断，例如最多 300 字符，避免 IM 消息刷屏或超过平台限制。

该功能只影响 IM 渠道，不改变 Web UI 中 Thinking 和 Tools 的展示逻辑。

## 测试计划

为 `imagent` 命令处理增加或更新单测：

- `/debug` 展示默认状态，并且不修改已有消息。
- `/debug thinking` 可以切换 Thinking 并持久化。
- `/debug tools` 可以切换 Tools Use 并持久化。
- `/debug thinking on|off` 可以显式设置 Thinking。
- `/debug tools on|off` 可以显式设置 Tools Use。
- `/debug all` 符合完整 Debug 模式的 toggle 规则。
- `/debug all on|off` 可以显式设置两个开关。
- 非法 `/debug` 格式返回帮助文案，并且不持久化状态变更。
- 现有 `/agent` 测试保持通过。

为 WeChat 和 WeCom 的 gateway/runtime 增加或更新测试：

- 默认 Debug 设置下保持当前行为：只发送最终模型正文。
- `thinking=true` 时，最终 IM 回复包含 Thinking Debug 文本。
- `tools=true` 时，最终 IM 回复包含简短工具摘要。
- `thinking=false, tools=true` 时，只包含工具摘要，不包含 Thinking。
- `thinking=true, tools=false` 时，只包含 Thinking，不包含工具摘要。
- 输入、输出或错误过长时，工具摘要会被截断。
- `/debug` 命令会立即返回，不会调用模型 runner。

## 假设

- Debug 设置作用于当前 IM 会话。
- 默认行为保持不变：Thinking 和 Tools Use 都隐藏。
- `/debug all` 表示“进入完整 Debug 模式”，只有已经处于完整 Debug 模式时才会切换为全部关闭。
- Tools Use 展示为简短摘要，不展示完整执行日志。
- 该功能只影响 IM 渠道，不改变 Web chat 渲染。
