# 增加定时任务功能并规避 Ditto 队列展示 Bug

## 摘要

当前 Lumi 没有完整的定时任务能力。参考项目 Ditto 的可取部分，不是它前端里那套输入队列，而是它的后端调度器、后台执行器、消息持久化和主动广播链路。

Ditto 已经证明了一点：定时任务不能被建模成“等用户再发一次 prompt 才消费的前端待发送输入”。任务到点后，必须由后端直接执行，并把触发消息、流式输出和最终结果主动推给前端。否则就会出现参考项目里的那个 Bug：任务实际上已经被安排甚至触发了，但 UI 不会自动显示，直到用户再输入一次 prompt，前端才像消费本地队列一样把它拿出来。

Lumi 当前 `/api/chat` 的 SSE 只存在于用户主动发起一次聊天请求期间。后台没有独立的全局事件流可以把“定时任务触发后的新消息”推给当前 Web UI。这是本次功能实现里必须补齐的核心能力。

目标行为：

- 用户可以创建、查看、暂停、恢复、删除、立即运行定时任务。
- 定时任务到点后由后端自动执行，不依赖前端输入框，也不依赖前端命令队列。
- 任务触发后，前端会自动看到新的会话变化、触发提示和流式输出，不需要用户再手工输入 prompt。
- 定时 prompt 本身默认不作为普通用户消息展示，避免 UI 像“用户刚刚手动发了一条消息”。
- MVP 先保证可靠性，不在第一版实现 Ditto 的完整技能系统、错过执行补偿和复杂 cron 表达式。

## Ditto 调研结论

### 参考项目里真正可复用的思路

- `CronService` 负责加载任务、启动 timer、调度执行、更新任务状态。
- `WorkerTaskManagerJobExecutor` 在任务触发时直接获取或创建 conversation/task，并调用 agent 执行。
- 执行开始前，Ditto 会先写入一条 `cron_trigger` 类型的可见消息，用于前端展示“这是一次定时任务触发”。
- 实际发给 agent 的定时 prompt 是 hidden message，不直接作为普通聊天内容显示。
- 任务执行相关状态通过 IPC event emitter 主动广播给前端，而不是等前端自己轮询或者等输入框重新触发。

### 参考项目里需要规避的点

- 不要把定时任务复用成前端 send box queue 或 command queue。
- 不要要求“用户发新 prompt”作为刷新/消费定时任务结果的前置条件。
- 不要把定时执行 prompt 和用户手工输入的 message 混为同一类 UI 事件。

### 对 Lumi 的直接启发

- Lumi 要做的是“后端后台任务 + 全局前端订阅”，不是“聊天输入框增强版”。
- 定时任务执行链路应该复用现有聊天主链路的 agent 调用逻辑，但要把 SSE 输出目标抽象出来，支持普通聊天和后台任务两种 sink。
- 前端需要一个与当前 `/api/chat` POST SSE 分离的长期订阅通道，用来接收后台任务带来的 session 和消息更新。

## 实现改动

### 后端定时任务子系统

- 新增 cron 子系统，职责拆分保持简单明确：
  - `CronJobStore`：负责 job 持久化。
  - `CronService`：负责内存任务加载、timer 生命周期和调度。
  - `CronRunner` 或等价执行器：负责真正触发 Lumi 现有聊天执行链路。
- 持久化位置使用 `~/.lumi/cron/jobs.json`。
- `CronJob` 数据结构固定为：
  - `id`
  - `name`
  - `enabled`
  - `workspaceId`
  - `agentId`
  - `conversationId`
  - `schedule`
  - `prompt`
  - `state`
  - `createdAt`
  - `updatedAt`
- `state` 至少包含：
  - `lastRunAt`
  - `nextRunAt`
  - `lastStatus`
  - `lastError`
  - `runCount`
- `schedule` 在 MVP 只支持两类：
  - `type: "once"`，字段 `runAt`
  - `type: "interval"`，字段 `everySeconds`
- `everySeconds` 最小值固定为 60，避免做秒级频繁调度导致实现和 UI 噪音过大。
- 服务启动时加载所有 enabled jobs，并为每个 job 建立 `time.Timer` 或重建下一次触发。
- 更新、暂停、恢复、删除任务时，必须同步重建或销毁对应 timer。

### 后端聊天执行链路抽取

- 从 `backend/internal/api/chat.go` 中把“准备会话 + 调 agent + 处理 notification + 持久化消息 + 产出 SSE 事件”的主流程抽取成可复用 runner。
- runner 需要接受一个事件 sink，用来决定事件写到哪里：
  - 普通用户聊天时，sink 继续写当前 `/api/chat` 请求的 SSE。
  - 定时任务后台执行时，sink 改为写全局 cron/chat event bus。
