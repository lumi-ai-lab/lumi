# PI ACP 多 IM Session 问题的 Lumi 内置 Patch 方案

## 背景

Lumi 在 IM 场景中会把每个 IM conversation 映射到一个远端 ACP session：

```text
conversationID + deviceID + agentID -> ACP sessionId
```

下一轮消息会复用这个 `sessionId`，通过远端 `device-executor` 调用 ACP `session/prompt`。

这个模型要求同一个 ACP 进程可以同时保留多个 live session。Claude Code ACP 可以做到这一点，但当前 `pi-acp@0.0.27` 会在 `session/new` 或 `session/load` 后执行类似下面的逻辑：

```ts
this.sessions.closeAllExcept(session.sessionId)
```

这会导致新 IM conversation 创建 PI session 后，旧 IM conversation 的 session 在 PI ACP 内部被关闭。Lumi 侧仍保存旧 `sessionId`，旧 conversation 再发消息时就会遇到：

```text
Invalid params: Unknown sessionId
```

## 目标

1. 用户仍然使用原作者发布的 `pi-acp@0.0.27` 作为安装来源。
2. Lumi 在安装或启动 PI ACP 前，对这个精确版本应用一份内置 patch。
3. patch 只修复多 live session 行为，不引入 fork 包发布和安装来源切换。
4. upstream 后续修复后，Lumi 可以移除 patch 并升级默认 PI ACP 版本。
5. patch 的存在、适用版本、校验条件和移除条件都在 Lumi 仓库中可追踪。

## 非目标

1. 不发布自有 `pi-acp` fork 包。
2. 不要求用户把配置改成安装 Lumi 自己发布的 ACP 包。
3. 不在现阶段实现完整 TTL/LRU 资源治理。
4. 不依赖 `npx` cache 的偶然路径来保存 patch。

## 总体判断

如果不打算向 upstream 提 PR，就不需要 fork 远程仓库。

推荐做法是：

1. 用本地临时 clone 或 `npm pack pi-acp@0.0.27` 获取原始源码。
2. 本地改出最小修复。
3. 生成 patch 文件。
4. 把 patch 文件提交到 Lumi 仓库。
5. Lumi setup/device-executor 安装原始 `pi-acp@0.0.27` 后自动应用 patch。

fork 的价值主要在提交 PR、保存公开修改分支、方便别人查看上下文。对于 Lumi 内置临时 patch 来说，fork 不是必须。

## Patch 内容

核心修复是把 PI ACP 当前的单 live session 行为改成显式兼容模式。

新增环境变量：

```text
PI_ACP_SINGLE_LIVE_SESSION=true
```

默认不设置该变量时，PI ACP 保留多个 live session。

示例逻辑：

```ts
function shouldUseSingleLiveSession(): boolean {
  return process.env.PI_ACP_SINGLE_LIVE_SESSION === "true"
}
```

把原逻辑：

```ts
;(this.sessions as any).closeAllExcept?.(session.sessionId)
```

改成：

```ts
if (shouldUseSingleLiveSession()) {
  ;(this.sessions as any).closeAllExcept?.(session.sessionId)
}
```

需要覆盖的位置：

1. `newSession()` 创建 session 后的 `closeAllExcept`
2. `loadSession()` 加载 session 后的 `closeAllExcept`

## 仓库内管理方式

推荐在 Lumi 仓库新增以下结构：

```text
patches/
  acp/
    pi-acp/
      0.0.27/
        multi-session.patch
        README.md
```

`multi-session.patch` 是自动应用的真实 patch 文件。

`README.md` 记录：

1. patch 解决的问题。
2. patch 适用的包和版本。
3. patch 的核心行为变化。
4. patch 的生成方式。
5. patch 的验证方式。
6. patch 的移除条件。

示例：

```md
# pi-acp 0.0.27 multi-session patch

Reason:
pi-acp@0.0.27 closes all existing sessions after session/new or session/load.
This breaks Lumi IM conversations because Lumi keeps one remote ACP sessionId
per conversation.

Patch:
Only call closeAllExcept(sessionId) when PI_ACP_SINGLE_LIVE_SESSION=true.

Applies to:
pi-acp@0.0.27

Remove when:
Upstream pi-acp supports multiple live sessions by default, or provides an
equivalent config option.
```

