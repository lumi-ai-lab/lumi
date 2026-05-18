# IM Sandbox 并发限制记录

## 问题

当前在同一台节点上，不能可靠地同时运行多个 IM 渠道的 sandbox 模式进程。例如同时启动两个命令：

```bash
lumi wechat run --workspace /path/a --kind sandbox --agent claude --port 3000
lumi wechat run --workspace /path/b --kind sandbox --agent claude --port 3001
```

即使端口不同，这两个进程也会映射到同一个 IM CLI sandbox 工作区，无法形成两个独立隔离的 sandbox runtime。

## 当前机制

IM CLI 的 sandbox 工作区 ID 是固定的：

```text
cli-sandbox
```

这个固定 ID 会继续派生出其它运行时标识：

```text
workspace ID: cli-sandbox
container:    lumi-sandbox-cli-sandbox
device ID:    sandbox-cli-sandbox
runtime dir:  ~/.lumi/runtime/sandboxes/cli-sandbox/runtime
```

因此，`lumi wechat run --kind sandbox` 或 `lumi wecom run --kind sandbox` 中传入的 `--workspace` 只会更新这个固定 workspace 的 host path。它不会为每个不同工作目录创建一个新的 sandbox workspace ID。

## 过程与原因

当第一个 IM sandbox 进程启动时，它会配置并启动 `cli-sandbox`：

```text
workspace ID = cli-sandbox
host path    = /path/a
container    = lumi-sandbox-cli-sandbox
runtime dir  = ~/.lumi/runtime/sandboxes/cli-sandbox/runtime
```

如果随后第二个 IM sandbox 进程启动，它仍然使用同一个 `cli-sandbox`：

```text
workspace ID = cli-sandbox
host path    = /path/b
container    = lumi-sandbox-cli-sandbox
runtime dir  = ~/.lumi/runtime/sandboxes/cli-sandbox/runtime
```

这会带来几个冲突：

- `lumi.config.json` 中的 `cli-sandbox` 配置会被后启动的进程覆盖。
- Docker 容器名相同，后启动的进程可能停止并重建同名容器。
- device ID 相同，两个进程无法拥有独立的 sandbox executor 连接。
- runtime/cache 目录相同，依赖缓存和 bootstrap 状态被共享。
- WeChat/WeCom 的 channel config store 也各自只有一份当前配置，会被后启动的进程覆盖。
- 使用不同 `--port` 只能解决 HTTP 监听端口冲突，不能解决 workspace/container/device/runtime/config 冲突。

## 结果

当前结论：

```text
同一台节点上不支持多个 IM sandbox 实例并行隔离运行。
```

当前可支持的使用方式：

- 单个 IM sandbox 实例。
- 多个 IM 进程但不同时使用 sandbox。
- 使用 local workspace 模式。
- 使用不同机器/节点运行多个 sandbox 实例。

当前不可靠的使用方式：

- 同一台机器上同时运行多个 `wechat run --kind sandbox`。
- 同一台机器上同时运行多个 `wecom run --kind sandbox`。
- 同一台机器上同时运行 WeChat sandbox 和 WeCom sandbox，并期望它们隔离。
- 仅通过不同 `--port` 来尝试隔离多个 IM sandbox 实例。

## 后续改进方向

如果后续需要支持同一节点多个 IM sandbox 实例，需要为 IM CLI 引入实例级隔离标识，例如：

```bash
lumi wechat run --workspace /path/a --kind sandbox --sandbox-id wx-a --port 3000
lumi wechat run --workspace /path/b --kind sandbox --sandbox-id wx-b --port 3001
```

然后按实例 ID 派生独立资源：

```text
workspace ID: cli-sandbox-wx-a
container:    lumi-sandbox-cli-sandbox-wx-a
device ID:    sandbox-cli-sandbox-wx-a
runtime dir:  ~/.lumi/runtime/sandboxes/cli-sandbox-wx-a/runtime
```

另一个可选方案是按 workspace path hash 派生 ID，例如：

```text
cli-sandbox-<path-hash>
```

但这会让同一路径复用缓存，不同路径隔离缓存。最终应选择哪种方式，取决于产品语义：是按“IM 进程实例”隔离，还是按“工作目录”隔离。