- 本次不要复制一套新的 agent 调用逻辑。核心执行必须仍复用现有 `handleLocalChat` / `handleDeviceChat` 的能力，只把外层编排重构成可复用组件。
- 定时任务执行时仍然支持：
  - 本地 workspace agent
  - remote workspace/device agent
  - 现有权限确认和 tool call 处理

### 后端全局事件通道

- 新增 `GET /api/cron/events` SSE 接口，作为前端的长期订阅源。
- 该全局事件流至少广播以下事件：
  - `job_created`
  - `job_updated`
  - `job_deleted`
  - `session_updated`
  - `chat_event`
- `session_updated` 用于通知前端某个 session 的摘要信息发生了变化，刷新侧边栏和当前会话元信息。
- `chat_event` 用于通知前端某个 conversation 新增了触发提示、流式文本、tool call、done、error 等事件。
- 事件总线需要支持多个订阅者，模式参考现有 `/api/setup/subscribe` 的 SSE subscriber 管理方式。
- 后台任务执行期间，所有原本只写给当前 `/api/chat` SSE 的流式事件，都要同步转换为 `chat_event` 并广播到这个全局通道。

### 定时任务执行时的消息模型

- 扩展 `backend/internal/conversation/manager.go` 的 `Message` 结构，新增可选字段：
  - `Kind string`
  - `Hidden bool`
  - `Cron *CronMessageMeta`
- `CronMessageMeta` 至少包含：
  - `jobId`
  - `jobName`
  - `triggeredAt`
- 定时任务开始执行时，先写入一条可见消息：
  - `role: "assistant"`
  - `kind: "cron_trigger"`
  - `content` 使用简洁提示文案
  - `cron` 填充触发元信息
- 真正发给 agent 的 prompt 作为 hidden user message：
  - `role: "user"`
  - `hidden: true`
  - `content` 为调度 prompt
  - `cron` 填充触发元信息
- assistant 正常输出、tool call、错误信息仍然沿用现有消息结构，只在需要时补 `cron` 元信息。
- `persistConversation` 必须保存上述新字段，旧会话数据没有这些字段时仍然要兼容读取。

### 会话与运行策略

- MVP 默认支持“复用已有会话”。
- 创建定时任务时如果当前有会话，默认把 `conversationId` 绑定到当前会话。
- 如果创建任务时没有当前会话，或者任务明确不绑定会话，则在首次运行时创建新 session，并将后续运行继续复用这个 session。
- 同一个 `conversationId` 在已有 agent 执行进行中时，不允许再并发启动新的定时执行。
- 发生 busy 冲突时，MVP 行为固定为：
  - 本次执行记为 skipped
  - 更新 `lastStatus` 和 `lastError`
  - 不做重试队列
- once 任务执行完成后自动置为 disabled。

### HTTP API

- 新增以下接口：
  - `GET /api/cron/jobs`
  - `POST /api/cron/jobs`
  - `PUT /api/cron/jobs/{id}`
  - `DELETE /api/cron/jobs/{id}`
  - `POST /api/cron/jobs/{id}/run`
  - `GET /api/cron/events`
- `POST /api/cron/jobs` 请求体固定包含：
  - `name`
  - `prompt`
  - `agentId`
  - `workspaceId`
  - `conversationId`
  - `schedule`
- `PUT /api/cron/jobs/{id}` 支持更新：
  - `name`
  - `prompt`
  - `enabled`
  - `schedule`
  - `agentId`
  - `workspaceId`
  - `conversationId`
- `POST /api/cron/jobs/{id}/run` 立即触发一次后台执行，不改变 schedule 本身。

### React/Next 前端

- 在 `web/src/lib/types.ts` 中新增定时任务相关类型：
  - `CronJob`
  - `CronSchedule`
  - `CronJobState`
  - `CronEvent`
- 扩展 `Message` 类型，增加：
  - `kind?: "text" | "cron_trigger"`
  - `hidden?: boolean`
  - `cron?: { jobId: string; jobName: string; triggeredAt: number }`
- 在 `use-chat-controller.tsx` 中新增全局 cron 事件订阅：
  - 页面初始化后创建 `EventSource('/api/cron/events')`
  - 收到 `session_updated` 时刷新 `sessions` 和必要的 `sessionDetails`
  - 收到 `chat_event` 时复用现有 `handleStreamEvent` 逻辑，把事件路由到目标 session
- 后台任务触发的消息进入前端后，不经过 composer，也不设置为“待发送消息”。
- `commitStreamItems` 后写入本地 session cache 时，要保留 `kind`/`cron`/`hidden` 等扩展字段。

### React/Next UI 展示

