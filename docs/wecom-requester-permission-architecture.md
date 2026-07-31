# 企业微信提问人权限：第一阶段整体方案

> 本阶段先用静态 JSON 管理固定用户，不建设独立权限服务。
>
> 企业微信真实群聊联调已确认：机器人由企业超级管理员创建时，回调中的 `body.from.userid` 是明文 UserID，因此可直接作为 canonical UserID。非超级管理员创建的机器人返回企业主体下的密文 `open_userid`；本阶段不实现该密文到明文 UserID 的转换。

## 1. 目标与主流程

```text
                   上线前准备

 企业管理员提前导出 UserID
              |
              v
 wecom-requesters.json（Lumi 后端持有）
              |
              v
+--------------------------------------------------------------+
|                         运行时                               |
+--------------------------------------------------------------+
              |
              v
 企业微信群：张三 @机器人
              |
              | body.from.userid = user-demo-001
              | body.aibotid     = bot-demo-001
              | body.msgid       = wecom-msg-001
              v
+------------------------------+
| Lumi                         |
| 1. 校验消息身份与 Bot ID     |
| 2. 按明文 UserID 查询 JSON   |
| 3. 构造 RequesterContext     |
+---------------+--------------+
                |
         +------+------+
         |             |
   未配置/停用       已授权
         |             |
         v             v
  附件下载前拒绝   Local / Sandbox Agent
                       |
                       | _meta.lumi.requesterContext
                       | Session 专属 JSON
                       v
                 Harness（后续改造）
                       |
                       | 校验能力与范围
                       | 生成 CLI 过滤条件
                       v
               CAS / CMR / Indicators / SQL
```

本轮实现范围是 Lumi 图中的部分。Harness 本轮不修改，因此完成本轮后代表“提问人和权限上下文已经送到 Agent 运行环境”，不代表四类 Agent 到数据源的端到端权限已经强制生效。

## 2. UserID 从哪里来

配置键必须用企业微信 UserID，不能用姓名：

```text
authorization key = (botId, canonicalUserId)

displayName = 仅用于日志和展示
```

运行时能否直接得到明文 UserID，取决于智能机器人的创建者：

```text
body.from.userid
        |
        +-- 超级管理员创建机器人 ----> 明文 UserID ----> 本阶段支持
        |
        +-- 非超级管理员创建机器人 --> 密文 open_userid -> 本阶段不转换
```

因此，第一阶段部署的明确前提是：使用企业超级管理员创建的智能机器人。BotID 本身不携带“超管机器人”类型信息，Lumi 也不能只根据 ID 的长度或格式判断身份类型。

不需要让每位员工先使用一次 Lumi。上线前可由企业管理员批量导出：

```text
企业管理员一次性开通通讯录 API 权限
             |
             | CorpID + 通讯录同步 Secret + 可信 IP
             v
GET /cgi-bin/gettoken
             |
             v
POST /cgi-bin/user/list_id
             |
             v
userid 清单 -> 生成配置模板 -> 业务负责人补充权限范围
```

- 普通成员不能自行取得通讯录 Access Token；需要企业管理员开通并提供对应 Secret 的使用条件。
- Bot Secret 不能替代通讯录同步 Secret 或企业自建应用 Secret。
- 管理员不需要反复手工获取 Token，受控脚本可缓存并刷新约 7,200 秒有效的 Access Token。
- 静态配置阶段建议由管理员在受控环境执行一次导出；Lumi 不长期保存通讯录 Secret。
- 企业微信 UserID 是管理端成员详情中的“账号”，不保证等于邮箱前缀；应以管理端或官方 API 返回值为准。

参考企业微信官方文档：