## Patch 生成流程

建议不要直接手写 patch。使用 npm 包的发布产物生成，确保 patch 对最终安装内容有效。

流程：

```bash
mkdir -p /tmp/lumi-pi-acp-patch
cd /tmp/lumi-pi-acp-patch
npm pack pi-acp@0.0.27
mkdir original patched
tar -xzf pi-acp-0.0.27.tgz -C original --strip-components=1
cp -R original/. patched/
```

在 `patched/` 中完成代码修改后生成 patch：

```bash
diff -ruN original patched > multi-session.patch
```

检查 patch：

```bash
patch --dry-run -p1 -d original < multi-session.patch
```

然后把 patch 放入 Lumi：

```text
patches/acp/pi-acp/0.0.27/multi-session.patch
```

注意事项：

1. patch 应基于 npm tarball 内容生成，而不是只基于 GitHub 源码。
2. patch 文件应尽量小，只包含这次修复。
3. 不要把临时解压目录提交到仓库。
4. patch 文件中不要包含构建产物以外的无关变更。

## Lumi 应用 Patch 的推荐实现

### 1. 使用 Lumi 管理的 npm runtime

当前默认配置是：

```json
{
  "id": "pi",
  "name": "PI",
  "command": "npx",
  "args": ["-y", "pi-acp@0.0.27"]
}
```

如果继续直接运行 `npx -y pi-acp@0.0.27`，patch 会落在 npx cache 内。npx cache 路径不稳定，也可能被 npm 重新下载覆盖，因此不适合作为长期 patch 载体。

推荐让 Lumi 对 PI ACP 做特殊处理：

1. setup 阶段识别 `pi-acp@0.0.27`。
2. 安装原包到 Lumi 管理的 npm prefix。
3. 对该 prefix 下的 `node_modules/pi-acp` 应用 patch。
4. 启动时执行 Lumi 管理 runtime 里的 `pi-acp` bin。

建议 runtime 路径：

```text
普通设备:
~/.lumi/runtime/npm

sandbox:
/lumi/runtime/npm
```

sandbox 当前已经使用：

```text
NPM_CONFIG_PREFIX=/lumi/runtime/npm
NPM_CONFIG_CACHE=/lumi/runtime/npm-cache
PATH=/lumi/runtime/npm/bin:...
```

普通设备可以新增同类路径，避免污染用户全局 npm。

### 2. 安装原始包

对 `pi-acp@0.0.27` 执行：

```bash
npm install --prefix <lumi-npm-prefix> pi-acp@0.0.27
```

安装完成后的包目录通常是：

```text
<lumi-npm-prefix>/lib/node_modules/pi-acp
```

Windows 需要兼容 npm prefix 的目录结构和 `.cmd` bin。

### 3. 版本校验

应用 patch 前必须读取：

```text
<package-dir>/package.json
```

要求：

```json
{
  "name": "pi-acp",
  "version": "0.0.27"
}
```

如果版本不是 `0.0.27`，不得应用 patch。此时应返回明确错误或跳过 patch，并在 setup 状态中提示。

推荐错误信息：

```text
Lumi PI ACP patch only supports pi-acp@0.0.27, got pi-acp@<version>.
```

### 4. 内容校验

只做版本校验还不够。npm registry、镜像源或重新发布异常都可能导致同版本内容不同。

应用 patch 前建议校验目标文件包含预期片段，例如：

```text
closeAllExcept
```

更稳妥的方式是计算目标文件的 SHA256 或 package tarball integrity。短期可以先做片段校验，后续再补强 hash 校验。

如果找不到预期片段，应失败并提示：

```text
pi-acp@0.0.27 source does not match Lumi patch expectations.
```

### 5. 应用 patch

可以选择两种方式：

1. 调系统 `patch` 命令。
2. 在 Go 里实现一个小型 patch applicator。

短期推荐使用系统 `patch` 命令，成本低，行为直观。

示例：

```bash
patch -p1 -d <package-dir> < patches/acp/pi-acp/0.0.27/multi-session.patch
```

但需要注意：

1. Windows 环境可能没有 `patch` 命令。
2. 如果要支持 Windows，需要 Go 内置 patch 应用逻辑，或改成针对目标 TS/JS 文件的结构化替换。