- 在聊天区新增对 `message.kind === "cron_trigger"` 的展示分支。
- `cron_trigger` 使用居中、弱强调样式，文案只表达“某个定时任务已触发”，不要伪装成普通 assistant 正文。
- `hidden` message 不进入 `buildVisibleMessages`。
- 侧边栏 session 列表在收到 `session_updated` 后要自动刷新排序，确保后台任务刚执行过的会话会提升到顶部。
- UI 形态对齐 Ditto：聊天页闹钟入口 + 定时任务列表视图 + 定时任务详情视图 + 创建/编辑弹窗。
- 聊天主界面右上角增加 `Clock` 图标入口，和 Share / Settings 同级：

```text
+--------------------------------------------------------------------------------+
| Sidebar                 | Chat                                                  |
|-------------------------|-------------------------------------------------------|
| Workspace               |                                      [Share] [Clock] [Settings]
|                         |                                                       |
| Chats                   |   assistant message...                                |
| - Chat A                |                                                       |
| - Chat B                |   +------------------------------------------------+  |
| - Chat C                |   |  alarm  定时任务「日报总结」已触发        >     |  |
|                         |   +------------------------------------------------+  |
|                         |                                                       |
|                         |                         +-------------------------+   |
|                         |                         | composer                |   |
+--------------------------------------------------------------------------------+
```

- 闹钟入口状态固定为：

```text
No task on current chat:
  [Clock gray dot]
      hover/click -> 引导用户通过 Agent 创建定时任务

Task active:
  [Clock green dot]
      click -> /scheduled/{jobId}

Task paused:
  [Clock orange dot]
      click -> /scheduled/{jobId}

Task error:
  [Clock red dot]
      click -> /scheduled/{jobId}
```

- 新增任务列表视图，路由建议为 `/scheduled`：

```text
+--------------------------------------------------------------------------------+
| Sidebar                 | Scheduled Tasks                                       |
|-------------------------|-------------------------------------------------------|
| Workspace               |  alarm  Scheduled Tasks                 [+ New Task]  |
|                         |  Background tasks that run prompts automatically.     |
| Chats                   |                                                       |
| - Chat A                |  +--------------------+ +--------------------+        |
| - Chat B                |  | 日报总结     Active| | 代码巡检     Paused|        |
|                         |  | Every 1 day         | | Every 60 min       |        |
| Scheduled               |  | Next: 09:00         | | Next: -           |        |
| - Scheduled Tasks       |  |        [toggle] [x] | |        [toggle] [x]|        |
|                         |  +--------------------+ +--------------------+        |
+--------------------------------------------------------------------------------+
```

- 新增任务详情视图，路由建议为 `/scheduled/{jobId}`：

```text
+--------------------------------------------------------------------------------+
| Sidebar                 | < Back to Scheduled Tasks                              |
|-------------------------|-------------------------------------------------------|
| Workspace               |  日报总结                         [Edit] [Delete] [Run Now]
|                         |  Active    Next run: 2026-05-09 09:00                 |
| Chats                   |                                                       |
| - Chat A                |  Agent                                                |
| - Chat B                |  claude                                               |
|                         |                                                       |
| Scheduled               |  Target                                               |
| - Scheduled Tasks       |  Workspace: default                                   |
|                         |  Conversation: Chat A                                 |
|                         |                                                       |
|                         |  Prompt                                               |
|                         |  +------------------------------------------------+   |
|                         |  | Summarize yesterday's project changes...       |   |
|                         |  +------------------------------------------------+   |
|                         |                                                       |
|                         |  Schedule                                             |
|                         |  [enabled toggle] Every 1 day                         |
|                         |                                                       |
|                         |  Last Run                                             |
|                         |  Success, 2026-05-08 09:00                            |
+--------------------------------------------------------------------------------+
```

- 创建/编辑弹窗字段固定为：名称、Prompt、Agent、Workspace、目标会话、执行方式。
- 执行方式在 MVP 仅提供：一次性执行（日期时间）和每 N 分钟执行（数字输入）。

```text
+------------------------------------------------------------+
| Create Scheduled Task                                  [x] |
|------------------------------------------------------------|
| Name                                                       |
| [ 日报总结                                             ]   |
|                                                            |
| Prompt                                                     |
| [ Summarize yesterday's project changes...             ]   |
| [                                                        ] |
|                                                            |
| Agent                                                      |
| [ claude v ]                                               |
|                                                            |
| Workspace                                                  |
| [ default v ]                                              |
|                                                            |
| Target Conversation                                        |
| [ Current chat: Chat A v ]                                 |
|                                                            |
| Schedule                                                   |
| (o) Once at     [ 2026-05-09 09:00 ]                       |
| ( ) Every       [ 60 ] minutes                             |
|                                                            |
|                              [Cancel] [Save]               |
+------------------------------------------------------------+
```

