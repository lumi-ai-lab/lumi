# Lumi 企业微信提问人权限：第一阶段实现说明

> 本文只描述 Lumi 本轮要实现的内容。整体边界见[第一阶段整体方案](./wecom-requester-permission-architecture.md)。
>
> 当前状态：Lumi 侧第一阶段代码已实现；Harness 侧强制过滤仍属于后续工作。

## 1. 本轮结果

```text
企业微信回调 body.from.userid
              |
       +------+------+
       |             |
  超管创建机器人   非超管创建机器人
       |             |
       v             v
明文 canonical    密文 open_userid
    UserID            |
       |              v
       v         本轮停止：不转换
+-----------------------------+
| Lumi                        |
| 严格配置校验                |
| 用户查询与拒绝              |
| 构造 RequesterContext       |
| Local / Sandbox 逐轮传递    |
+--------------+--------------+
               |
               v
        Claude/Codex/Qwen/Pi
       可定位本轮权限上下文
               |
               v
       Harness 后续读取并强制过滤
```

企业微信真实群聊联调已验证，企业超级管理员创建的机器人会直接返回明文 UserID。本轮部署以此为前提：Lumi 不把 UserID 转成用户名，也不把密文 `open_userid` 转成明文 UserID。鉴权键是明文 UserID，`displayName` 从 JSON 配置读取且只用于展示。

上述分支是部署前需要确认的企业微信行为，并非 Lumi 在运行时根据 BotID、UserID 长度或格式判断机器人创建者身份。

```text
超级管理员创建机器人
          |
          v
body.from.userid = 明文 UserID
          |
          v
严格权限配置 -> RequesterContext
```