考虑跨平台，最终推荐 Go 实现时不要依赖外部 `patch` 命令，而是做一个精确的文本替换：

1. 定位目标文件。
2. 校验旧片段完整存在。
3. 替换为新片段。
4. 再写入 patch marker 文件。

marker 文件：

```text
<package-dir>/.lumi-patches/pi-acp-0.0.27-multi-session.json
```

内容：

```json
{
  "id": "pi-acp-0.0.27-multi-session",
  "package": "pi-acp",
  "version": "0.0.27",
  "appliedAt": "2026-06-08T00:00:00Z"
}
```

### 6. 幂等性

patch 应用逻辑必须幂等：

1. 如果 marker 存在，检查目标文件已经包含新逻辑。
2. 如果新逻辑已存在但 marker 不存在，补写 marker。
3. 如果旧逻辑和新逻辑都不存在，判定为不兼容。
4. 不允许重复插入 helper 函数。

### 7. 启动时选择 patched bin

即使配置仍保留：

```json
"command": "npx",
"args": ["-y", "pi-acp@0.0.27"]
```

`device-executor` 在启动 agent process 前也可以做一次解析：

1. 如果 agent 是 PI。
2. 如果命令是 `npx` 且 package spec 是 `pi-acp@0.0.27`。
3. 确保 Lumi runtime 中的 patched package 已安装。
4. 把实际启动命令改成 Lumi runtime 的 bin。

Unix:

```text
<lumi-npm-prefix>/bin/pi-acp
```

Windows:

```text
<lumi-npm-prefix>/pi-acp.cmd
```

这样用户配置仍然表达“使用原作者的 `pi-acp@0.0.27`”，但实际执行的是 Lumi 管理目录下已经 patch 过的同版本包。

## sandbox 模式处理

sandbox 模式不需要把已 patch 的 `pi-acp` 直接 bake 进 Docker image。

推荐保持 sandbox image 只提供运行环境和 `device-executor`：

1. Node/npm/npx。
2. `NPM_CONFIG_PREFIX=/lumi/runtime/npm`。
3. `NPM_CONFIG_CACHE=/lumi/runtime/npm-cache`。
4. `PATH=/lumi/runtime/npm/bin:...`。
5. `device-executor`。

当前 sandbox Dockerfile 和容器启动配置已经具备这些条件：

```text
NPM_CONFIG_PREFIX=/lumi/runtime/npm
NPM_CONFIG_CACHE=/lumi/runtime/npm-cache
PATH=/lumi/runtime/npm/bin:...
```

因此 sandbox 里的处理流程应与普通设备一致：

1. 容器启动 `device-executor connect ... --install`。
2. setup 识别 `pi-acp@0.0.27`。
3. 在 `/lumi/runtime/npm` 安装原作者发布的 `pi-acp@0.0.27`。
4. 在 `/lumi/runtime/npm/lib/node_modules/pi-acp` 应用 Lumi 内置 patch。
5. 启动 PI ACP 时使用 `/lumi/runtime/npm/bin/pi-acp`。

这样做的好处：

1. sandbox image 不需要随着 patch 包内容变化而频繁重建。
2. 普通设备和 sandbox 共享同一套 patch manager 逻辑。
3. patch 状态可以通过 marker 文件和 setup 状态统一展示。
4. upstream 修复后，只需要移除 Lumi patch 逻辑并升级默认包版本，不需要维护一份特殊 sandbox 镜像。

注意事项：

1. patch 文件或 patch 规则必须随 `device-executor` 可用。推荐用 Go `embed` 把 patch 规则或精确文本替换内容编进 `device-executor`，不要依赖容器内存在 Lumi 源码目录。
2. 不要依赖系统 `patch` 命令。`node:bookworm-slim` 默认不保证有 `patch`，也会影响 Windows 普通设备。推荐 Go 里做精确文本替换。
3. setup bootstrap manifest 的签名应包含 patch id/version。否则旧 sandbox runtime 里已经安装过未 patch 的 `pi-acp@0.0.27` 时，`--install` 可能因为认为依赖已 ready 而跳过补丁。
4. 即使 status 显示 ACP package ready，也应在启动 PI ACP 前调用一次 `EnsurePiACPPatched` 做幂等校验，避免 runtime 目录被手动改动或缓存恢复成未 patch 状态。

