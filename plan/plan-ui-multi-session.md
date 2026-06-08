# Web 端多会话并发发送方案

## 背景

后端已经支持同一个 device 上的任务队列：

```text
不同会话 -> 同一 device -> 后端按 device 串行排队执行
```

但当前 Web 前端仍然使用全局发送状态：

```ts
isSending: boolean
sendingSessionId: string | null
pendingPermission: PermissionRequest | null
```

`ChatPanel` 中 composer 的禁用条件包含全局 `isSending`：

```tsx
disabled={isSending || !currentWorkspace || isWorkspaceBlocked}
```

因此只要任意一个会话正在执行，用户切换到另一个会话后也无法发送消息。这会阻止 Web 端验证和使用后端的 device queue 能力。

## 目标

1. Web 端允许不同会话同时发起消息。
2. 同一个会话仍然禁止重复发送，避免同一 conversation 内消息乱序。
3. 每个会话的流式内容、loading、permission request 和取消操作按 session 隔离。
4. 后端 device queue 继续负责同一 device 上不同会话的串行执行。
5. v1 不做复杂的排队位置、全局任务中心或 sidebar 运行态增强，只保证基础并发体验正确。

## 非目标

1. 不改变后端 device queue 语义。
2. 不允许同一 session 内多条消息并发执行。
3. 不实现跨浏览器 tab 的运行态同步。
4. 不引入持久化前端任务队列；刷新页面后仍以服务端 session 状态为准。

## 核心改动

### 1. 发送状态改为按 session 管理

把全局发送状态从：

```ts
isSending: boolean
sendingSessionId: string | null
```

调整为按 session 记录：

```ts
sendingSessionIds: Record<string, true>
```

派生当前会话发送状态：

```ts
const currentSessionIsSending = currentSessionId
  ? Boolean(sendingSessionIds[currentSessionId])
  : false
```

`ChatPanel` 和 `ChatComposer` 只根据当前 session 判断是否禁用：

```tsx
disabled={currentSessionIsSending || !currentWorkspace || isWorkspaceBlocked}
isSending={currentSessionIsSending}
```

对于尚未创建 session 的新会话，发送流程需要先创建 session，再把该 session 标记为 sending。

### 2. streamItems 保持按 session 隔离

当前已有：

```ts
streamItemsBySession: Record<string, StreamItem[]>
```

实现时需要避免在发送新会话消息时调用会影响其他会话的全局清理逻辑。

要求：

1. 发送 session A 时，只 commit/clear session A 的 stream items。
2. 切到 session B 并发送时，不清除 session A 正在流式显示的内容。
3. 收到 SSE event 时，继续使用请求发起时捕获的 `targetSessionId` 写入对应 session。

建议把现有 `commitStreamItems()`、`clearStreamItems()` 拆成 session 版本：

```ts
commitStreamItemsForSession(sessionId: string)
clearStreamItemsForSession(sessionId: string)
```

保留当前会话快捷包装：

```ts
commitCurrentStreamItems()
clearCurrentStreamItems()
```

### 3. permission request 改为按 session 管理

当前全局：

```ts
pendingPermission: PermissionRequest | null
```

需要改为：

```ts
pendingPermissionBySession: Record<string, PermissionRequest>
```

派生当前会话 permission：

```ts
const currentPendingPermission = currentSessionId
  ? pendingPermissionBySession[currentSessionId] || null
  : null
```

处理规则：

1. 收到 `permission_request` event 时，写入对应 `targetSessionId`。
2. 当前 UI 只展示当前会话的 pending permission。
3. 用户确认 permission 后，只清除当前 session 的 permission。
4. 如果同一 session 收到 `done` 或 `error`，清除该 session 的 permission。

### 4. 取消操作按当前 session 执行

当前取消依赖全局：

```ts
agentSessionId
currentAgent
```

多会话并发后，需要按 session 保存远端 agent session：

```ts
agentSessionIdBySession: Record<string, string>
agentBySession: Record<string, string>
```

取消当前会话时：

