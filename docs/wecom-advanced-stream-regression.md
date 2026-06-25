# 企业微信高级流式回归手册

本文用于验证企业微信 `--stream` 的正式实现。验证需要真实企业微信智能机器人和人工观察客户端最终展示；单元测试不能替代这一步。

## 前置条件

1. 停掉同一个 bot 的其它 `lumi wecom run` 或 `stream-spike` 进程，避免多个 websocket 连接抢同一条 callback。
2. 准备环境变量，不要把 secret 写入日志或提交到仓库：

```bash
export LUMI_BOT_ID="..."
export LUMI_BOT_SECRET="..."
```

3. 构建当前代码：

```bash
cd backend
go build -buildvcs=false -o /tmp/lumi-wecom-advanced ./cmd/lumi
```

## 启动命令

```bash
mkdir -p /tmp/lumi-wecom-advanced-logs

LUMI_WECOM_STREAM=true \
/tmp/lumi-wecom-advanced wecom run \
  --workspace /tmp/for-lumi \
  --kind sandbox \
  --agent claude \
  --bot-id "$LUMI_BOT_ID" \
  --bot-secret "$LUMI_BOT_SECRET" \
  --stream \
  2>&1 | tee "/tmp/lumi-wecom-advanced-logs/run-$(date +%Y%m%d-%H%M%S).log"
```

如果要回退普通消息模式，去掉 `--stream` 和 `LUMI_WECOM_STREAM=true` 后重启。

## 用例

在企业微信客户端依次发送：

```text
请用 Markdown 表格输出 20 行区域销售数据，边生成边输出。
```

```text
请输出一个包含 80 个 items 的 JSON，并用代码块包起来。
```

```text
请输出一段很长的分段说明，大约 3000 字。
```

如果需要覆盖文件/图片协议，发送一个会触发 `[LUMI_WECOM_SEND]` 的任务，并确认 live 阶段没有出现协议块、路径或 JSON。

## 客户端验收

每个用例都要人工观察：

- 生成过程中持续有 live raw 文本可见。
- 表格、JSON、代码块不会让 live 输出长时间停顿。
- final 阶段原地覆盖 raw 气泡，而不是新增一份完整正式答案。
- 最终聊天记录没有 raw + final 重复。
- 长 code / JSON 最终仍以可覆盖文本展示，不被强制替换成“已转为文件发送”的摘要。
- 若包含 media，文本 slots 先完成覆盖，之后再发送 media / caption。

## 日志验收

日志中应出现：

```text
mode=advanced
event=slot_created
event=slot_updated
event=slot_rotated_unfinished
event=slot_finalizing
event=slot_final_ack
event=advanced_complete
```

有 media 时还应出现：

```text
event=media_delivered
```

重点检查：

- 每个有 live 内容的 `streamID` 都有对应 `slot_final_ack`。
- 多 stream 场景下有多个不同 `streamID`。
- 不应出现 `ack_timeout`、`stream_expired`、`write_failed`、`websocket_disconnected`。
- 不应出现包含 `[LUMI_WECOM_SEND]`、media JSON 或工作区文件路径的 stream content。

## 失败处理

如果出现 final ack 失败、stream 过期、客户端 raw + final 重复或旧 stream 无法原地覆盖：

1. 保存日志和客户端截图。
2. 停止进程。
3. 去掉 `--stream` 和 `LUMI_WECOM_STREAM=true` 回退普通消息模式。
4. 不要用普通 `Send` 补发完整答案来“修复”失败，因为这会破坏无重复目标。

## 记录模板

```text
日期:
二进制版本/commit:
bot:
workspace:

用例 1 Markdown 表格:
- 客户端持续 live: PASS/FAIL
- final 原地覆盖: PASS/FAIL
- 无重复: PASS/FAIL
- 日志异常:

用例 2 JSON/code:
- 客户端持续 live: PASS/FAIL
- final 保持文本覆盖: PASS/FAIL
- 无重复: PASS/FAIL
- 日志异常:

用例 3 长文本:
- 多 stream 轮转: PASS/FAIL
- 所有 slot_final_ack: PASS/FAIL
- 无重复: PASS/FAIL
- 日志异常:

media 协议:
- live 不泄漏协议块: PASS/FAIL
- text slots 先 final: PASS/FAIL
- media/caption 发送: PASS/FAIL
- 日志异常:
```