是否需要改 sandbox image：

1. 如果 patch manager 使用 Go `embed`，通常只需要重新构建包含新版 `device-executor` 的 sandbox image，不需要额外修改 Dockerfile。
2. 如果选择把 patch 文件作为外部文件读取，则需要在 Dockerfile 中 `COPY patches/...` 到镜像或挂载到容器内。但不推荐这种方式，因为普通设备和 sandbox 会出现两套路径处理。
3. 如果错误地依赖外部 `patch` 命令，则需要在 Dockerfile 里安装 `patch` 包。但该方案不推荐。

## Release 产物影响

本方案不会改变用户安装 PI ACP 的来源，但会改变 Lumi 自身 release 产物。

需要重新发布：

1. `device-executor` binary。
   - patch manager、版本校验、幂等应用、启动时 patched bin 解析都在 `device-executor` 内。
2. sandbox image。
   - sandbox image 中包含 `/usr/local/bin/device-executor`，因此需要重新构建并发布包含新版 `device-executor` 的 image。
   - 通常不需要修改 Dockerfile 结构，也不需要把 patched `pi-acp` bake 进 image。
3. Lumi backend/desktop release。
   - 如果 setup 状态展示 patch 状态，或普通设备安装逻辑由 Lumi backend/desktop 暴露，则对应 release 也需要更新。

不需要发布：

1. 自有 `pi-acp` fork 包。
2. 单独的 patch 文件下载包。
3. 特殊 sandbox runtime 目录结构。

### Go embed 选择

推荐用 Go `embed` 把 patch 规则或精确文本替换内容编进 `device-executor`。

理由：

1. 普通设备和 sandbox 使用同一份 patch 逻辑。
2. release 时不需要额外分发 `patches/...` 外部文件。
3. sandbox Dockerfile 不需要额外 `COPY patches/...`。
4. 避免运行时路径差异，例如源码目录、安装目录、容器目录不同。
5. 避免用户缺少外部 patch 文件导致 setup/install 失败。

实现上可以仍在 Lumi 仓库保留 patch 说明文件：

```text
patches/acp/pi-acp/0.0.27/README.md
```

但真正执行用的 patch 内容建议以 Go 常量或 embedded asset 进入：

```text
backend/internal/acppatch/
  pi_acp.go
  pi_acp_patch.txt
```

其中 `pi_acp_patch.txt` 可以是：

1. 传统 unified diff，仅作为记录和测试输入。
2. 或精确替换所需的 old/new 文本片段。

考虑跨平台和幂等性，最终执行路径推荐使用精确文本替换，而不是调用系统 `patch` 命令。

## setup 状态展示

setup 页面和 CLI 建议保留 package 来源显示：

```text
pi-acp@0.0.27
```

同时增加 patch 状态：

```text
Installed with Lumi patch: pi-acp-0.0.27-multi-session
```

失败时显示具体原因：

```text
Failed to apply Lumi patch pi-acp-0.0.27-multi-session: source does not match expected content.
```

这能避免用户误以为已经切换到了 Lumi fork。

## 与现有 Lumi 代码的关联点

预计需要关注以下模块：

```text
backend/internal/config/config.go
backend/internal/setupcheck/checker.go
backend/internal/setupcheck/install.go
backend/internal/agent/process.go
backend/cmd/device-executor/runner.go
docker/sandbox/Dockerfile
```

职责建议：

1. `config.go` 继续保留默认 `npx -y pi-acp@0.0.27`，不强迫用户改配置。
2. `setupcheck` 识别 `pi-acp@0.0.27` 并展示 patch 状态。
3. `install.go` 对 PI ACP 使用 Lumi runtime install + patch。
4. agent 启动前解析 patched executable。
5. sandbox 继续使用 `/lumi/runtime/npm`。

如果希望边界更清晰，可以新增包：

```text
backend/internal/acppatch/
  pi_acp.go
  runtime.go
  patch.go
  patch_test.go
```

核心 API 示例：

```go
type PatchStatus struct {
    Package string
    Version string
    PatchID string
    Applied bool
    Message string
}

func EnsurePiACPPatched(packageSpec string, opts RuntimeOptions) (PatchStatus, error)
func ResolvePiACPExecutable(opts RuntimeOptions) (string, []string, error)
```