1. 读取 `currentSessionId`。
2. 找到该 session 的 `agentSessionId` 和 agent。
3. 调用 `/api/chat/cancel`。
4. 只结束当前 session 的 sending/stream 状态。

如果当前 session 没有运行中的 agent session，取消按钮不可用或直接返回 false。

### 5. SSE 生命周期按请求隔离

`sendCurrentMessage` 发起请求时需要捕获：

```ts
targetSessionId
targetWorkspaceId
targetAgent
targetDeviceId
```

后续所有回调都使用这些捕获值，不再依赖 `currentSessionIdRef.current` 等当前 UI 状态。

请求结束条件：

1. 收到 `done`：清除该 session sending 状态，commit stream，清除 permission。
2. 收到 `error`：清除该 session sending 状态，保留或提交已收到 stream，清除 permission。
3. fetch 失败或 abort：清除该 session sending 状态。

## UI 行为

### 同一会话

1. 会话 A 正在执行时，会话 A composer 禁用。
2. 会话 A 显示 loading dots 或 permission card。
3. 用户不能在会话 A 再发第二条消息。

### 不同会话

1. 会话 A 正在执行时，切到会话 B。
2. 会话 B composer 可用。
3. 会话 B 可以发送消息。
4. 如果会话 A 和 B 使用同一个 device，后端会让 B 等待 A 完成。
5. 切回会话 A 时，应能看到 A 自己的 stream/loading/permission 状态。

### 权限请求

1. 会话 A 的 permission request 只在会话 A 显示。
2. 会话 B 的 permission request 只在会话 B 显示。
3. 如果用户切换会话，不应看到其他会话的 permission card。

## 测试计划

### 单元/组件测试

1. session A sending 时，session A composer disabled，session B composer enabled。
2. `permission_request` 写入指定 session，不覆盖其他 session。
3. `done/error` 只清理对应 session 的 sending、stream 和 permission。
4. cancel 只读取并取消当前 session 的 agent session。

### E2E 测试

1. 创建两个不同 Web 会话，选择同一个 sandbox/remote device。
2. 会话 A 发送耗时任务，例如：

```text
执行 sleep 60 && echo A done
```

3. 切到会话 B，确认输入框可用并发送消息。
4. 会话 B 不应出现 `Device is busy`。
5. 会话 A 完成后，会话 B 继续执行并收到 `done`。
6. 同一会话 A 在任务未完成时再次发送，仍应被前端禁用或后端返回 conversation busy。

### 回归测试

1. 普通单会话聊天流式输出不变。
2. permission confirm 仍能正常发送。
3. cancel 当前会话仍能取消正在运行的任务。
4. 切换会话不会丢失当前 session 的 stream 内容。
5. cron event/session update 不应污染当前手动发送状态。

## 风险与约束

1. 最大风险是全局状态拆分不完整，导致 stream 或 permission 串会话。
2. v1 不要求多个会话同时在后台持续渲染到 sidebar，只要求切换到对应会话时状态正确。
3. 多个会话同时请求同一 device 会增加后端排队请求数量，依赖后端 5 分钟队列超时兜底。
4. 浏览器刷新会丢失前端运行态，这是现有架构限制，v1 不处理。

## 建议实施顺序

1. 先把 `isSending` 改成 `sendingSessionIds`，只修 composer disabled 和 loading dots。
2. 再把 `pendingPermission` 改成 `pendingPermissionBySession`。
3. 拆分 stream commit/clear 为 session 维度，确保并发不会清错会话。
4. 最后调整 cancel 逻辑为按当前 session 取消。
5. 每一步都跑现有 chat E2E/组件测试，并补充多会话并发用例。

## 验收标准

1. Web 单 tab 内，会话 A 正在执行时，切到会话 B 可以发送消息。
2. 同一会话仍不能并发发送。
3. 两个不同会话同时打同一 device 时，前端不阻止第二个请求，后端排队执行。
4. stream、permission、cancel 不跨 session 串线。
5. 现有单会话聊天、远端设备聊天、sandbox 聊天行为不回退。
