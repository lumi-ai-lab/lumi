# Lumi 企业微信提问人授权上下文实现说明

> 本文描述 Lumi 负责的身份识别、Policy v2 加载和 RequesterContext v2 传递。领域 capability/claims 的解释与权限实施不属于 Lumi Core。

## 1. 功能概览

```text
企业微信回调 body.from.userid
              |
              v
       Requester Policy v2
       精确身份匹配与快照
              |
              v
      RequesterContext v2
       /                 \
      v                   v
 Local Agent        Sandbox Agent
      \                   /
       v                 v
       Domain Consumer 校验并执行
```

Lumi 不安装、发现或执行领域 CLI，也不定义领域模型。

## 2. 启用方式

命令行：

```bash
lumi wecom run \
  --workspace /absolute/workspace \
  --agent claude \
  --requester-config /absolute/config/wecom-requesters.json
```

也可以使用环境变量：

```bash
LUMI_WECOM_REQUESTER_CONFIG=/absolute/config/wecom-requesters.json
```

- `--requester-config` 优先于环境变量。
- 配置路径非空即进入严格 requester policy 模式。
- 未配置 requester policy 时，保留原有 `allowFrom` 行为。
- Policy 文件应放在 Agent workspace 外；Sandbox 配置会拒绝 workspace 内或经符号链接落入 workspace 的路径。

## 3. Policy v2 示例

```json
{
  "version": 2,
  "users": [
    {
      "userId": "user-demo-001",
      "displayName": "示例用户",
      "enabled": true,
      "authorization": {
        "capabilities": ["com.example.reports.read"],
        "claims": {
          "com.example.reports": {
            "schemaVersion": 1,
            "tenantIds": ["tenant-a"]
          }
        }
      }
    },
    {
      "userId": "disabled-user",
      "displayName": "停用用户",
      "enabled": false,
      "authorization": {
        "capabilities": [],
        "claims": {}
      }
    }
  ]
}
```

完整示例见 [wecom-requesters.demo.json](./examples/wecom-requesters.demo.json)。

### 通用校验规则

- Policy 版本必须为 `2`，不自动转换 v1。
- `botId` 是可选的 audience 约束；省略或留空时 Policy 可被多个机器人复用，非空时必须与当前企业微信机器人一致。
- RequesterContext 中的 BotID 始终来自 Lumi 运行时配置，而不是 Policy。
- UserID 去除首尾空白后非空、唯一、大小写敏感。
- 启用用户至少包含一个 capability；停用用户可以使用空授权。
- capability 和 claim namespace 必须为小写点分标识，可使用数字、`_` 和 `-`：

```text
^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$
```

- capability 会去除首尾空白并拒绝重复，输入顺序保持不变。
- claim namespace 不做空白归一化，不合法时直接拒绝。
- 每个 claim namespace 的值必须为 JSON object。
- claim object 内部完全 opaque，Lumi 不拒绝其中的领域字段。
- Policy、用户及 `authorization` 层仍使用严格 JSON 解码并拒绝未知字段。

### Schema 职责边界

- `version`、`users`、`userId` 和可选 `botId` 属于企业微信 Policy 适配层。
- `authorization.capabilities` 与 `authorization.claims` 的容器结构属于 Lumi 通用授权协议。
- capability 的具体名称、claim namespace 以及 claim 内部 `schemaVersion` 和字段全部由业务消费端拥有。
- 下游消费端只接收当前用户的 RequesterContext，不读取完整 Policy 用户清单。
- 新业务只需定义 namespaced capability、版本化 claim schema 和 fail-closed 消费端，不需要修改 Lumi Core。

完整字段归属表、财务领域示例和跨 IM 渠道边界见 [企业微信提问人授权上下文架构](./wecom-requester-permission-architecture.md)。

## 4. 加载与生效时机

```text
Save / Test Connection   单独校验 policy 文件
Service.Start            读取并建立不可变快照
Runtime callback         只查询当前快照
Policy file changed      不自动热加载
Service restart          建立新快照
```

加载失败、版本错误、非空 BotID 约束不匹配或通用结构非法时，服务拒绝启动或保存配置，不回退到 `allowFrom`。

Policy revision 使用原始文件内容的 SHA-256。仅改变 JSON 排版也会产生新的 revision，便于确认运行时加载的确切文件版本。

## 5. 回调身份处理

启用严格 policy 后，回调必须满足：

- `msgid` 非空；
- `from.userid` 非空；
- `aibotid` 非空且等于配置 BotID；
- UserID 存在于启动快照且 `enabled=true`。

任一条件失败时，请求在附件下载和 Agent 调用之前被拒绝。

Lumi 使用企业微信提供的 UserID 原值作为 canonical identity，只去除首尾空白，不做大小写转换、用户名映射或格式猜测。

## 6. RequesterContext v2

授权请求构造如下 Context：