## Lumi 侧兜底

PI ACP patch 是主修复，但仍建议 Lumi 保留远端 session 失效的一次性恢复机制。

仍可能导致旧 `sessionId` 失效的场景：

1. device-executor 重启。
2. PI ACP 进程重启。
3. 设备断线重连。
4. 用户手动删除 PI session 文件。
5. 未来 ACP 实现主动清理 idle session。

兜底逻辑：

1. `session/prompt` 返回 `Unknown sessionId`、`Session not found`、`Invalid params` 等错误。
2. Lumi 清除 `conversationID + deviceID + agentID` 的 remote session 映射。
3. 不带 `sessionId` 重试一次。
4. device-executor 重新 `session/new`。
5. Lumi 保存新的 remote `sessionId`。
6. 只重试一次，避免循环。

注意：这个兜底会丢失旧 PI live session 的上下文连续性，所以它只是可用性保护，不是主修复。

## 验证方案

### 1. Patch 文件验证

使用 npm tarball 验证：

```bash
npm pack pi-acp@0.0.27
tar -xzf pi-acp-0.0.27.tgz -C /tmp/pi-acp-original --strip-components=1
patch --dry-run -p1 -d /tmp/pi-acp-original < patches/acp/pi-acp/0.0.27/multi-session.patch
```

期望：

```text
patch applies cleanly
```

### 2. Lumi patch manager 单测

覆盖：

1. `pi-acp@0.0.27` 可成功应用 patch。
2. 非 `0.0.27` 版本拒绝应用。
3. 已应用 patch 时重复执行保持幂等。
4. 目标文件内容不匹配时返回明确错误。
5. marker 缺失但新逻辑存在时补写 marker。

### 3. PI ACP 行为验证

用 fake PI process 或真实 PI CLI 验证：

1. `session/new` 得到 `sid1`。
2. `session/prompt sid1` 成功。
3. `session/new` 得到 `sid2`。
4. `session/prompt sid2` 成功。
5. 再 `session/prompt sid1` 成功。

兼容模式验证：

1. 设置 `PI_ACP_SINGLE_LIVE_SESSION=true`。
2. 重复上述流程。
3. 第 5 步应返回 `Unknown sessionId` 或等价错误。

### 4. Lumi IM 集成验证

流程：

1. IM conversation A 对 PI 发消息，保存远端 `sidA`。
2. IM conversation B 对 PI 发消息，保存远端 `sidB`。
3. 回到 conversation A 继续发消息。

期望：

1. conversation A 不再报 `Invalid params: Unknown sessionId`。
2. device-executor 没有为 A 重新创建 session。
3. PI ACP 日志中没有因为 B 创建 session 而关闭 A。

### 5. setup 验证

普通设备：

1. 清理 Lumi runtime npm 目录。
2. 运行 `device-executor setup --install`。
3. 确认安装 `pi-acp@0.0.27`。
4. 确认 patch marker 存在。
5. 确认启动命令使用 Lumi runtime bin。

sandbox：

1. 构建 sandbox 镜像。
2. 启动 sandbox device-executor。
3. 确认 `/lumi/runtime/npm` 下安装原包并应用 patch。
4. 确认 PATH 中优先使用 `/lumi/runtime/npm/bin`。

## 风险和缓解

### 风险 1：npx cache 覆盖 patch

缓解：

不要把 patch 应用到 npx cache。使用 Lumi 管理的 npm runtime。

### 风险 2：同版本包内容变化

缓解：

应用 patch 前做版本和内容校验。必要时增加 SHA256 校验。

### 风险 3：Windows 没有 patch 命令

缓解：

Go 实现中不要依赖外部 `patch` 命令，使用精确文本替换或内置 patch applicator。

### 风险 4：多 live session 增加 PI subprocess 数量

缓解：

短期接受该风险，因为会话正确性优先。后续再实现 idle TTL 和 max live sessions LRU。

### 风险 5：用户已有自定义 PI 配置

缓解：

只自动处理明确识别为 `pi-acp@0.0.27` 的配置。自定义 command 或非目标版本不强行 patch，只给提示。

## 移除 Patch 的条件

满足以下任一条件即可考虑移除：

