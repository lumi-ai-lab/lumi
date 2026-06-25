package wecom

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type WeComStreamPolicy struct {
	FlushInterval       time.Duration
	SafeDuration        time.Duration
	MaxBytes            int
	LivePreviewMaxBytes int
	FinalMaxBytes       int
	MaxAge              time.Duration
	MaxUpdates          int
	MinUpdateGap        time.Duration
	CoalesceGap         time.Duration
	LongReplyNotice     string
	FallbackNotice      string
}

type WeComRuntimeConfig struct {
	StreamPolicy      WeComStreamPolicy
	MarkdownTableMode string
	IRRendererEnabled bool
}

const (
	wecomMarkdownSendMaxBytes = 4096
)

var defaultWeComStreamPolicy = WeComStreamPolicy{
	FlushInterval: 200 * time.Millisecond,
	// Keep stream replies inside WeCom's short-lived callback window. After
	// this, finish the preview and send the final answer through aibot_send_msg.
	SafeDuration: 290 * time.Second,
	// replyStream.content protocol hard limit for WeCom WebSocket AI Bot.
	MaxBytes:            20480,
	LivePreviewMaxBytes: 16000,
	FinalMaxBytes:       20000,
	MaxAge:              240 * time.Second,
	MaxUpdates:          80,
	MinUpdateGap:        200 * time.Millisecond,
	CoalesceGap:         200 * time.Millisecond,
	LongReplyNotice:     "（回答较长，以下继续发送剩余内容）",
	FallbackNotice:      "（处理时间较长，最终结果将通过普通消息发送）",
}

var defaultWeComRuntimeConfig = WeComRuntimeConfig{
	StreamPolicy:      defaultWeComStreamPolicy,
	MarkdownTableMode: "auto",
	IRRendererEnabled: true,
}

func loadWeComRuntimeConfigFromEnv() WeComRuntimeConfig {
	cfg := defaultWeComRuntimeConfig
	cfg.StreamPolicy.MaxAge = envDuration("LUMI_WECOM_STREAM_MAX_AGE", cfg.StreamPolicy.MaxAge)
	cfg.StreamPolicy.MaxBytes = envInt("LUMI_WECOM_STREAM_MAX_BYTES", cfg.StreamPolicy.MaxBytes)
	cfg.StreamPolicy.FinalMaxBytes = envInt("LUMI_WECOM_STREAM_FINAL_MAX_BYTES", cfg.StreamPolicy.FinalMaxBytes)
	cfg.StreamPolicy.MaxUpdates = envInt("LUMI_WECOM_STREAM_MAX_UPDATES", cfg.StreamPolicy.MaxUpdates)
	cfg.StreamPolicy.MinUpdateGap = envDuration("LUMI_WECOM_STREAM_MIN_UPDATE_GAP", cfg.StreamPolicy.MinUpdateGap)
	cfg.StreamPolicy.CoalesceGap = envDuration("LUMI_WECOM_STREAM_COALESCE_GAP", cfg.StreamPolicy.CoalesceGap)
	if mode := strings.TrimSpace(os.Getenv("LUMI_WECOM_MARKDOWN_TABLE_MODE")); mode != "" {
		cfg.MarkdownTableMode = strings.ToLower(mode)
	}
	cfg.IRRendererEnabled = envBool("LUMI_WECOM_IR_RENDERER", cfg.IRRendererEnabled)
	return cfg
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	if d, err := time.ParseDuration(value); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(value); err == nil && n > 0 {
		return time.Duration(n) * time.Millisecond
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envBool(name string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch value {
	case "":
		return fallback
	case "1", "true", "yes", "on", "enabled":
		return true
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return fallback
	}
}

const (
	wecomStreamFlushInterval       = 200 * time.Millisecond
	wecomStreamSafeDuration        = 290 * time.Second
	wecomStreamMaxBytes            = 20480
	wecomStreamLivePreviewMaxBytes = 16000
	wecomStreamFinalMaxBytes       = 20000
	wecomLongReplyNotice           = "（回答较长，以下继续发送剩余内容）"
	wecomStreamFallbackNotice      = "（处理时间较长，最终结果将通过普通消息发送）"
)
