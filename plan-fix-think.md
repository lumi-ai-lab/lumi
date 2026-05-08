# 修复 Thinking 输出泄露

## 摘要

当前 Lumi 把 ACP 的 `agent_thought_chunk` 错误地当成普通 assistant 正文处理。后端会把 thought chunk 和 `agent_message_chunk` 追加到同一个文本累加器里，当前实际使用的 React/Next 对话页面再把这些内容渲染并提交成普通 assistant 消息。

修复方向参考 Ditto 的“类型隔离”：正式回答、工具调用、Thinking 内容必须是彼此独立的消息/流式类型，不能混在同一个 `content` 里。

Vue 版本前端已经不再使用，本次不改 Vue 相关文件。不要修改 `web/src/components/ChatContainer.vue`、`web/src/stores/session.ts` 等 Vue 旧实现。

目标行为：

- React/Next Web 对话页把 Thinking 显示为独立的可折叠块。
- Thinking 在本机会话历史里持久化，刷新页面、切换会话后仍可查看。
- 分享页默认隐藏 Thinking，避免对外分享时泄露模型思考过程。
- 微信、企业微信等 IM 渠道直接隐藏 Thinking。
- 正常 assistant 回答里不再包含 thought 文本，也不包含 `<think>` / `<thinking>` 块。

## 实现改动

### 后端 Web Chat 主链路

- 在 `backend/internal/api/notification.go` 中拆分 `agent_message_chunk` 和 `agent_thought_chunk` 的处理逻辑。
- `agent_message_chunk` 继续作为正式回答文本处理，但进入普通 `currentText` 累加器前要先清理 `<think>` / `<thinking>` 内容。
- 为 `agent_thought_chunk` 增加独立的 Thinking 累加器/状态，不能追加到 `currentText`。
- 扩展 `backend/internal/api/chat.go` 里的 `streamItem`，支持 `text`、`tool`、`thinking` 三类 item。
- 在 `backend/internal/conversation/manager.go` 增加可持久化的 Thinking 消息形态，建议使用兼容旧数据的 `type` 字段：
  - 旧消息没有 `type` 或 `type` 为空时，仍按普通 text 消息处理；
  - 新 Thinking 消息使用 `type: "thinking"`；
  - `content` 保存累积后的 Thinking 文本；
  - 为 UI 展示保留必要元信息，例如 `status: "thinking" | "done"`、`duration`，可以放在可选字段中，也可以放在一个小的 `thinking` 对象里。
- 增加 manager 辅助方法，例如 `AddThinkingMessage`，或增加一个更通用的 typed message helper。现有 `AddAssistantMessage` 保持只负责普通正文。
- 参考 Ditto 的阶段结束逻辑，在以下时机 finalize Thinking：
  - tool call 到来前，先 flush 普通文本并结束当前 Thinking；
  - Thinking 后面出现普通正文时，先结束当前 Thinking，再追加正文；
  - stream `done` 时，结束所有仍处于 active 状态的 Thinking 并持久化。
- 增加 `<think>` / `<thinking>` 提取与剥离逻辑：
  - 从普通回答 chunk 中提取完整 `<think>...</think>`、`<thinking>...</thinking>` 块，进入 Web Thinking 通道；
  - 剥离后的文本才进入普通 `currentText`；
  - 防御性处理孤立的闭合标签，例如只有 `</think>` 的 MiniMax/DeepSeek 类输出，行为可参考 Ditto 的 `ThinkTagDetector`。

### React/Next Web 前端

- 更新 `web/src/lib/types.ts` 中的共享类型：
  - `Message` 增加可选 `type?: "text" | "thinking"`；
  - 如有需要，新增 `ThinkingData`；
  - `StreamItem` 增加 `{ type: "thinking"; data: ThinkingData }`。
- 更新 `web/src/features/chat/use-chat-controller.tsx`：
  - 删除当前 `agent_thought_chunk` 调用 `addStreamingText` 的逻辑；
  - 增加 `addThinking` / `finalizeThinking` 等按 session 维护 Thinking stream item 的操作；
  - commit stream items 时，Thinking 写入 `type: "thinking"` 的 message，不能写成普通 assistant text。