- [通讯录管理概述](https://developer.work.weixin.qq.com/document/path/90193)
- [获取成员 ID 列表](https://developer.work.weixin.qq.com/document/path/96067)
- [获取 Access Token](https://developer.work.weixin.qq.com/document/path/91039)
- [智能机器人长连接](https://developer.work.weixin.qq.com/document/path/101463)
- [自建应用与智能机器人的对接](https://developer.work.weixin.qq.com/document/path/101521)

企业微信为非超级管理员创建的机器人提供了密文转换接口：

```text
POST /cgi-bin/batch/openuserid_to_userid
密文 open_userid -> 明文 UserID
```

该接口需要企业自建应用的 Access Token，并要求成员位于应用可见范围内。当前 Lumi 没有调用此接口；在“超级管理员创建机器人”的部署前提下，本阶段固定采用：

```text
trim(body.from.userid) == canonicalUserId
```

## 3. 严格 JSON 权限配置

示例文件见 [`examples/wecom-requesters.demo.json`](./examples/wecom-requesters.demo.json)：

```json
{
  "version": 1,
  "botId": "bot-demo-001",
  "users": [
    {
      "userId": "user-demo-001",
      "displayName": "张三（模拟用户）",
      "enabled": true,
      "capabilities": [
        "qdm.cas.token",
        "qdm.cmr.query",
        "qdm.indicators.query",
        "qdm.sql.select"
      ],
      "scope": {
        "manageAreaIds": ["CN18"],
        "dcManageAreaIds": ["CN18"],
        "categoryLevel1Ids": ["12", "13"]
      }
    }
  ]
}
```

四个 capability 是跨 Lumi、Agent 与未来 Harness 使用的稳定标识：

```text
qdm.cas.token          CAS Token
qdm.cmr.query          CMR 查询
qdm.indicators.query   Indicators 查询
qdm.sql.select         只读 SQL 查询意图
```

启用方式：

```text
lumi wecom run --requester-config /absolute/path/wecom-requesters.json

或

LUMI_WECOM_REQUESTER_CONFIG=/absolute/path/wecom-requesters.json
```

规则：

- `--requester-config` 优先于环境变量。
- 配置路径非空即开启严格模式；未配置时保留现有 `allowFrom` 行为。
- 严格模式不回落到 `allowFrom`。
- JSON 拒绝未知字段、错误版本、空或重复 UserID、未知 capability。
- UserID 只做首尾空白清理，保留大小写并精确匹配。
- 启用用户必须至少有一个 capability，`manageAreaIds` 或 `dcManageAreaIds` 至少一个非空，并且 `categoryLevel1Ids` 非空。
- 配置在 `Service.Start()` 建立 WebSocket 前加载为不可变快照；修改后必须重启，不做热加载。
- 配置文件应位于 Harness workspace 外，且不会整体传入 Sandbox。

## 4. RequesterContext 契约

每条已授权消息都重新构造一份上下文：

```json
{
  "version": 1,
  "requestId": "wecom-msg-001",
  "policyRevision": "sha256:...",
  "principal": {
    "channel": "wecom",
    "botId": "bot-demo-001",
    "canonicalUserId": "user-demo-001",
    "displayName": "张三（模拟用户）"
  },
  "audience": {
    "chatId": "group-demo-001",
    "chatType": "group"
  },
  "authorization": {
    "capabilities": [
      "qdm.cas.token",
      "qdm.cmr.query",
      "qdm.indicators.query",
      "qdm.sql.select"
    ],
    "scope": {
      "manageAreaIds": ["CN18"],
      "dcManageAreaIds": ["CN18"],
      "categoryLevel1Ids": ["12", "13"]
    }
  }
}
```

```text
WeCom callback
      |
      v
ChatRunInput.RequesterContext
      |
      +-------------------------+
      |                         |
      v                         v
Local runtime             Sandbox task payload
      |                   requesterContext
      +------------+------------+
                   |
                   v
             ACP 每轮 prompt
                   |
          +--------+--------+
          |                 |
          v                 v
_meta.lumi.              Session 专属
requesterContext         requester JSON
```

双通道的含义：

1. 标准通道在每次 `session/prompt` 写入 `_meta.lumi.requesterContext`。
2. 兼容通道在每次 prompt 前原子写入 `<LUMI_REQUESTER_CONTEXT_DIR>/<sha256(acp-session-id)>.json`。

Session 文件包含 `workspaceId`、`agentId`、`sessionId`、`issuedAt`、`expiresAt` 和完整 `requesterContext`；目录权限为 `0700`、文件权限为 `0600`，prompt 完成、失败、取消或 Session 重置后清理。写入失败时严格模式中止本次查询，不降级运行。

```text
Local:   $LUMI_HOME/runtime/requester-context/<pid>/agents/<agent-id>/
Sandbox: /lumi/runtime/requester-context/...
```

Lumi 对 Claude、Codex、Qwen、Pi 输出相同的上下文契约，但 Agent/Hook 的实际读取仍需按所锁定的 adapter 版本做 smoke test。Session JSON 是兼容桥，不是安全边界。

## 5. 严格模式的拒绝顺序

```text
收到 callback
      |
      v
msgid、from.userid、aibotid 是否齐全？
      |
      v
aibotid 是否等于 cfg.BotID？
      |
      v
UserID 是否已配置且 enabled？
      |
      +-- 否 -> 统一拒绝/丢弃，不下载附件，不启动 Agent
      |
      +-- 是 -> 构造 RequesterContext，继续处理
```

- 未配置或停用用户收到统一文案：`你暂未开通该机器人的使用权限，请联系管理员。`，并将消息标记为已处理。
- 缺少可信身份字段或 Bot ID 不匹配时直接丢弃并记录错误，不向不可信目标回复。
- 拒绝发生在附件下载、IM 命令处理和 `ChatRunner` 调用之前。
- `body.msgid` 用作 `requestId`；`headers.req_id` 只用于企业微信回复关联。
- 同一条消息的自动 continuation 复用该消息的权限快照；下一条消息重新查询启动时建立的不可变快照。
- 不修改现有 conversation key；在超级管理员创建机器人的前提下，去除首尾空白后的 UserID 与 canonical UserID 相同。
- 严格模式禁用 WeCom cron Agent 查询，避免通过合成 `target.UserID` 伪造提问人。

## 6. 三部分职责

```text
+---------------------+    +---------------------+    +----------------------+
| Lumi（本轮实现）    | -> | Agent 运行环境      | -> | Harness（后续实现）   |
| 识别、授权、传上下文|    | 携带双通道上下文    |    | 校验并强制过滤       |
+---------------------+    +---------------------+    +----------------------+
```

Lumi 本轮负责：

- 加载并校验静态 JSON。
- 在超级管理员创建机器人的前提下识别明文 UserID，并在附件下载前 fail closed。
- 为每条消息构造并通过 Local/Sandbox 传递 `RequesterContext`。
- 管理逐 Session 兼容文件的写入、切换和清理。

权限配置本轮负责：

- 保存固定用户、展示名、启停状态、capability、区域和一级品类。
- 作为唯一权限来源；暂不提供 API、动态同步或热加载。

Harness 后续负责：

- 从 Hook/extension 读取上下文并校验 capability。
- 将用户请求范围与授权范围求交集，拒绝越权范围。
- 用确定性 wrapper 追加区域、一级品类等 CLI 过滤条件，不能只依赖 Prompt。
- 缺失或过期上下文时 fail closed。

## 7. 安全边界

```text
静态配置 + unsigned RequesterContext + Session JSON
                         |
                         v
                 受控试点可用
                         |
                         v
          不是生产级防绕过权限边界
```

- Harness 本轮不修改；裸 QDM CLI、共享 Token 或直连数据源仍可能绕过未来 Hook。
- Session JSON 与 Agent 属于同一 OS 用户时可被修改，因此它只解决兼容传递，不提供不可伪造性。
- `qdm.sql.select` 只表示授权意图。SQL 不能只靠 Prompt 或字符串拼接安全过滤；生产开放前必须由查询代理、SQL AST 校验或数据库 RLS 强制范围。
- 群聊结果对全群成员可见，试点群成员应具有相容的数据权限。

## 8. 第一阶段验收

```text
[x] 合法 JSON 启动，非法/重复/未知字段配置启动失败
[x] 未配置或停用用户在附件下载和 Agent 启动前被拒绝
[x] body.aibotid 与配置 Bot ID 不一致时不处理
[x] 同群两个 UserID 得到各自的新鲜 RequesterContext
[x] Local 与 Sandbox 都传递相同结构
[x] 每轮 ACP prompt 都包含 _meta.lumi.requesterContext
[x] Session 专属 JSON 内容一致，并在 prompt 返回后清理
[x] 严格模式不回落 allowFrom，且禁用 WeCom cron Agent 查询
[x] 未配置 requester config 时旧 allowFrom 行为不变
[x] 真实群聊验证超管创建机器人回调直接返回明文 UserID
```

以上是 Lumi 侧第一阶段验收状态。Harness 的 capability 校验、范围求交和 CLI 强制过滤不包含在这些勾选项内。
