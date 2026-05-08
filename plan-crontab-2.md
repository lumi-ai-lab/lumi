# 定时任务按渠道和会话隔离修正方案

## 背景

`plan-fix-crontab.md` 中已经为 Lumi 增加了定时任务能力，对应提交为 `32f3d22cb370`。当前创建任务、查看任务列表、暂停任务等基础功能已经可用，但现有设计存在一个明显问题：定时任务没有形成严格的会话和渠道隔离。

当前表现包括：

- 其他 Web Session 可以看到之前 Session 创建的定时任务。
- 如果删除创建任务的 Session，仍然启用的定时任务触发后可能新建一个 Web 会话来展示执行结果。
- Web、微信、企业微信共用同一个 cron service，但任务缺少明确的渠道归属，存在结果交叉污染风险。

本方案参考 Ditto 的定时任务设计后，对 Lumi 的定时任务模型进行收敛：定时任务不是全局任务池，而是某个会话里的后台能力。

## 参考 Ditto 的结论

Ditto 的定时任务整体是和 conversation 绑定的。它的 `CronJob` 模型中有 `metadata.conversationId`，并提供 `listByConversation(conversationId)`、`deleteByConversation(conversationId)` 这类按会话查询和清理的能力。

Ditto 有两种执行模式：

- `existing`：任务绑定到一个已有 conversation，每次触发都写回这个 conversation。
- `new_conversation`：任务每次触发都创建一个新的 conversation，子 conversation 通过 `cronJobId` 归属到任务。

Lumi 当前不引入 Ditto 的 `new_conversation` 模式。Lumi 的第一版定时任务应该只做：

```text
job -> channel + conversationId -> 原会话触发和回写
```

也就是说：

- Web 创建的任务只属于当前 Web session。
- 微信创建的任务只属于当前微信隐藏会话。
- 企业微信创建的任务只属于当前企微隐藏会话。
- 触发结果必须回到原会话，不自动创建替代会话。

## 目标模型

每个定时任务内部自动记录以下 scope：

```text
channel + conversationId
```

`channel` 是系统内部字段，用户无感，不需要手动选择。

```text
Web 前端创建
-> channel = "web"
-> conversationId = 当前 Web session id

微信消息中由 Agent 创建
-> channel = "wechat"
-> conversationId = 当前微信隐藏会话 id
-> 保存微信主动回发所需的 conversationKey / contextToken 等元数据

企业微信消息中由 Agent 创建
-> channel = "wecom"
-> conversationId = 当前企微隐藏会话 id
-> 保存企微主动回发所需的 chatId / userId / replyContext 等元数据
```

所有 CRUD、Agent 命令、prompt 注入、触发执行、事件广播、删除清理都必须按这个 scope 隔离。

## 当前问题示意

现在的任务列表接近全局视图，容易把不同 Session 和不同渠道的任务混在一起。

```text
┌────────────────────────────────────────────────────────────┐
│ Web App                                                     │
├───────────────┬────────────────────────────────────────────┤
│ Sessions      │ Chat: Session A                            │
│               │                                            │
│  Session A    │  用户: 帮我每天 9 点总结项目                │
│  Session B    │  Agent: 已创建定时任务                     │
│               │                                            │
│  Scheduled    │                                            │
│  Tasks        │                                            │
├───────────────┴────────────────────────────────────────────┤
│ Scheduled Tasks 页面                                        │
│                                                            │
│  - Session A 创建的任务                                     │
│  - Session B 创建的任务                                     │
│  - 微信创建的任务                                           │
│  - 企业微信创建的任务                                       │
└────────────────────────────────────────────────────────────┘
```

这个体验会导致：

- 用户在 Session B 中看到 Session A 的任务。
- Web 中看到 IM 渠道创建的任务。
- Agent 在当前会话中通过 `[CRON_LIST]` 看到不属于当前会话的任务。
- 删除原 Session 后，任务仍然可能继续触发并创建新的 Web 会话。

## 调整后的前端体验

聊天页中的定时任务入口只表达当前会话的任务状态。

```text
┌────────────────────────────────────────────────────────────┐
│ Web App                                                     │
├───────────────┬────────────────────────────────────────────┤
│ Sessions      │ Chat: Session A                            │
│               │                                            │
│  Session A    │  用户: 帮我每天 9 点总结项目                │
│  Session B    │  Agent: 已创建定时任务                     │
│               │                                            │
│  Scheduled    │  [Alarm] 当前会话有 1 个定时任务            │
│  Tasks        │                                            │
└───────────────┴────────────────────────────────────────────┘
```