- 聊天区 `cron_trigger` 展示为可点击的弱强调卡片，点击进入 `/scheduled/{jobId}`：

```text
+------------------------------------------------------------+
| alarm  定时任务「项目日报」已触发                    >     |
+------------------------------------------------------------+

assistant streaming output...
```

### 对话式定时任务管理

- 保留和 Ditto 一样的对话式管理入口：用户可以直接对 Agent 说“每天早上 9 点帮我总结昨天这个项目的变化”。
- 实现方式和 Ditto 的关键点一致：在发给 Agent 的 prompt 中注入 Lumi 定时任务协议，让 Agent 输出 `[CRON_CREATE]`、`[CRON_LIST]` 等内部命令，再由后端解析执行。
- Lumi 不能直接照搬 Ditto 的 cron skill 文案；Ditto 会教 Agent 输出 cron 表达式，而 Lumi 当前只支持 `once:<YYYY-MM-DD HH:mm>` 和 `interval:<minutes>`。
- Agent 创建任务时必须通过后端 cron 命令执行器落到 `CronService`，不允许把任务塞进前端 composer/send queue。
- Web、企业微信、微信都要走同一套协议和后端执行器：

```text
+-------------------+      +----------------------------+
| User says reminder| ---> | Prompt + Lumi cron protocol |
+-------------------+      +----------------------------+
                                      |
                                      v
                            +----------------------------+
                            | Agent emits [CRON_*]       |
                            +----------------------------+
                                      |
                                      v
                            +----------------------------+
                            | Backend strips + executes  |
                            +----------------------------+
                                      |
                                      v
                            +----------------------------+
                            | User sees natural response |
                            +----------------------------+
```

- 对话式管理覆盖：
  - 创建任务
  - 查看任务列表
  - 暂停任务
  - 恢复任务
  - 删除任务
  - 立即运行
- 编辑任务仍优先通过显式 UI 和 HTTP API 完成；对话中如需修改任务，先列表确认目标，再删除旧任务并创建新任务。
- 对话式创建成功后，聊天区展示自然语言确认，不展示底层命令或 JSON：

```text
User:
  每天早上 9 点帮我总结昨天这个项目的变化

Assistant:
  已创建定时任务「项目日报」。
  下次运行时间：2026-05-09 09:00。
```

## 明确不做

- 不实现完整 cron 表达式。
- 不实现 missed job 补偿执行。
- 不实现桌面系统通知。
- 不实现 Ditto 的 skill-suggest / SKILL.md 文件加载机制；只实现等价的 Lumi 内置 prompt 协议注入。
- 不实现复杂的“新建会话 / 每次新建会话 / 复用不同会话策略”多模式切换。
- 不实现前端本地命令队列和定时任务的混用。

## 测试计划

后端测试：

- 创建、更新、暂停、恢复、删除 job 后，持久化内容和内存 timer 状态正确。
- once 任务到点后执行一次，并自动 disabled。
- interval 任务重复执行时，`nextRunAt`、`lastRunAt`、`runCount` 正确更新。
- conversation busy 时，本次执行被 skipped，不会并发跑两个任务。
- 定时执行会写入一条 `cron_trigger` 可见消息和一条 hidden prompt 消息。
- assistant 输出和 tool call 仍会正常持久化到 session。
- `/api/cron/events` 订阅者能收到 job 和 chat 事件。
- Web/企业微信/微信对话中 Agent 输出 `[CRON_CREATE]` 时，后端能创建任务，并且最终消息不包含原始 `[CRON_*]`。
- cron prompt instruction 不包含 Ditto cron 表达式示例，避免 Agent 输出 Lumi 当前不支持的格式。

前端测试或手动验证：

- 不操作输入框时，后台定时任务触发后，当前打开页面会自动出现会话变化和新消息。
- `cron_trigger` 能显示，hidden prompt 不显示。
- 当前会话打开时，流式输出会自动渲染。
- 非当前会话触发时，侧边栏列表会自动刷新并重排。
- 创建、暂停、恢复、删除、立即运行任务的 UI 和 API 行为一致。

回归检查：

- 普通 `/api/chat` 交互保持原样。
- 远程 device chat 仍可正常工作。
- 分享页、workspace 预览、权限确认、tool call 渲染不受影响。

## 推荐实现顺序

1. 先定义 `CronJob`、持久化层和 `CronService`，把 timer 生命周期跑通。
2. 抽取现有聊天执行 runner，把普通聊天 SSE 和后台任务 event sink 解耦。
3. 增加 `/api/cron/events` 和定时任务 CRUD API。
4. 扩展 conversation message 结构，补 `cron_trigger` 和 hidden prompt 持久化。
5. 接入 React/Next 全局订阅，让后台任务结果自动进前端会话状态。
6. 最后再补任务管理 UI 和相关测试。
