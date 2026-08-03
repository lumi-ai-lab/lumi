# Lumi Pi 授权运行时边界修复 Handoff

## 1. 当前状态

- 日期：2026-08-03
- Worktree：`/Users/pengmd/c/lumi-pi-authz-fix`
- 分支：`fix/pi-authz-runtime-boundary`
- 基线：`f181be55f5376095aa0dd9ee96dee56f87acd339`
- 基线提交：`feat(authz): 支持 Pi 降权运行与共享上下文`
- 发布判断：**No-Go**
- 关联 Harness PR：<https://github.com/lumi-ai-lab/harness-data/pull/12>
- 计划中的 Harness 版本：`v0.0.28`，修复和跨 UID/GID 联调完成前不得发布

本分支只修复 `f181be5` 引入的 Pi 降权运行与共享 RequesterContext 边界，不扩大授权协议范围，也不改变未启用安全模式时 Lumi 已有的 Agent 能力。

## 2. 必须保持的跨仓库合同

### 2.1 RequesterContext

- Envelope 版本：v1。
- `RequesterContext` 版本：v2。
- 安全模式目录：`<root>/<workspace>/<agent>`。
- Pi 授权目录：`<root>/<workspace>/pi`。
- context root、Workspace 和 Agent 目录：publisher owner、reader group、精确模式 `0710`。
- 信封文件：publisher owner、reader group、精确模式 `0640`。
- 文件名：`sha256(raw ACP session ID) + ".json"`。
- consumer 必须根据原始 session ID 精确打开文件，不得用 `ReadDir`、glob 或“唯一活动文件”推断身份。
- reader group 可以遍历已知路径并读取已知文件，但不能枚举其他 session 文件名。

Harness 的生产 consumer 已遵守上述合同，不得为了适配 Lumi 测试 fixture 将目录放宽为 `0750`。

### 2.2 运行身份

安全部署中的进程关系为：

```text
Lumi / device-executor publisher
    ├── 创建并写入 RequesterContext
    └── 以 runAsUid/runAsGid 启动 Pi
            └── 通过 supplementaryGids 中的 reader GID 只读上下文
```

- `runAsUid` 和 `runAsGid` 必须成对配置且不能为 root。
- `runAsUid` 必须不同于 publisher UID。
- Pi 的 primary GID 或 supplementary GID 必须包含 RequesterContext reader GID。
- publisher 仍是共享目录和文件的 owner；Pi 不得获得写权限。
- 未配置 `runAsUid`/`runAsGid` 时，继续使用原有同身份启动行为。

### 2.3 安全模式开关

只有以下两个变量成对存在时才启用共享 RequesterContext 安全模式：

```text
LUMI_REQUESTER_CONTEXT_ROOT=<专用 requester-context 根目录>
LUMI_REQUESTER_CONTEXT_READER_GID=<非 root reader GID>
```

缺少任意一项必须 fail closed。两项都不存在时继续使用原有进程私有的 `0700/0600` 模式。

## 3. 已确认的发布阻断

### P1：降权 Pi 无法读取 Lumi 内置 Pi ACP bridge

证据：

- `backend/internal/agent/process_unix.go` 在执行 Node 前切换到 `runAsUid`、`runAsGid` 和声明的 supplementary groups。
- `backend/internal/piacpbridge/bridge.go` 当前把 `pi-acp-bridge` 根目录和签名目录设置为 `0700`，文件设置为 `0600`。
- bridge 当前位于 `$LUMI_HOME/runtime/pi-acp-bridge`；即使只放宽 bridge 自身，`/root` 或其他私有上层目录也可能阻止降权 Pi 遍历。

结果：示例安全配置中的内置 Pi 在读取 `index.js` 前即启动失败。

#### 修复要求

1. 未配置 run-as identity 时保持当前私有 materialization 行为和 `0700/0600`，避免改变既有部署。
2. 内置 Pi 配置了 run-as identity 时，使用 publisher 控制的专用共享 bridge 根：
   - 安全模式下建议从 RequesterContext root 的父目录派生兄弟目录 `pi-acp-bridge`；
   - Local 示例：`/run/lumi/requester-context` 对应 `/run/lumi/pi-acp-bridge`；
   - Sandbox 示例：`/lumi/runtime/requester-context` 对应 `/lumi/runtime/pi-acp-bridge`；
   - 不得放在降权身份无法遍历的 `/root/...` 路径下。
3. 共享 bridge 目录使用 `0750`，文件使用 `0640`；publisher 保持 owner，group 使用 Pi 的 `runAsGid` 或另一个明确包含在 Pi groups 中的专用 GID。
4. Pi 必须可读、可遍历但不可修改 bridge；无关 UID/GID 不得读取。
5. 共享 bridge 根必须执行和 RequesterContext root 同等级的真实目录、owner、group、mode 与 symlink 校验，不能对任意既有路径执行修复式 `chmod/chown`。

