# Lumi 企业微信提问人权限透传

## 启用

```bash
lumi wecom run \
  --workspace /absolute/workspace \
  --agent <id> \
  --bot-id <bot> \
  --bot-secret <secret> \
  --requester-config /absolute/config/wecom-requesters.json \
  --requester-config-refresh 10m \
  --port 3001
```

| Flag | Env | 默认 | 说明 |
| --- | --- | --- | --- |
| `--requester-config` | `LUMI_WECOM_REQUESTER_CONFIG` | 空 | 多用户权限清单；非空即开启严格权限模式 |
| `--requester-config-refresh` | `LUMI_WECOM_REQUESTER_CONFIG_REFRESH` | `10m` | 周期重载；`0` 关闭周期（仍可手动 reload） |

未配置清单时保留原 `allowFrom` 行为。

## 清单格式（Policy v2）

见 [examples/wecom-requesters.demo.json](./examples/wecom-requesters.demo.json)。

- `version` 必须为 `2`
- 按 `body.from.userid` 精确匹配（trim，大小写敏感）
- `enabled=false` 或未知用户：fail-closed，回复无权文案
- Lumi 只校验外壳（capability 命名、claim 为 JSON object）；不解释 `qdm.scope` 业务字段

## 进程内内存与刷新

- 启动时加载清单到**本进程**内存；多机器人 = 多 Lumi 进程，互不共享
- 默认每 10 分钟从磁盘重载；失败则保留上一份成功快照
- 立即刷新（指定进程，按 HTTP 端口）：

```bash
lumi wecom policy reload --port 3001
# 或
curl -X POST http://127.0.0.1:3001/api/wecom/requester-policy/reload
curl http://127.0.0.1:3001/api/wecom/requester-policy
```

## 透传字段（对齐 harness-data authz v2）

每轮命中用户后：

1. 切片为单用户 auth 文档（`version=2` + `users` 长度 1）
2. AES-256-GCM 加密为 `qdm1enc.*`（与 metric-cli 相同协议与默认密钥）
3. 旁路下发（**不写进用户 prompt 正文**）：

### ACP `session/prompt` `_meta`

```json
{
  "_auth": "qdm1enc....",
  "_auth_user_id": "pengmingde01",
  "lumi": {
    "requesterContext": { }
  }
}
```

### Session 文件信封

目录：`$LUMI_HOME/runtime/requester-context/<pid>/agents/<agent-id>/`

环境变量：`LUMI_REQUESTER_CONTEXT_DIR`

信封字段含 `_auth`、`_auth_user_id`（及可选 `requesterContext`）。

## 密钥

环境变量 `QDM_METRIC_AUTH_BLOB_KEY`（base64 编码的 32 字节）与 metric-cli 共用。生产必须覆盖内置默认密钥。

## 对接 metric-cli 手测

```bash
# 从信封复制 _auth 后：
qdm-metric-cli analysis validate --payload request.json \
  --data-auth --auth-blob '<_auth value>'
```

## 手动验收摘要

1. 授权用户发消息 → 有 `_auth` 信封 / Agent 正常
2. 未授权用户 → 无权文案，无 Agent
3. 改清单后 `policy reload --port` → 立即生效
4. 不传 `--requester-config` → 旧 allowFrom 行为
