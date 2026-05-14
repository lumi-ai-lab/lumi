<p align="center">
  <img src="docs/lumi-16-9.png" alt="Lumi Logo" width="720">
</p>

# Lumi

基于 [ACP](https://agentclientprotocol.com/protocol/overview) 协议 - 同时调度多个 AI Agent 的 ChatBot。

## 功能特性

- 多 Agent 支持 (Claude Code, Codex 等)
- 多工作区管理
- 会话历史持久化
- 实时流式响应 (SSE)
- 权限确认机制
- 深色/浅色主题
- 中英文切换

## 预览

![前置依赖安装](docs/setup.png)

配置你的 AI 供应商, 后即可使用

![渠道配置](docs/config.png)

![聊天页面预览](docs/chat.png)

## 架构图

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Browser (Vue 3 + TypeScript)                 │
│                                                                      │
│   ┌──────────┐   ┌───────────────┐   ┌───────────┐   ┌──────────┐  │
│   │ Sidebar  │   │ ChatContainer │   │ ChatInput │   │ Settings │  │
│   │          │   │               │   │           │   │  Modal   │  │
│   │ Sessions │   │  Messages     │   │ @mentions │   │          │  │
│   │ Workspace│   │  ToolCalls    │   │ /commands │   │ Agents   │  │
│   └────┬─────┘   └───────┬───────┘   └─────┬─────┘   └──────────┘  │
│        │                 │                 │                        │
│        └─────────────────┼─────────────────┘                        │
│                          ▼                                          │
│                   ┌─────────────┐                                   │
│                   │ session.ts  │  State Management                 │
│                   └──────┬──────┘                                   │
│                          ▼                                          │
│                   ┌─────────────┐                                   │
│                   │  api/       │  HTTP + Server-Sent Events        │
│                   └──────┬──────┘                                   │
└──────────────────────────┼──────────────────────────────────────────┘
                           │
                           │ HTTP / SSE
                           ▼
┌──────────────────────────────────────────────────────────────────────┐
│                          Go Backend                                   │
│                                                                       │
│   ┌───────────────────────────────────────────────────────────────┐  │
│   │                        HTTP Server                             │  │
│   │  /api/chat    /api/sessions    /api/agents    /api/workspaces │  │
│   └───────────────────────────┬───────────────────────────────────┘  │
│                               │                                       │
│          ┌────────────────────┼────────────────────┐                 │
│          ▼                    ▼                    ▼                 │
│   ┌─────────────┐     ┌─────────────┐      ┌─────────────┐          │
│   │   Router    │     │  Session    │      │ Conversation│          │
│   │             │     │  Storage    │      │   Manager   │          │
│   │ @mention    │     │             │      │             │          │
│   │ keywords    │     │ ~/.config/  │      │  In-memory  │          │
│   └──────┬──────┘     └─────────────┘      └─────────────┘          │
│          │                                                           │
│          ▼                                                           │
│   ┌─────────────────────────────────────────────────────────────┐   │
│   │                     Agent Manager                            │   │
│   │                                                              │   │
│   │   ┌─────────────┐   ┌─────────────┐   ┌─────────────┐       │   │
│   │   │   Agent 1   │   │   Agent 2   │   │   Agent N   │       │   │
│   │   │ (claude)    │   │  (codex)    │   │   (...)     │       │   │
│   │   └──────┬──────┘   └──────┬──────┘   └──────┬──────┘       │   │
│   │          │                 │                 │               │   │
│   └──────────┼─────────────────┼─────────────────┼───────────────┘   │
│              │                 │                 │                    │
└──────────────┼─────────────────┼─────────────────┼────────────────────┘
               │ JSON-RPC        │ JSON-RPC        │ JSON-RPC
               ▼                 ▼                 ▼
        ┌─────────────┐   ┌─────────────┐   ┌─────────────┐
        │ claude-code │   │   codex     │   │    ...      │
        │  process    │   │  process    │   │  process    │
        └─────────────┘   └─────────────┘   └─────────────┘
```

## 消息流程图

```
用户输入消息
      │
      ▼
┌─────────────────┐
│  ChatInput.vue  │  检测 @mentions, /commands
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  session.ts     │  commitStreamItems() + addUserMessage()
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  api/index.ts   │  POST /api/chat (SSE)
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  chat.go        │  解析请求, 路由到 Agent
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  router.go      │  根据 @mention/keywords 选择 Agent
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  manager.go     │  获取或启动 Agent 进程
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  rpc.go         │  JSON-RPC 通信
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Agent Process  │  处理请求, 流式响应
└────────┬────────┘
         │
         │ events (message, tool_call, etc.)
         ▼
┌─────────────────┐
│  chat.go        │  SSE 推送到前端
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  session.ts     │  addStreamingText() / addToolCall()
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ ChatContainer   │  实时渲染消息和工具调用
└─────────────────┘
```

## 快速开始

### 1. 安装依赖

```bash
# 前端
cd web && npm install

# 后端 (Go 1.21+)
cd backend && go mod download
```

### 2. 配置

首次启动时，如果没有找到配置文件，会自动创建 `~/.lumi/lumi.config.json`。

你也可以手动复制配置文件:
```bash
mkdir -p ~/.lumi
cp backend/lumi.config.example.json ~/.lumi/lumi.config.json
```

### 3. 开发模式

```bash
# 终端 1: 启动后端
cd backend && go run ./cmd/lumi

# 终端 2: 启动前端 (热重载)
cd web && npm run dev
```

访问 http://localhost:5173

### 4. 生产构建

```bash
# 构建嵌入式单文件
cd web && npm run build:togo
cd ../backend && go build -o lumi ./cmd/lumi

# 运行
./lumi
```

访问 http://localhost:3000

`lumi` 也内置 CLI 子命令，可在不打开 Web 的情况下管理系统能力：

```bash
./lumi setup
./lumi cron list
./lumi wecom run --workspace <path> --agent <id> --bot-id <id> --bot-secret <secret>
```

`lumi wecom run` 不需要打开 Web 页面，但会启动本地 Lumi runtime，默认监听 `3000` 端口；如需避开端口冲突，可传 `--port` 或设置 `LUMI_PORT`。

### 5. Linux 上的 Claude ACP 运行注意事项

如果你在 Linux 服务器上通过 `@agentclientprotocol/claude-agent-acp@0.30.0` 使用 Claude，且系统自带的 `glibc` 版本较老（例如部分 CentOS 7 / RHEL 7 环境），SDK 默认下载的 Linux native `claude` binary 可能无法启动。

这种情况下，需要在启动 Lumi 之前显式指定系统里已安装可用的 Claude 可执行文件：

```bash
export CLAUDE_CODE_EXECUTABLE=/opt/nodejs/bin/claude
```

适用场景：

- Linux 服务器上 `session/new` 阶段报 `write EPIPE`
- 或手动执行 npx cache 中的 `@anthropic-ai/claude-agent-sdk-linux-x64/claude` 时提示 `GLIBC_xxx not found`

如果本机通过默认安装链路已经能正常启动 Claude，则通常不需要设置这个变量。

## 配置说明

### 配置文件位置

配置文件按以下顺序搜索（使用第一个找到的）:

1. `./lumi.config.json` - 当前目录
2. `./lumi.json` - 当前目录
3. `~/.lumi/lumi.config.json` - 用户目录 (首次启动自动创建)
4. `~/.config/lumi/config.json` - XDG 配置目录

### 配置格式

```json
{
  "publicServerURL": "",
  "agents": [
    {
      "args": [
        "-y",
        "@agentclientprotocol/claude-agent-acp@0.30.0"
      ],
      "command": "npx",
      "id": "claude",
      "name": "Claude Code",
      "permissionMode": "default"
    },
    {
      "args": [
        "-y",
        "@zed-industries/codex-acp"
      ],
      "command": "npx",
      "env": {
        "OPENAI_API_KEY": "aicoding-xxxxx",
        "OPENAI_BASE_URL": "https://api.aicoding.sh/v1"
      },
      "id": "codex",
      "name": "Codex CLI",
      "permissionMode": "default"
    }
  ],
  "defaultAgent": "claude",
  "routing": {
    "keywords": {
      "@claude": "claude",
      "@codex": "codex"
    },
    "meta": true
  }
}
```

说明：

- `claude` 这类 agent 默认会继承当前 shell 环境变量；sample 配置里不再内置 Claude/Codex 的占位鉴权信息。
- 如果在 Linux 服务器上使用 `@agentclientprotocol/claude-agent-acp@0.30.0`，并且系统的 `glibc` 版本不足以运行 SDK 自带的 native `claude` binary，需要在启动前设置 `CLAUDE_CODE_EXECUTABLE` 指向系统中可用的 `claude` 可执行文件。

`publicServerURL` 是可选项。配置后，远程设备配对命令会优先使用这个地址；未配置时，系统会自动尝试使用当前服务机器的局域网 IP，而不是默认写成 `localhost`。

### Agent 权限模式

- `default`: 敏感操作需要用户确认
- `bypass`: 自动批准所有操作 (谨慎使用)

### 路由规则

- `@agent-id`: 使用 @ 指定 Agent
- `keywords`: 关键词匹配路由
- `meta`: 启用元路由 (Agent 可以路由到其他 Agent)

## 技术栈

**前端:**
- Vue 3 (Composition API)
- TypeScript
- Vite
- markstream-vue (Markdown 渲染)

**后端:**
- Go 1.21+
- 内嵌静态文件 (go:embed)
- JSON-RPC 2.0

## License

MIT
