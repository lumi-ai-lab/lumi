# syntax=docker/dockerfile:1

FROM node:22-bookworm-slim AS runtime

ARG DEVICE_EXECUTOR_BIN=docker/sandbox/bin/device-executor

ENV NODE_ENV=production
ENV NPM_CONFIG_UPDATE_NOTIFIER=false
ENV NPM_CONFIG_FUND=false
ENV NPM_CONFIG_AUDIT=false

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       bash \
       ca-certificates \
       curl \
       git \
       openssh-client \
       tini \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g \
      @anthropic-ai/claude-code \
      @openai/codex \
      @qwen-code/qwen-code \
      @agentclientprotocol/claude-agent-acp@0.30.0 \
      @zed-industries/codex-acp \
    && npm cache clean --force

COPY ${DEVICE_EXECUTOR_BIN} /usr/local/bin/device-executor

RUN mkdir -p /workspace /lumi/device-executor /root/.claude /root/.codex /root/.config \
    && chmod 0755 /usr/local/bin/device-executor

WORKDIR /workspace

ENTRYPOINT ["/usr/bin/tini", "--", "device-executor"]
CMD ["--help"]
