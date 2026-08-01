package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

var secretValuePattern = regexp.MustCompile(`(?i)(sk-[a-z0-9][a-z0-9_-]{4,})`)

func redactLogValue(value string) string {
	if redactedJSON, ok := redactStructuredLog(value); ok {
		value = redactedJSON
	}
	redacted := secretValuePattern.ReplaceAllString(value, "sk-<redacted>")
	for _, key := range []string{
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_API_KEY",
		"OPENAI_API_KEY",
		"API_KEY",
		"TOKEN",
		"SECRET",
	} {
		redacted = redactEnvAssignment(redacted, key)
	}
	return redacted
}

func redactStructuredLog(value string) (string, bool) {
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) != nil {
		return "", false
	}
	redactStructuredValue(decoded)
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func redactStructuredValue(value any) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			redactStructuredValue(item)
		}
	case map[string]any:
		for key, item := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			switch {
			case key == "_meta", normalized == "prompt", normalized == "cwd",
				normalized == "workspacepath", normalized == "sessionid", normalized == "conversationid",
				normalized == "userid", normalized == "chatid", normalized == "reqid", normalized == "requestid",
				normalized == "contexttoken", normalized == "apibase", normalized == "server", normalized == "serverurl",
				normalized == "content", normalized == "message", normalized == "toolcall", normalized == "rawinput",
				normalized == "rawoutput", normalized == "arguments", normalized == "headers", normalized == "authorization",
				normalized == "cookie", normalized == "path", normalized == "filepath", normalized == "locations",
				normalized == "command", normalized == "diff", normalized == "oldtext", normalized == "newtext",
				normalized == "env", normalized == "url", normalized == "uri":
				typed[key] = "<redacted>"
			case strings.Contains(normalized, "token"), strings.Contains(normalized, "secret"), strings.Contains(normalized, "password"):
				typed[key] = "<redacted>"
			default:
				redactStructuredValue(item)
			}
		}
	}
}

func redactEnvAssignment(value string, key string) string {
	idx := strings.Index(strings.ToUpper(value), key+"=")
	if idx < 0 {
		return value
	}

	start := idx + len(key) + 1
	end := start
	for end < len(value) {
		switch value[end] {
		case ' ', '\n', '\r', '\t', '"', '\'':
			return value[:start] + "<redacted>" + value[end:]
		default:
			end++
		}
	}
	return value[:start] + "<redacted>"
}

func isSensitiveEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	return strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "SECRET") ||
		strings.Contains(upper, "PASSWORD") ||
		strings.Contains(upper, "API_KEY") ||
		strings.HasSuffix(upper, "_KEY")
}