点击 `Scheduled Tasks` 后，列表默认只展示当前 Session 的任务。

```text
┌────────────────────────────────────────────────────────────┐
│ Scheduled Tasks                                             │
│ Scope: Session A                                            │
├────────────────────────────────────────────────────────────┤
│  项目日报                                                   │
│  Every 24 hours                                             │
│  Next: 2026-05-10 09:00                                     │
│                                                            │
│  [Pause] [Run Now] [Edit] [Delete]                          │
└────────────────────────────────────────────────────────────┘
```

切到 Session B 后，任务列表也随之切换。

```text
┌────────────────────────────────────────────────────────────┐
│ Scheduled Tasks                                             │
│ Scope: Session B                                            │
├────────────────────────────────────────────────────────────┤
│  No scheduled tasks in this chat                            │
└────────────────────────────────────────────────────────────┘
```

## 创建任务弹窗

当前创建弹窗允许用户选择 `Target Conversation`，这会让用户误以为任务可以跨会话绑定。

调整前：

```text
┌──────────────────────────────┐
│ Create Scheduled Task         │
├──────────────────────────────┤
│ Name                          │
│ Prompt                        │
│ Agent                         │
│ Workspace                     │
│ Target Conversation  [select] │
│ Schedule                      │
│                              │
│ [Cancel] [Save]               │
└──────────────────────────────┘
```

调整后不再让用户手动选择会话，任务自动绑定当前会话。

```text
┌──────────────────────────────┐
│ Create Scheduled Task         │
├──────────────────────────────┤
│ Name                          │
│ Prompt                        │
│ Agent                         │
│ Workspace                     │
│                              │
│ Bound to current chat          │
│ Session A                     │
│                              │
│ Schedule                      │
│                              │
│ [Cancel] [Save]               │
└──────────────────────────────┘
```

`Bound to current chat` 是只读提示，不是选择器。

## 删除会话弹窗

如果当前会话没有绑定定时任务，删除确认保持普通提示。

```text
┌──────────────────────────────┐
│ Delete Chat                   │
├──────────────────────────────┤
│ Are you sure you want to      │
│ delete this chat?             │
│                              │
│ [Cancel] [Delete]             │
└──────────────────────────────┘
```

如果当前会话绑定了定时任务，删除确认必须明确提示会连带删除任务。

```text
┌──────────────────────────────┐
│ Delete Chat                   │
├──────────────────────────────┤
│ This chat has 2 scheduled     │
│ tasks. Deleting this chat     │
│ will also delete those tasks. │
│                              │
│ [Cancel] [Delete Chat & Tasks]│
└──────────────────────────────┘
```

Web 删除会话时需要用户确认；微信和企业微信内部清理隐藏会话时不弹窗，直接静默级联删除对应任务。

## 后端改造

### Cron Job 模型

扩展 `backend/internal/cron/types.go` 中的 `Job`：

```go
type Job struct {
    ID             string
    Name           string
    Enabled        bool
    Channel        string // web | wechat | wecom
    WorkspaceID    string
    AgentID        string
    ConversationID string
    Schedule       Schedule
    Prompt         string
    Target         JobTarget
    State          JobState
    CreatedAt      int64
    UpdatedAt      int64
}

type JobTarget struct {
    WeChat *WeChatCronTarget
    WeCom  *WeComCronTarget
}
```

目标字段用于保存 IM 主动回发所需信息。Web 任务不需要额外 target。

示例：

```go
type WeChatCronTarget struct {
    ConversationKey string
    ContextToken    string
}

type WeComCronTarget struct {
    ChatID   string
    ChatType string
    UserID   string
    ReqID    string
}
```

具体字段可以按现有微信/企微发送接口需要收敛，原则是：后台触发时必须能主动把结果发回原 IM 对话。

### Cron Service

新增按 scope 操作能力：

```go
ListByScope(channel, conversationID string) []Job
DeleteByScope(channel, conversationID string) error
CountByScope(channel, conversationID string) int
GetScoped(channel, conversationID, jobID string) (Job, bool)
```

所有 pause、resume、delete、run now 等操作都必须校验 scope。跨 scope 的任务对调用方表现为 not found 或 forbidden。

### Web API

Web 侧不允许前端传入 `channel`。

- `POST /api/cron/jobs`
  - 固定写入 `channel="web"`。
  - `conversationId` 必须是当前 Web session id。
  - 不允许创建空 `conversationId` 的新任务。