```json
{
  "version": 2,
  "requestId": "msg-demo-001",
  "policyRevision": "sha256:...",
  "principal": {
    "channel": "wecom",
    "botId": "bot-demo-001",
    "canonicalUserId": "user-demo-001",
    "displayName": "示例用户"
  },
  "audience": {
    "chatId": "chat-demo-001",
    "chatType": "group"
  },
  "authorization": {
    "capabilities": ["com.example.reports.read"],
    "claims": {
      "com.example.reports": {
        "schemaVersion": 1,
        "tenantIds": ["tenant-a"]
      }
    }
  }
}
```

实现使用 `json.RawMessage` 保存每个 namespace 的 claim object，并在保存快照及构造 Context 时深拷贝。Lumi 不将其解码为业务 Go struct。

## 7. Local 传递

每轮请求通过 ACP metadata 传递：

```text
session/prompt
  _meta
    lumi
      requesterContext
```

同时写入逐 Session 文件信封，供不读取 ACP `_meta` 的 Hook 或 Harness 使用。Agent 通过以下环境变量定位目录：

```bash
LUMI_REQUESTER_CONTEXT_DIR=/private/runtime/path
```

文件名由 ACP Session ID 的 SHA-256 生成，写入使用临时文件和原子 rename。默认兼容模式下文件权限为 `0600`、目录权限为 `0700`。

Linux 授权部署必须同时设置：

```bash
LUMI_REQUESTER_CONTEXT_ROOT=/run/lumi/requester-context
LUMI_REQUESTER_CONTEXT_READER_GID=<部署解析出的专用组 GID>
```

两项必须成对出现；只配置一项会 fail closed。启用后目录改为
`<root>/<workspace>/<agent>`，publisher 保持文件 owner，专用 reader group
作为 group owner，目录和文件分别严格使用 `0710` 与 `0640`。Lumi 配置中
的 Pi `runAsGid` 或 `supplementaryGids` 必须包含同一个 reader GID。

## 8. Sandbox 传递

```text
Host WeCom callback
        |
        v
TaskExecutePayload.requesterContext
        |
        v
device-executor
        +--> ACP _meta
        +--> session-scoped file envelope
```

- Task 只包含本轮 Context，不包含完整 policy 文件。
- Context 在 Host、device-executor 和领域 consumer 之间必须保持 v2。
- 托管 Sandbox 使用每个 Workspace 独立的宿主 source：`$LUMI_HOME/runtime/sandboxes/<workspace>/requester-context`，并将它 bind mount 到容器内 `/run/lumi/requester-context`。该 source 不得放入共享 `/lumi/runtime`；后者可由降权 Agent 控制，不能作为授权信封的信任根。
- 已存在的 Sandbox 镜像不会自动更新；升级本协议后必须重新构建镜像并重建或重启 Sandbox。

## 9. 文件生命周期

- 文件信封版本为 `1`，内部 `requesterContext.version` 为 `2`。
- 默认 TTL 为 30 分钟。
- 默认兼容模式下，Local 文件位于 `$LUMI_HOME/runtime/requester-context/<pid>/agents/<agent-id>/`。PID namespace 隔离共享同一 `LUMI_HOME` 的 Lumi 进程，也防止新进程读取崩溃进程遗留的授权文件；同一进程内 Agent 跨 Workspace 复用该目录，信封仍记录真实 `workspaceId`。
- Linux 安全部署模式下，Local 与 device-executor 都使用稳定的 `<root>/<workspace>/<agent>`；一个已启动的降权 Pi 只能绑定一个 Workspace，尝试跨 Workspace 复用会被拒绝。
- 安全部署设置只改变 Pi 的 Harness 授权路径；非 Pi Agent 保持默认兼容模式，不会因此获得 Harness 授权能力。
- 正常完成、取消和错误路径都会执行清理。
- 同一 Session 写入新 Context 后，旧 cleanup 不会删除新文件。
- 消费端必须校验 TTL、Envelope 版本、Context 版本、WorkspaceID、AgentID 和 SessionID。

## 10. 领域消费端契约

Lumi 传递的是声明，不是最终权限执行结果。消费端必须：

1. 检查 Context v2。
2. 精确检查自己要求的 capability。
3. 解析自己拥有的 namespace。
4. 校验 namespace 内的 schema 版本和全部业务约束。
5. 将用户请求与授权范围求交集。
6. 在可信服务或数据源侧强制执行。
7. 对缺失、未知或非法数据 fail closed。

Lumi 不提供 capability 注册表、领域 validator 注入点或外部 validator 命令。领域逻辑应随 Skill、MCP、Harness、查询代理或 Sandbox 镜像独立发布。

## 11. 升级与回滚

本版本是 breaking change：

- Policy v1 会被明确拒绝。
- RequesterContext v1 consumer 必须先升级。
- Policy、Lumi、device-executor 与领域 consumer 应协调发布。
- 回滚二进制时必须同步回滚 policy 和 consumer。

建议上线前分别验证：

- 合法用户的 Local 请求；
- 合法用户的 Sandbox 请求；
- 未知、停用用户以及 Policy BotID 约束不匹配的提前拒绝；
- 空 claims capability；
- 多 namespace claims；
- consumer 对缺失或错误 claim schema 的 fail-closed 行为。
