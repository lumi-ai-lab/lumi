# 企业微信提问人授权上下文架构

## 1. 目标与边界

Lumi 负责把 IM 渠道认证出的提问人身份和不可变授权快照安全传给 Agent。Lumi Core 不定义任何业务 capability、数据范围或工具生命周期。

```text
WeCom / Other IM
        |
        v
+---------------------------+
| Lumi Core                 |
| - authenticated identity  |
| - immutable policy        |
| - opaque capabilities     |
| - namespaced claims       |
+-------------+-------------+
              |
      +-------+-------+
      |               |
      v               v
 Domain Consumer   Other Consumer
 validate claims   validate claims
 enforce policy    enforce policy
      |
      v
 Skill / MCP / Query Proxy / Sandbox Image
```

Lumi 的职责到 RequesterContext 传递完成为止。业务消费端必须自行解释 capability 和 claims，并在调用数据源或工具之前 fail closed。

## 2. 身份来源

企业微信回调中的 `body.from.userid` 是授权查询键：

```text
body.from.userid
        |
        | trim surrounding whitespace
        v
canonicalUserId
        |
        v
immutable requester policy snapshot
```

- UserID 大小写敏感，Lumi 不根据长度或格式推断身份类型。
- `displayName` 来自 policy，只用于展示，不能作为鉴权键。
- `body.aibotid` 必须与当前 BotID 一致。
- 无法获得可用于匹配的明文 UserID 时，应在身份转换层解决；Lumi 不猜测或降级匹配。

## 3. Requester Policy v2

Policy v2 将身份字段和授权字段分开：

```json
{
  "version": 2,
  "users": [
    {
      "userId": "user-demo-001",
      "displayName": "示例用户",
      "enabled": true,
      "authorization": {
        "capabilities": [
          "com.example.reports.read",
          "com.example.reports.export"
        ],
        "claims": {
          "com.example.reports": {
            "schemaVersion": 1,
            "tenantIds": ["tenant-a"]
          }
        }
      }
    }
  ]
}
```

### 3.1 Lumi 校验的内容

- `version` 必须为 `2`。
- `botId` 是可选的 audience 约束；非空时必须与运行中的 BotID 精确一致，省略或留空时允许跨机器人复用。
- RequesterContext 的 `principal.botId` 始终使用运行时认证的 BotID，Policy 不能覆盖它。
- UserID 非空且去除首尾空白后不得重复。
- 启用用户至少有一个 capability。
- capability 去除首尾空白后必须唯一，并满足：

```text
^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$
```

- claim namespace 使用同一命名规则，且不能带首尾空白。
- 每个 namespace 的 claim 必须是 JSON object。
- Policy、用户和 `authorization` 层拒绝未知字段。

### 3.2 Lumi 不校验的内容

- capability 是否被某个业务系统支持。
- capability 与 claim namespace 是否对应。
- claim object 内有哪些字段、字段类型和业务约束。
- 某个 capability 是否必须带特定 claim。
- claim 表示的权限是否已经在数据源侧实施。

claim object 由 namespace 所属消费端拥有。消费端应在对象内维护自己的 schema 版本，并拒绝缺失、未知版本或不合法的数据。

### 3.3 Schema 所有权与扩展边界

Policy v2 同时包含渠道身份、Lumi 通用授权和领域授权三层信息。三层必须分开演进：

| 层次 | 所有者 | 职责 |
| --- | --- | --- |
| 渠道身份层 | 企业微信适配器 | 从回调中识别 Bot、User 和会话，并把 UserID 映射到 Policy 用户 |
| 通用授权层 | Lumi Core | 定义 `authorization.capabilities` 与 `authorization.claims` 外壳，建立不可变快照并安全传递 |
| 领域授权层 | namespace 所属业务 | 定义 capability 含义、claim schema、范围求交和最终权限实施 |

字段级归属如下：

| 字段 | 所有者 | 其他业务是否需要修改 |
| --- | --- | --- |
| `version` | Lumi 的 WeCom Policy 契约 | 不修改；当前固定为 `2` |
| `botId` | 企业微信适配器 | 可省略；仅在需要将 Policy 绑定到特定 Bot 时填写 |
| `users` | 企业微信适配器 | 复用结构并配置本项目用户 |
| `userId` | 企业微信适配器 | 填写精确的 `body.from.userid` |
| `displayName` | Policy 维护者 | 仅用于展示和审计，不参与鉴权 |
| `enabled` | Policy 适配器 | 显式控制用户是否可生成 RequesterContext |
| `authorization` | Lumi Core | 不修改结构 |
| `capabilities` 数组 | Lumi Core | 不修改容器结构；数组中的名称和含义由业务定义 |
| `claims` object | Lumi Core | 不修改容器结构；namespace 和 payload 由业务定义 |
| `qdm.cmr.query` | QDM | 其他业务替换为自己的 namespaced capability |
| `qdm.scope` | QDM | 其他业务替换为自己拥有的 claim namespace |
| `qdm.scope.schemaVersion` 及内部字段 | QDM | 完全替换为自己的 schema，并由自己的消费端校验 |

因此，Lumi 通用协议只约束下面的外壳：

```json
{
  "authorization": {
    "capabilities": ["<domain capability>"],
    "claims": {
      "<domain namespace>": {
        "<domain-owned schema>": "..."
      }
    }
  }
}
```

Lumi 会校验 capability 和 namespace 的名称格式、重复项以及 claim 必须为 JSON object，但不会建立 capability 注册表，也不会解释 namespace 内部字段。claim 内的 `schemaVersion` 属于业务协议，与顶层 Policy `version`、RequesterContext `version` 和 File Envelope `version` 均独立。