建议让 `processCommand` 根据 Agent 配置显式选择 private/shared materialization，而不是让 `piacpbridge` 隐式读取零散环境状态。

### P1：`0710` 与 Lumi integration consumer 的目录枚举冲突

证据：

- `backend/internal/requestercontext/bridge.go` 中 `WithReaderGID` 正确设置目录 `0710`、文件 `0640`。
- `backend/integration/requestercontext/consumer/main.go` 的 `loadCurrentEnvelope` 使用 `os.ReadDir` 枚举信封。
- group 对 `0710` 目录只有 execute 权限，没有 read 权限，因此降权 consumer 会收到 `permission denied`。

#### 修复要求

1. 保留生产权限合同 `0710/0640`，不要改成 `0750`。
2. integration consumer 增加必填的原始 `--session-id` 输入。
3. 使用 `requestercontext.SessionFileName(sessionID)` 生成唯一允许的文件名并精确 `ReadFile`。
4. 删除“枚举所有 JSON 文件并要求只有一个活动信封”的逻辑和对应的 ambiguous-context 测试。
5. 仍需严格校验 envelope 内的 `sessionId` 与输入完全一致，不得 trim 或规范化原始 session ID。
6. 更新 `backend/integration/requestercontext/README.md` 的调用示例。

该 consumer 是 Lumi 的集成 fixture；Harness 生产读取路径已经按 session ID 精确打开，不需要修改 Harness 权限合同。

### P1：托管 Sandbox 没有收到安全模式环境变量

证据：

- `backend/cmd/device-executor/requester_context_bridge.go` 依赖 `LUMI_REQUESTER_CONTEXT_ROOT` 与 `LUMI_REQUESTER_CONTEXT_READER_GID` 启用共享模式。
- `backend/internal/sandbox/docker/containers.go` 当前只注入 Workspace、npm prefix/cache 和 PATH 等既有环境变量。
- `backend/internal/sandbox/manager.go` 创建 `ContainerSpec` 时没有传递 RequesterContext 安全配置。

结果：Agent 配置中虽然包含 `runAsUid`，device-executor 仍以 legacy 模式创建 `0700/0600` 文件，降权 Pi 无法读取。

#### 修复要求

1. 在 Sandbox manager 创建容器前解析成对的 host 安全设置，并验证 Pi run-as groups 包含 reader GID。
2. 扩展 `ContainerSpec`，只在安全模式启用时成对注入：

   ```text
   LUMI_REQUESTER_CONTEXT_ROOT=/lumi/runtime/requester-context
   LUMI_REQUESTER_CONTEXT_READER_GID=<与 host 部署一致的数值 GID>
   ```

3. 不得把宿主的 `/run/lumi/requester-context` 原样传入容器；Sandbox 的共享 runtime bind mount 位于 `/lumi/runtime`。
4. 未启用安全模式时不得注入其中任何一个变量，保持 legacy 行为。
5. 单元测试必须断言安全模式下两个变量同时存在、legacy 模式下两个变量同时不存在、部分配置被拒绝。

### P1：错误 root 配置可能修改宿主系统目录权限

证据：

- `backend/internal/requestercontext/runtime_settings.go` 当前接受任意非空绝对路径，包括 `/`、`/run` 或卷根。
- `backend/internal/requestercontext/bridge.go` 的 `ensureDir` 对 context root、Workspace 和 Agent 目录无条件执行 `chmod` 与 `chown`。
- 特权 publisher 若收到错误 root，可能更改系统目录权限或属组。

#### 修复要求

1. `RuntimeSettingsFromEnv` 至少拒绝：
   - `/` 和 Windows volume root；
   - `/run`、`/var`、`/opt`、`/tmp` 等宽泛系统目录本身；
   - basename 不是 `requester-context` 的安全 root；
   - 非 clean absolute path、NUL、无效 volume 或其他不安全表示。
2. 只管理专用 root 本身以及其下的 Workspace/Agent 目录，不修改 root 的父目录。
3. 对不存在的受管目录进行创建后，才允许设置预期 owner/group/mode。
4. 对已存在目录使用 `Lstat`：
   - 必须是真实目录，拒绝 symlink；
   - owner 必须是当前 publisher UID；
   - group 和 mode 必须已经精确匹配合同；
   - 任意不匹配均 fail closed，不得自动 `chmod/chown` “修复”。
5. 处理 create/inspect 之间的竞争，遇到并发创建时重新严格验证。
6. 对 `/`、`/run`、volume root 和 symlink 的测试必须记录操作前 mode/owner/group，并断言失败后完全未改变。

