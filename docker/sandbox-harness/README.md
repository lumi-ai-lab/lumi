# lumi-harness-auth（sandbox + harness-data）

基于 `ghcr.io/lumi-ai-lab/lumi-sandbox`：预装 PI，在镜像 **`/workspace`** 安装最新 `@lumi-ai-lab/harness-data`（`--data-auth`），并强制 **`authz.allow_local_blob: false`**（只认 Host 信封）。构建时还会 **删除** `config/dev-auth.blob` 与 fixture blob（仅靠 yaml 关 local 挡不住模型 `cat` 假降级为 `local-test-user`）。构建时重编 **device-executor**（auth-2，`promptWithHostAuth`）。

| 项 | 值 |
| --- | --- |
| 本地 tag | `lumi-harness-auth` |
| GHCR | `ghcr.io/lumi-ai-lab/lumi-harness-auth` |
| 架构 | **linux/amd64**（Linux x86 服务器） |
| PI | `pi-acp@0.0.33` + `@earendil-works/pi-coding-agent@0.83.0` |
| harness | npm latest + metric-cli（安装器解析） |
| Build token | `docker/sandbox-harness/.github_token`（gitignore，BuildKit secret） |

## 本地构建

```bash
cp docker/sandbox-harness/.github_token.example docker/sandbox-harness/.github_token
# 编辑为可读私有 harness / metric-cli release 的 token（单行）

# 可选代理（Docker 构建请用 host.docker.internal）
export http_proxy=http://host.docker.internal:1082
export https_proxy=http://host.docker.internal:1082

./docker/sandbox-harness/build.sh
```

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `BASE_IMAGE` | `ghcr.io/lumi-ai-lab/lumi-sandbox:latest` | 基镜像 |
| `PLATFORM` | `linux/amd64` | 目标平台 |
| `IMAGE_TAG` | `lumi-harness-auth` | 本地输出名 |

## 离线分发（推荐：docker save + scp，避开 GHCR 大包推送）

本机已是 **linux/amd64** 镜像时：

```bash
# 导出 gzip 包（默认 /tmp/lumi-harness-auth-amd64-YYYYMMDD.tar.gz）
./docker/sandbox-harness/export.sh

# 上传到服务器
scp /tmp/lumi-harness-auth-amd64-YYYYMMDD.tar.gz USER@HOST:/tmp/
```

服务器：

```bash
gunzip -c /tmp/lumi-harness-auth-amd64-YYYYMMDD.tar.gz | docker load
docker images | grep lumi-harness-auth
# 应看到 lumi-harness-auth:latest  linux/amd64
```

### 服务器 seed + 启动

```bash
# seed：bind-mount 会盖住镜像 /workspace，必须先拷到宿主机目录
mkdir -p /data/lumi-e2e-ws
docker create --name lumi-harness-seed lumi-harness-auth:latest
docker cp lumi-harness-seed:/workspace/. /data/lumi-e2e-ws/
docker rm lumi-harness-seed

# requester 清单必须在 workspace 外
lumi wecom run \
  --workspace /data/lumi-e2e-ws \
  --kind sandbox \
  --image lumi-harness-auth:latest \
  --agent pi \
  --agents pi \
  --bot-id … \
  --bot-secret … \
  --requester-config /etc/lumi/wecom-requesters.json \
  --stream
```

换镜像后若旧容器仍在：`lumi sandbox prune` 再启动。

## 推送到 GHCR（可选，网络稳定时）

```bash
./docker/sandbox-harness/publish.sh
IMAGE_TAG=v0.0.1 PUSH_LATEST=1 ./docker/sandbox-harness/publish.sh
```

CI：`.github/workflows/publish-harness-auth-image.yml`，需 secret `HARNESS_DATA_GITHUB_TOKEN`。  
镜像较大时本机经代理 push 易中断，**优先用上面的 scp 离线包**。

## Layer 0 冒烟（本机，不挂 /workspace）

```bash
docker run --rm -it --platform linux/amd64 \
  --entrypoint bash \
  lumi-harness-auth

export PATH=/lumi/runtime/npm/bin:$PATH
pi --version
grep -A6 '^authz:' /workspace/config/harness-config.yaml
# 期望 allow_local_blob: false
```

## 安全

- 构建用 token 仅 BuildKit secret，不进镜像层  
- 不要提交 `.github_token` / `.wecom-bot.env` / `wecom-requesters.json`  
- 泄露后轮换 token 并重建镜像  