1. upstream `pi-acp` 默认支持同一 ACP 进程内多个 live session。
2. upstream 提供等价配置项，默认或 Lumi 可配置为多 live session。
3. Lumi 默认 PI ACP 版本升级到已修复版本。

移除步骤：

1. 删除 `patches/acp/pi-acp/0.0.27/`。
2. 删除 `backend/internal/acppatch` 中对应 patch 逻辑。
3. 更新默认 PI ACP 版本。
4. 更新 README 和 setup 文案。
5. 保留 `Unknown sessionId` 一次性恢复兜底。
6. 跑完整 IM 多 session 回归验证。

## 推荐实施顺序

1. 生成 `pi-acp@0.0.27` 的 `multi-session.patch`。
2. 在 Lumi 仓库加入 patch 文件和说明。
3. 新增 `backend/internal/acppatch`，实现安装、校验、应用和 marker。
4. 修改 setup/install，让 `pi-acp@0.0.27` 走 Lumi managed npm runtime。
5. 修改 agent 启动解析，让 PI ACP 使用 patched bin。
6. 增加单测和 patch 幂等测试。
7. 做 IM 多 conversation 集成验证。
8. 增加 Lumi 侧 `Unknown sessionId` 一次性重建兜底。
9. 后续再实现 PI ACP TTL/LRU 或等待 upstream 修复。

## 最终建议

采用 Lumi 内置 patch 方案。

用户看到和配置的仍是原作者的 `pi-acp@0.0.27`，Lumi 只是在受控 runtime 内对这个精确版本应用临时修复。这样既避免用户切换安装来源，也避免维护自有 fork 包。后续 upstream 修复后，Lumi 只需要升级默认版本并移除 patch 逻辑。

## Hand-off：GitHub Release 用户升级指引

本节用于发布 GitHub Release 后给已有用户升级。

### 结论

如果用户配置中的 PI agent 仍是默认形式：

```json
{
  "id": "pi",
  "command": "npx",
  "args": ["-y", "pi-acp@0.0.27"]
}
```

则不需要修改配置文件。用户只需要更新 Lumi release 产物，并重启对应进程或容器。

新版 Lumi 会在启动 PI agent 时自动把 `npx -y pi-acp@0.0.27` 解析到 Lumi managed runtime 中已经 patch 过的同版本 `pi-acp`：

```text
~/.lumi/runtime/npm/bin/pi-acp
```

sandbox 容器内对应为：

```text
/lumi/runtime/npm/bin/pi-acp
```

### 普通本机 Web 用户

适用场景：

1. 用户直接运行 GitHub Release 下载的 `lumi` 二进制。
2. agent 也在同一台机器上启动。
3. 没有使用 sandbox Docker 容器。

升级步骤：

```bash
# 1. 停止旧 lumi 进程
# 按用户自己的部署方式停止，例如 Ctrl+C、systemctl stop lumi 等。

# 2. 从 GitHub Release 下载并替换新版 lumi 二进制
# 示例：
chmod +x ./lumi

# 3. 运行 setup，确保 managed runtime 中安装并应用 PI ACP patch
./lumi setup --config ~/.lumi/lumi.config.json

# 4. 重启 lumi
./lumi --config ~/.lumi/lumi.config.json
```

验证：

```bash
test -f ~/.lumi/runtime/npm/lib/node_modules/pi-acp/.lumi-patches/pi-acp-0.0.27-multi-session.json
```

首次启动 PI 时日志应出现：

```text
Using Lumi managed agent [pi]: .../.lumi/runtime/npm/bin/pi-acp
```

### 独立 device-executor 用户

适用场景：

1. Lumi server 在一台机器。
2. `device-executor` 在另一台普通机器上运行。
3. 该机器不是 sandbox Docker 容器。

升级步骤：

```bash
# 1. 停止旧 device-executor

# 2. 从 GitHub Release 下载并替换新版 device-executor 二进制
chmod +x ./device-executor

# 3. 运行安装检查和 patch
./device-executor setup --install --config ~/.lumi/lumi.config.json

# 4. 重新连接 Lumi server
./device-executor connect --server <lumi-server-url> --token <pairing-token> --config ~/.lumi/lumi.config.json
```

验证：