## 4. 实施顺序

1. 先补路径拒绝和 existing-directory fail-closed 测试，再重构 `ensureDir`，优先消除宿主破坏风险。
2. 修复 shared Pi ACP bridge materialization，并增加真实降权启动/read-only 测试。
3. 修改 integration consumer 为精确 session 文件读取。
4. 补齐 Sandbox 环境配置传递和容器路径转换。
5. 运行完整单元测试、Linux 跨 UID/GID 测试、交叉编译和 Sandbox E2E。
6. 创建 Lumi PR；不得直接 push 本地 `main`。
7. Lumi PR 合并或至少产生稳定可引用的候选 commit 后，更新 Harness PR #12 的跨仓库依赖并重跑 Harness release smoke。

## 5. 必须新增的验收测试

### 5.1 真实身份边界

仅检查 `Stat` 的 owner 身份测试不充分。Linux 特权测试应使用真实不同 UID/GID 启动 helper 进程，覆盖：

- publisher UID 与 Pi UID 不同；
- 降权 Pi 能启动 Lumi 内置 bridge；
- Pi 能读取 bridge 的 JS/metadata，但不能写入、替换或删除；
- 无关 UID/GID 无法读取 bridge；
- reader-group Pi 能通过已知 session ID 读取 `0710/0640` 信封；
- reader-group Pi 无法 `ReadDir` 枚举 Agent 目录；
- 无关 UID/GID 无法读取信封；
- Pi 无法修改、替换或删除 publisher-owned 信封。

测试在非 Linux 或没有所需特权时可以明确 skip，但发布 CI 必须至少有一个 Linux root/容器 job 真正执行这些断言，不能全部 skip。

### 5.2 Sandbox 配置

- secure host 设置映射为容器内 `/lumi/runtime/requester-context`。
- reader GID 数值保持一致。
- 两个环境变量只会成对出现。
- Pi config 不含 reader group 时，在启动容器前 fail closed。
- legacy 模式仍生成原来的四个基础环境变量，不增加安全变量。

### 5.3 路径安全

- 拒绝 `/`、`/run`、`/var`、`/opt`、`/tmp` 和平台 volume root。
- 拒绝 basename 非 `requester-context` 的路径。
- 拒绝 root、Workspace 或 Agent 层 symlink。
- 已存在目录 mode、owner 或 group 不匹配时失败且不修改。
- 正确的既有专用目录可复用。
- 新专用目录最终严格为 `0710`，信封严格为 `0640`。

### 5.4 回归保护

- 不设置安全环境变量时 RequesterContext 仍为原来的进程私有路径和 `0700/0600`。
- 不配置 run-as identity 时内置 Pi bridge 仍使用原有 private materialization。
- Claude、Codex、Qwen 和自定义 Agent 的启动、环境变量和文件权限行为不变。
- 自定义 Pi command 不得被错误重定向到 Lumi 内置 bridge。
- Windows 仍明确拒绝 run-as identity，并能通过交叉编译。

## 6. 建议验证命令

在本 worktree 中执行：

```bash
cd /Users/pengmd/c/lumi-pi-authz-fix/backend
go test ./...
go test ./integration/requestercontext/...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/lumi ./cmd/device-executor
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/lumi ./cmd/device-executor
```

真实跨 UID/GID 和 Sandbox E2E 应在隔离的 Linux root 容器或专用 CI runner 中执行，不能在包含其他 Lumi Sandbox 的共享 Docker daemon 上运行。执行 E2E 前先阅读 `backend/integration/requestercontext/README.md` 中的隔离警告。

## 7. 完成定义

只有同时满足以下条件，Lumi 修复才可标记完成：

- 四个 P1 均有对应代码修复和回归测试。
- `go test ./...` 通过。
- Linux 与 Windows 交叉编译通过。
- 至少一次真实不同 UID/GID 的 bridge 启动和 RequesterContext 读取测试通过。
- Sandbox 容器实际收到安全环境变量并以 `0710/0640` 发布信封。
- legacy 模式及其他 Agent 行为未改变。
- Lumi PR 说明列出与 Harness PR #12 的跨仓库关系。
- Harness PR #12 在兼容的 Lumi commit 上完成 release smoke 后，才允许合并、打 `v0.0.28` 和发布。

## 8. 提交建议

提交必须使用 Angular 格式，scope 使用英文，正文使用中文。例如：

```text
fix(authz): 修复 Pi 降权运行时权限边界
```

不要把真实 UID/GID、Bot Secret、GitHub Token、私有 registry 凭据或运行时生成的 policy/context 文件提交到仓库。