- `GET /api/cron/jobs?conversationId=<id>`
  - 返回 `channel="web" + conversationId=<id>` 的任务。

- `DELETE /api/sessions/{id}`
  - 删除 session 前后由后端级联删除 `channel="web" + conversationId=id` 的任务。

### Agent Cron 命令

当前 Agent 命令执行上下文需要带上：

```go
type cronCommandContext struct {
    Channel        string
    ConversationID string
    AgentID        string
    WorkspaceID    string
    Target         JobTarget
}
```

不同入口自动填充：

- Web chat: `Channel="web"`
- WeChat chat: `Channel="wechat"`，并填充微信 target
- WeCom chat: `Channel="wecom"`，并填充企微 target

`[CRON_CREATE]` 创建任务时使用当前上下文的 scope。

`[CRON_LIST]`、暂停、恢复、删除、立即运行只操作当前 scope 内任务。

`WithConversationInstructionAndJobs` 只注入当前 scope 的 jobs，不再注入全量 jobs。

### 执行回写

执行入口根据 `job.Channel` 路由。

```text
channel = web
-> 写 Web session
-> 发送 Web cron SSE

channel = wechat
-> 写微信隐藏会话
-> 通过微信 client 主动发送结果到原 conversationKey

channel = wecom
-> 写企微隐藏会话
-> 通过企微 sender 主动发送结果到原 replyContext
```

所有执行都遵循：

- 先写入可见 `cron_trigger`。
- 再发送 hidden prompt 给 Agent。
- assistant 输出、tool call、错误信息继续落到绑定会话。
- 如果绑定会话不存在，新逻辑不自动创建替代会话；任务应删除或标记失败。

## 前端改造

### use-chat-controller

- `loadCronJobs` 改为按当前 `currentSessionId` 拉取。
- `currentSessionId` 切换后重新加载当前会话任务。
- `/api/cron/events` 只处理 Web session 相关事件。
- 收到 `chat_event` 时，如果 `conversationId` 不是当前 Web session 或已缓存 session，不应该污染其他 session。

### ScheduledTasksPage

- 默认显示当前会话任务。
- 页面标题或副标题显示当前 scope，例如 `Scope: Session A`。
- 没有当前 session 时提示先创建或选择会话。
- 不显示微信/企微任务。

### CronTaskDialog

- 移除 `Target Conversation` 下拉选择。
- 创建时自动使用当前 session。
- 添加只读说明：`Bound to current chat`。
- 如果没有当前 session，不允许创建任务。

### Sidebar 删除确认

- 打开删除弹窗前或弹窗打开时，统计当前 session 绑定任务数量。
- 有任务时使用增强文案和按钮。
- 确认后仍调用删除 session API，由后端负责级联删除任务。

## 兼容策略

旧 `~/.lumi/cron/jobs.json` 中没有 `channel` 字段的任务按以下规则加载：

- 默认补 `channel="web"`。
- 已有 `conversationId` 的任务继续绑定原 Web session。
- 空 `conversationId` 的旧任务保留兼容展示，但新创建任务不再允许空绑定。
- 启动时对 orphan jobs 做清理：绑定会话不存在的任务停止计时并删除，避免后台继续触发到错误上下文。

## 测试计划

### Web

- Session A 创建任务后，Session B 的任务列表为空。
- Session A 创建任务后，Session B 中 Agent 执行 `[CRON_LIST]` 看不到 A 的任务。
- Session A 的任务触发后，只写回 Session A。
- 删除 Session A 时，如果有任务，弹窗提示会连带删除任务。
- 确认删除后，Session A 和对应任务都被删除。

### 微信

- 微信中创建任务后，Web 任务列表看不到该任务。
- 微信任务触发后，结果主动发送回原微信对话。
- 微信任务不会写入 Web session store。

### 企业微信

- 企业微信中创建任务后，Web 和微信 scope 都看不到该任务。
- 企业微信任务触发后，结果主动发送回原企微对话。
- 企业微信任务不会写入 Web session store。

### Scope 安全

- 对其他 session 的任务执行 pause/resume/delete/run now 返回 not found 或 forbidden。
- 相同 `conversationId` 字符串在不同 channel 下不会互相影响。
- `/api/cron/events` 不向 Web 前端推送 IM 任务结果。

## 结论

Lumi 的定时任务应定义为：

```text
某个 channel + conversationId 下的后台任务
```

不是：

```text
全局任务池 + 尽量路由到某个 session
```

本方案不引入 Ditto 的 `new_conversation`，只保留更符合 Lumi 当前产品语义的原会话触发和回写模型。