```bash
test -f ~/.lumi/runtime/npm/lib/node_modules/pi-acp/.lumi-patches/pi-acp-0.0.27-multi-session.json
```

日志应出现：

```text
Using Lumi managed agent [pi]: .../.lumi/runtime/npm/bin/pi-acp
```

### sandbox Docker 用户

适用场景：

1. 用户通过 Lumi sandbox workspace 启动任务。
2. `device-executor` 运行在 sandbox Docker 容器内。
3. sandbox image 来自 GitHub Release 对应发布的 image。

这类用户需要同时更新：

1. 宿主机上的 `lumi` release 二进制。
2. sandbox image。
3. 已运行的旧 sandbox 容器。

升级步骤：

```bash
# 1. 停止旧 lumi 进程

# 2. 从 GitHub Release 下载并替换新版 lumi 二进制
chmod +x ./lumi

# 3. 拉取 GitHub Release 对应的新版 sandbox image
docker pull ghcr.io/lumi-ai-lab/lumi-sandbox:<release-version>

# 如果配置使用 latest，也可以拉取 latest：
docker pull ghcr.io/lumi-ai-lab/lumi-sandbox:latest
```

如果用户配置里 workspace image 是固定版本 tag，需要把 `lumi.config.json` 中 sandbox workspace 的 `image` 更新到 Release 对应 tag：

```json
{
  "kind": "sandbox",
  "image": "ghcr.io/lumi-ai-lab/lumi-sandbox:<release-version>"
}
```

如果用户配置里已经使用：

```json
"image": "ghcr.io/lumi-ai-lab/lumi-sandbox:latest"
```

则通常不需要修改配置，只需要确保服务器已经 `docker pull` 到新 image。

删除旧 sandbox 容器，让 Lumi 用新 image 重新创建：

```bash
docker ps -a --filter label=lumi.runtime=sandbox \
  --format '{{.ID}} {{.Names}} {{.Image}}'

docker rm -f $(docker ps -aq --filter label=lumi.runtime=sandbox)
```

注意：不要只执行 `docker restart` 旧 sandbox 容器。旧容器内的 `/usr/local/bin/device-executor` 来自旧 image，只重启不会更新。

重启宿主机 Lumi：

```bash
./lumi --config ~/.lumi/lumi.config.json
```

随后在 Web 中重新触发 sandbox workspace。新容器启动时会自动运行：

```text
device-executor connect ... --install
```

它会在容器内安装并 patch：

```text
/lumi/runtime/npm/lib/node_modules/pi-acp
```

验证 sandbox 容器：

```bash
docker logs lumi-sandbox-<workspace-id> | grep 'Using Lumi managed agent'
```

期望看到：

```text
Using Lumi managed agent [pi]: /lumi/runtime/npm/bin/pi-acp
```

也可以确认 patch marker：

```bash
docker exec lumi-sandbox-<workspace-id> \
  test -f /lumi/runtime/npm/lib/node_modules/pi-acp/.lumi-patches/pi-acp-0.0.27-multi-session.json
```

### 配置文件是否需要改

一般不需要改 PI agent 配置。推荐继续保留：

```json
{
  "id": "pi",
  "name": "PI",
  "command": "npx",
  "args": ["-y", "pi-acp@0.0.27"],
  "sessionMode": "default"
}
```

不要统一改成绝对路径，例如：

```text
~/.lumi/runtime/npm/bin/pi-acp
```

原因是普通设备、服务器、容器和 Windows 的 runtime 路径不同。新版 Lumi 会自动解析，不需要用户在配置里维护机器相关路径。

只有一种临时例外：用户无法升级 Lumi 或 device-executor 二进制时，可以把 PI command 临时改成本机 patched `pi-acp` 绝对路径。但这不是推荐的 release 升级方案。

### 用户验证用例

升级完成后建议执行：

1. 在 Web 创建一个 conversation，对 PI 发一条消息，记住它能正常回复。
2. 打开另一个 conversation，也对 PI 发一条消息。
3. 回到第一个 conversation，继续对 PI 发消息。

期望：

1. 不再出现 `Invalid params`。
2. 不再出现 `Unknown sessionId`。
3. PI 启动时的版本升级提示不再显示给 Web 用户。
4. 日志中可以看到 `Using Lumi managed agent [pi]`。
