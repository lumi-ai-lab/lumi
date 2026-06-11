# 不停 Docker 容器更新/重启 Lumi 进程

本文说明在 IM sandbox 模式下，如何在不停止已有 sandbox Docker 容器的前提下更新或重启 Lumi 进程。

## 适用前提

只有同时满足以下条件时，才建议使用本文方案：

- 本次更新没有修改 `cmd/device-executor`。
- 本次更新没有修改 `docker/sandbox/Dockerfile` 或 sandbox 镜像内的运行环境。
- 本次更新没有修改容器启动契约，包括 `device-executor connect` 参数、挂载路径、环境变量、Docker label 语义等。
- 本次更新没有修改 executor 配置文件结构到旧 `device-executor` 无法兼容的程度。

如果涉及以上内容，应重建 sandbox 镜像，并重建对应 sandbox 容器。

## 原理

Lumi 进程退出时会保留 sandbox 容器。新的 Lumi 进程启动后，会通过两部分信息恢复已有容器：

- `$LUMI_HOME/runtime/sandboxes.json`：记录 workspace、device、container name、image、host path 和运行状态。
- Docker labels：容器上带有 `lumi.runtime=sandbox`、`lumi.workspace_id=<workspace-id>`、`lumi.device_id=<device-id>`。

因此，重启前后必须使用同一个 `LUMI_HOME`。如果 `LUMI_HOME` 改变，新进程会读取另一个 runtime store，可能无法识别旧容器。

## 推荐部署约定

所有启动脚本显式固定 `LUMI_HOME`，不要依赖不同用户或不同 shell 下的默认值：

```bash
export LUMI_HOME=/data/t2s/lumi-home
export LUMI_PORT=3000
```

如果同一台机器上有多个 Lumi 进程需要共存，确保每个进程的 `LUMI_HOME` 策略是刻意设计的。若它们要共享同一批 sandbox runtime，应使用同一个 `LUMI_HOME`。

## 更新步骤

以下示例假设 Lumi 源码位于 `/data/t2s/lumi/main`，运行目录为 `/data/t2s/lumi/main/backend`。请按实际路径替换。

### 1. 记录当前容器

```bash
docker ps --filter label=lumi.runtime=sandbox \
  --format 'table {{.Names}}\t{{.Status}}\t{{.Image}}'
```

同时确认当前 Lumi 进程使用的 `LUMI_HOME`：

```bash
echo "$LUMI_HOME"
ls -l "$LUMI_HOME/runtime/sandboxes.json"
```

### 2. 构建新的 Lumi 进程

```bash
cd /data/t2s/lumi/main/backend
go build -buildvcs=false -o lumi ./cmd/lumi
```

如果前端也有更新，先构建前端，再构建后端：

```bash
cd /data/t2s/lumi/main/web
npm install
npm run build

cd /data/t2s/lumi/main/backend
go build -buildvcs=false -o lumi ./cmd/lumi
```

### 3. 停止旧 Lumi 进程

使用业务当前的进程管理方式停止 Lumi，例如 `systemd`：

```bash
sudo systemctl stop lumi
```

或向旧进程发送正常退出信号：

```bash
kill -TERM <lumi-pid>
```

不要执行以下操作，除非本次更新确实需要重建 sandbox 容器：

```bash
docker stop <sandbox-container>
docker rm <sandbox-container>
docker compose down
docker system prune
```

### 4. 启动新 Lumi 进程

确保启动环境继续使用同一个 `LUMI_HOME`：

```bash
export LUMI_HOME=/data/t2s/lumi-home
export LUMI_PORT=3000

cd /data/t2s/lumi/main/backend
./lumi
```

如果使用 `systemd`，建议把 `LUMI_HOME` 写入 unit 文件或 EnvironmentFile，避免手工 shell 环境丢失：

```ini
[Service]
Environment=LUMI_HOME=/data/t2s/lumi-home
Environment=LUMI_PORT=3000
WorkingDirectory=/data/t2s/lumi/main/backend
ExecStart=/data/t2s/lumi/main/backend/lumi
```

然后重启服务：

```bash
sudo systemctl daemon-reload
sudo systemctl start lumi
```

### 5. 验证容器没有被替换

重启前后比较容器名和容器 ID：

```bash
docker ps --filter label=lumi.runtime=sandbox \
  --format 'table {{.ID}}\t{{.Names}}\t{{.Status}}\t{{.Image}}'
```

确认 runtime store 仍能看到对应 workspace：

```bash
cat "$LUMI_HOME/runtime/sandboxes.json"
```

如果 IM sandbox 能继续收发消息，并且容器 ID 没有变化，说明本次更新没有重建 sandbox 容器。

## 什么时候仍会重建容器

即使按本文步骤操作，以下情况也可能导致容器被停止并重新创建：

- 用户或接口显式 terminate/prune sandbox。
- sandbox 超过 idle timeout，被 GC 清理。
- 某个 workspace 重新走 `Ensure` 创建路径，Lumi 会先移除同名旧容器再创建新容器。
- `LUMI_HOME` 不一致，导致新 Lumi 无法识别旧容器。
- 多个 Lumi 进程使用不同 `LUMI_HOME` 但共享同一个 Docker daemon，旧版本中可能把其他 runtime store 的容器当作 unknown container 清理。

## 回滚

如果新 Lumi 进程启动后异常，但没有改动 `device-executor` 或 sandbox 镜像，可以只回滚 Lumi 二进制并再次重启 Lumi 进程：

```bash
sudo systemctl stop lumi
cp /path/to/backup/lumi /data/t2s/lumi/main/backend/lumi
sudo systemctl start lumi
```

回滚期间同样不要停止或删除 sandbox 容器。

## 快速检查清单

- 确认本次更新不涉及 `device-executor`、sandbox 镜像或容器启动契约。
- 确认新旧进程使用同一个 `LUMI_HOME`。
- 停 Lumi 进程前记录 `docker ps --filter label=lumi.runtime=sandbox`。
- 只停止 Lumi 进程，不执行 Docker stop/rm/down/prune。
- 启动新 Lumi 后确认容器 ID 未变化。
- 验证 IM sandbox 收发消息正常。