Policy 文件的身份外壳当前由 WeCom 适配器实现。如果另一个项目仍通过企业微信接入，可以直接复用 `users[].userId` 等外层结构；如果接入其他 IM 渠道，渠道身份 Policy 可以不同，但最终必须映射到相同的 `Principal`、`Audience` 和 `Authorization` 通用 Context。

### 3.4 其他领域接入示例

例如财务领域可以在不修改 Lumi 的情况下定义自己的授权协议：

```json
{
  "version": 2,
  "users": [
    {
      "userId": "finance-user-001",
      "displayName": "财务用户",
      "enabled": true,
      "authorization": {
        "capabilities": [
          "finance.invoice.read",
          "finance.invoice.export"
        ],
        "claims": {
          "finance.scope": {
            "schemaVersion": 1,
            "legalEntityIds": ["entity-a"],
            "currencyCodes": ["CNY"]
          }
        }
      }
    }
  ]
}
```

该领域需要自行负责：

1. 定义 `finance.invoice.*` capability 的准确语义。
2. 拥有 `finance.scope` namespace 及其版本化 schema。
3. 在消费端严格校验 schema、必填字段和未知字段。
4. 将实际请求的法人、币种等范围与 claim 求交集。
5. 在查询代理、服务端 ACL 或数据源侧执行最终授权结果。
6. 为允许、缺 capability、缺 claim、未知版本和越权范围补充 fail-closed 测试。

Lumi 不需要为新增财务 capability 或 claim 重新发布。

### 3.5 Policy 维护者与消费端关注点

Policy 维护者负责完整用户清单；领域消费端不应读取或获得完整 Policy，只接收当前请求者的 Context：

```text
完整 Policy
    |
    | Lumi 按渠道身份解析当前用户
    v
单个 RequesterContext
    |
    v
领域消费端校验自己的 capability 与 claim
```

领域消费端需要关注 RequesterContext/Envelope 版本和绑定信息、自己要求的 capability、自己拥有的 claim namespace 及其内部 schema。它不需要了解其他用户，也不应解析不属于自己的 namespace。

## 4. 不可变策略快照

Policy 在 WebSocket 建立前加载：

```text
requester-config.json
        |
        | strict JSON decode
        | generic validation
        v
immutable snapshot + sha256 revision
        |
        v
WeCom callback lookup
```

- 运行中的连接始终使用启动时快照。
- 修改文件后必须重启服务才会生效。
- 每次生成 Context 都深拷贝 capability slice 和各 namespace 的原始 JSON payload。
- `policyRevision` 是原始 policy 文件内容的 SHA-256，用于审计本轮使用的快照。

## 5. RequesterContext v2

授权用户发起请求后，Lumi 构造：

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

`capabilities` 始终编码为数组，`claims` 始终编码为 object；没有 claims 时输出 `{}`，不输出 `null`。

## 6. 独立版本

三个契约独立版本化：

| 契约 | 当前版本 | 作用 |
| --- | ---: | --- |
| Requester Policy | 2 | 运维配置文件结构 |
| RequesterContext | 2 | ACP、Session 和设备任务中的授权上下文 |
| File Envelope | 1 | Session 文件绑定、TTL 和 Agent/Workspace 身份 |

Envelope 结构没有变化，因此保持 v1；其 `requesterContext.version` 为 2。消费端必须分别检查 Envelope 和 Context 版本，不能把两者视为同一个版本。

## 7. 传递路径

同一个 RequesterContext 通过两条路径传递：

```text
                         +--> session/prompt
                         |    _meta.lumi.requesterContext
WeCom -> Policy -> Context
                         |    session-scoped JSON envelope
                         +--> LUMI_REQUESTER_CONTEXT_DIR
```

- Local Agent 和 Sandbox Agent 接收相同的 Context v2。
- Sandbox task payload 只携带当前请求的 Context，不携带完整 policy 文件。
- 文件桥接使用原子写入、TTL 和逐 Session 清理，统一使用普通运行时
  目录以及 `0755/0644`，不要求部署 UID/GID 或 reader group。
- ACP `_meta` 的字段位置不变，只有 RequesterContext 内部 schema 升级为 v2。

## 8. 消费端安全要求

领域消费端在执行工具或访问数据前必须：

1. 检查 RequesterContext 版本为受支持版本。
2. 精确匹配所需 capability；未知 capability 不得自动授权。
3. 只解析自己拥有的 claim namespace。
4. 校验领域 schema 版本、字段类型、必填关系和取值范围。
5. 将请求范围与 claim 中允许范围求交集。
6. 在查询代理、AST 校验、服务端 ACL 或数据源 RLS 中强制实施结果。
7. 缺 capability、缺 claim、解析失败或版本未知时 fail closed。

```text
RequesterContext 到达 Agent
              !=
业务权限已经不可绕过地生效
```

Prompt、Skill 文本或 Agent 自律不能代替服务端权限控制。

RequesterContext 授权链不再要求 Lumi publisher 与 Pi 使用不同 UID，也
不要求 `runAsUid`/`runAsGid` 或 supplementary reader group。通用 run-as
能力仍可独立使用，但不会改变 RequesterContext 的发布合同。

## 9. v1 到 v2 迁移

v1 policy 和 Context 不再兼容。升级前必须把原有扁平授权字段迁移到：

```text
users[].authorization.capabilities
users[].authorization.claims.<domain-namespace>
```

迁移和发布顺序：

1. 为领域 consumer 增加 RequesterContext v2 与其 namespace schema 支持。
2. 更新 Lumi 及 Sandbox device-executor。
3. 将 policy 文件转换为 v2。
4. 重启 WeCom 服务加载新快照。
5. 完成 Local 与 Sandbox 端到端授权测试后再开放流量。

回滚 Lumi 时必须同步回滚 policy 和 consumer；旧组件不能读取 v2 policy，也不应尝试解释 v2 Context。