非超级管理员创建的机器人需要调用企业微信官方 `POST /cgi-bin/batch/openuserid_to_userid` 接口；该能力不在本轮实现范围内。详见[企业微信提问人权限整体方案](./wecom-requester-permission-architecture.md#2-userid-从哪里来)。

## 2. 启动与配置加载

```text
--requester-config PATH
          |
          | 若未提供
          v
LUMI_WECOM_REQUESTER_CONFIG
          |
          v
严格解析 JSON -> 不可变权限快照 -> 建立 WeCom WebSocket
```

- 新增 `lumi wecom run --requester-config <absolute-path>`，flag 优先于环境变量。
- 配置路径非空即进入严格模式；未设置时不改变现有 `allowFrom` 行为。
- 严格模式忽略 `allowFrom`，任何配置错误均阻止服务启动。
- 只接受 `version: 1` 和四个 capability：

```text
qdm.cas.token
qdm.cmr.query
qdm.indicators.query
qdm.metric.query
qdm.sql.select
```

- UserID 首尾去空白后大小写敏感匹配；拒绝空值和重复值。
- 启用用户必须至少有一个 capability，至少配置一个 `manageAreaIds` 或 `dcManageAreaIds`，并配置非空 `categoryLevel1Ids`。
- 运行时授权快照只在 `Service.Start()` 加载；保存配置、状态检查和连接测试会单独校验文件，但不会热更新正在运行的快照。修改后重启生效。
- 权限文件应位于 Harness workspace 外；Sandbox 只接收本轮上下文，不接收完整配置。

示例配置：[`examples/wecom-requesters.demo.json`](./examples/wecom-requesters.demo.json)。

## 3. 回调处理顺序

授权检查必须移到附件下载之前：

```text
handleMsgCallback
      |
      v
校验 msgid / from.userid / aibotid
      |
      v
校验 aibotid == cfg.BotID
      |
      v
lookup(trim(from.userid))
      |
      +-----------------------+
      |                       |
 unknown / disabled         enabled
      |                       |
      v                       v
统一拒绝并标记处理      构造 RequesterContext
      |                       |
      |                       v
      |                下载附件/处理命令
      |                       |
      |                       v
      +-- 不调用 Runner      ChatRunner
```

固定行为：

- 未配置与停用用户统一回复：`你暂未开通该机器人的使用权限，请联系管理员。`
- 缺少关键身份字段或 Bot ID 不匹配时丢弃并记录错误，不向不可信目标回复。
- 未授权消息不得下载附件、处理 IM 命令或调用 `ChatRunner`。
- `body.msgid` 是上下文 `requestId`；`headers.req_id` 继续仅用于回复关联。
- 严格模式拒绝 WeCom cron Agent 查询，防止合成 `target.UserID` 绕过真实回调身份。

## 4. 结构化传递链路

为公共输入和任务载荷增加可选 `RequesterContext`，完整路径为：

```text
WeComInboundMessage.RequesterContext
                 |
                 v
ChatRunInput.RequesterContext
                 |
       +---------+---------+
       |                   |
       v                   v
Local ChatRunner       Sandbox imRunInput
                           |
                           v
                TaskExecutePayload.requesterContext
       |                   |
       +---------+---------+
                 |
                 v
        device-executor / Agent runtime
```

`RequesterContext` 包含：

```text
version
requestId                <- body.msgid
policyRevision           <- 权限配置内容摘要
principal
  channel                = wecom
  botId                  = cfg.BotID
  canonicalUserId        = trim(body.from.userid)  // 超管创建机器人时为明文
  displayName            = 权限配置
audience
  chatId / chatType      = 企业微信回调
authorization
  capabilities           = 权限配置
  scope
    manageAreaIds
    dcManageAreaIds
    categoryLevel1Ids
```

每条新消息重新查询不可变配置快照并构造上下文。现有 conversation key 不修改；在超级管理员创建机器人的部署前提下，raw UserID 就是 canonical UserID。同一消息的 continuation 复用该消息快照。

## 5. Agent 双通道

```text
RequesterContext
       |
       +----------------------------+
       |                            |
       v                            v
ACP session/prompt              Session JSON
_meta.lumi.                     sha256(sessionId).json
requesterContext
       |                            |
       +-------------+--------------+
                     v
          Agent Hook / extension 可定位
```

### 标准通道

每一次 `session/prompt` 都写入：

```json
{
  "_meta": {
    "lumi": {
      "requesterContext": {}
    }
  }
}
```

不能只在 `session/new` 或 System Prompt 中设置一次，否则复用 Session 时权限可能陈旧。

### Session JSON 兼容通道

在每次 prompt 前写入：

```text
<LUMI_REQUESTER_CONTEXT_DIR>/<sha256(acp-session-id)>.json
```

文件外层契约：

```json
{
  "version": 1,
  "workspaceId": "cli-sandbox-demo",
  "agentId": "claude",
  "sessionId": "acp-session-id",
  "issuedAt": "2026-07-29T10:00:00+08:00",
  "expiresAt": "2026-07-29T10:30:00+08:00",
  "requesterContext": {}
}
```

生命周期：

- Local 目录为 `$LUMI_HOME/runtime/requester-context/<pid>/agents/<agent-id>/`；同一 Agent 跨 Workspace 复用该稳定目录，JSON 内仍记录真实 `workspaceId`。
- Sandbox 目录为 `/lumi/runtime/requester-context/`。
- 环境变量只暴露目录路径，不直接保存 UserID 或权限。
- prompt 前采用临时文件加 rename 原子写入；目录 `0700`、文件 `0600`。
- prompt 完成、失败或取消后删除；unknown-session 恢复时删除旧文件并为新 Session 重写。
- 严格模式写入失败即中止，不退化为仅 `_meta`。

Claude、Codex、Qwen、Pi 都使用这份统一输出契约，但这不等于四个 Agent 已经端到端执行权限。各 adapter 的 Session ID 对齐和 Hook/extension 读取需要版本锁定与 smoke test；Harness 本轮不修改。

## 6. 失败策略

```text
配置加载失败 ----------------------> 服务不启动
身份字段缺失 / Bot ID 不匹配 -----> 丢弃并记录
用户未知 / 停用 ------------------> 统一拒绝
上下文传递或 Session 文件失败 ----> 本次查询失败
Sandbox unknown-session ----------> 切换新 Session 并重写上下文
```

所有严格模式路径均 fail closed，不允许回落为“无上下文继续查询”。

## 7. 测试与验收

```text
配置层
  [x] 合法配置加载
  [x] 未知字段、错误版本、重复 UserID、未知 capability 拒绝
  [x] flag / env / flag 优先级正确

WeCom 入口
  [x] 已授权用户继续执行
  [x] 未知或停用用户在附件下载前拒绝
  [x] 空 msgid/UserID/aibotid 和 Bot ID 不匹配不执行 Runner
  [x] legacy allowFrom 模式行为不变
  [x] 严格模式 WeCom cron Agent 查询被拒绝
  [x] 真实群聊验证超管创建机器人返回明文 UserID

传递层
  [x] Local prompt 的 _meta 与 Session JSON 使用同一 RequesterContext
  [x] Sandbox task payload 完整携带 requesterContext
  [x] unknown-session 恢复时为新 Session 重写上下文
  [x] 两个 UserID 使用各自会话且不会串用权限范围
  [x] prompt 返回或报错后清理 Session 文件

兼容层
  [ ] Claude / Codex / Qwen / Pi 分别完成 Session 定位 smoke test
```

## 8. 本轮不做

- 不调用企业微信通讯录，也不获取 Access Token。
- 不做非超级管理员创建机器人所返回的密文 `open_userid` 到明文 UserID 的转换。
- 不建设权限服务、配置热加载或动态入离职同步。
- 不修改 Harness，不在 Lumi 中生成 CMR/Indicators/SQL 的最终过滤参数。
- 不把 unsigned Session JSON 当作安全凭证。

特别是 SQL：`qdm.sql.select` 只是一项 capability。生产环境仍必须由 Harness 查询代理、SQL AST 校验或数据库 RLS 实施不可绕过的区域/品类限制。