- 新增 React Thinking 展示组件，用于实时流式状态和已持久化消息：
  - 生成中默认展开；
  - 完成后默认折叠；
  - 标题使用简洁文本，例如生成中为 `Thinking...`，完成后为 `Thought complete`；
  - 内容区域使用 plain pre-wrap 文本展示，不按 Markdown 渲染。
- 更新 `web/src/features/chat/components/chat-message.tsx`：
  - `message.type === "thinking"` 时渲染 Thinking 组件；
  - tool call、error、user、普通 assistant 的现有行为保持不变。
- 更新 `web/src/features/chat/components/chat-panel.tsx`：
  - 渲染 `streamItems` 时增加 `thinking` 分支；
  - `readonly` 为 true 时过滤掉 Thinking，因此分享页不会显示 Thinking。
- 调整 visible-message 分组和 `hideAgentTag` 逻辑，避免 Thinking 块影响普通 assistant 消息的 agent 标签显示。
- 增加一个前端兜底过滤：
  - 如果普通 text 消息里仍包含 `<think>` / `<thinking>` 标签，渲染前剥离；
  - 这只是兼容旧脏数据或漏网内容的显示兜底，不是主修复点。

### IM 渠道

- 更新 `backend/internal/api/wechat_chat.go` 和 `backend/internal/api/wecom_chat.go`。
- 在 IM 渠道中把 `agent_thought_chunk` 视为隐藏内容：
  - 不追加到 `currentText`；
  - 不 emit 给 IM sink；
  - 不作为普通 assistant 文本持久化给 IM 展示。
- `agent_message_chunk`、工具调用、权限确认、错误处理等现有行为保持不变。
- 普通 message chunk 输出到 IM 前，也要剥离 `<think>` / `<thinking>` 内容，防止模型把思考过程内嵌在正文里泄露到微信或企业微信。

## 明确不做

- 不修改 Vue 旧前端文件，例如 `web/src/components/ChatContainer.vue`、`web/src/stores/session.ts`。
- 不做历史会话的批量迁移。旧消息没有 `type` 时继续按普通 text 渲染。
- 本次不在公开分享页显示 Thinking。
- 本次不增加“显示/隐藏 Thinking”的设置开关。

## 测试计划

后端测试：

- 只有 `agent_thought_chunk` 时，持久化为 `type: "thinking"`，不能进入普通 assistant `content`。
- `agent_message_chunk`、`agent_thought_chunk`、再 `agent_message_chunk` 的顺序下，产物应是干净正文消息加独立 Thinking 消息。
- tool call 边界会正确 flush 正文，同时 Thinking 仍保持独立消息。
- stream 完成时会 finalize active Thinking。
- 内联 `<think>secret</think>answer` 会产出 Thinking=`secret`、正文=`answer`。
- 微信/企业微信不会输出 `agent_thought_chunk`，也不会输出内联 think-tag 内容。

React/Next 前端测试或手动验证：

- Thinking 以独立可折叠块流式显示。
- 最终 assistant 正文不包含任何 thought 文本。
- 刷新页面、切换会话后，Web 会话内仍能看到持久化的 Thinking。
- 分享页不显示 Thinking。
- 工具调用仍能正常渲染和更新。

回归检查：

- 没有 `type` 字段的旧会话消息仍正常渲染。
- 发送消息、取消生成、权限确认、远程 device chat、workspace refresh 仍正常工作。

## 推荐实现顺序

1. 先增加后端 Thinking message/stream 类型，并补 `notification.go` 和 finalize 相关测试。
2. 更新 React/Next 类型和 `use-chat-controller.tsx`，让前端单独消费并 commit Thinking。
3. 新增 Thinking UI 组件，并在分享页过滤。
4. 更新 IM 渠道隐藏逻辑和内联 think-tag 剥离。
5. 跑后端测试和前端 type/build 检查。
