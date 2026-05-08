package cron

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	createBlockRegex = regexp.MustCompile(`(?is)\[CRON_CREATE\]\s*(.*?)\s*\[/CRON_CREATE\]`)
	listRegex        = regexp.MustCompile(`(?i)\[CRON_LIST\]`)
	deleteRegex      = regexp.MustCompile(`(?i)\[CRON_DELETE:\s*([^\]]+)\]`)
	pauseRegex       = regexp.MustCompile(`(?i)\[CRON_PAUSE:\s*([^\]]+)\]`)
	resumeRegex      = regexp.MustCompile(`(?i)\[CRON_RESUME:\s*([^\]]+)\]`)
	runRegex         = regexp.MustCompile(`(?i)\[CRON_RUN:\s*([^\]]+)\]`)
	codeBlockRegex   = regexp.MustCompile("(?s)```.*?```")
)

func DetectCommands(content string) []Command {
	clean := codeBlockRegex.ReplaceAllString(content, "")
	commands := make([]Command, 0)
	for _, match := range createBlockRegex.FindAllStringSubmatch(clean, -1) {
		if cmd, ok := parseCreateCommand(match[1]); ok {
			commands = append(commands, cmd)
		}
	}
	if listRegex.MatchString(clean) {
		commands = append(commands, Command{Kind: "list"})
	}
	for _, match := range deleteRegex.FindAllStringSubmatch(clean, -1) {
		if id := strings.TrimSpace(match[1]); id != "" {
			commands = append(commands, Command{Kind: "delete", JobID: id})
		}
	}
	for _, match := range pauseRegex.FindAllStringSubmatch(clean, -1) {
		if id := strings.TrimSpace(match[1]); id != "" {
			commands = append(commands, Command{Kind: "pause", JobID: id})
		}
	}
	for _, match := range resumeRegex.FindAllStringSubmatch(clean, -1) {
		if id := strings.TrimSpace(match[1]); id != "" {
			commands = append(commands, Command{Kind: "resume", JobID: id})
		}
	}
	for _, match := range runRegex.FindAllStringSubmatch(clean, -1) {
		if id := strings.TrimSpace(match[1]); id != "" {
			commands = append(commands, Command{Kind: "run", JobID: id})
		}
	}
	return commands
}

func StripCommands(content string) string {
	clean := createBlockRegex.ReplaceAllString(content, "")
	clean = listRegex.ReplaceAllString(clean, "")
	clean = deleteRegex.ReplaceAllString(clean, "")
	clean = pauseRegex.ReplaceAllString(clean, "")
	clean = resumeRegex.ReplaceAllString(clean, "")
	clean = runRegex.ReplaceAllString(clean, "")
	clean = regexp.MustCompile(`\n{3,}`).ReplaceAllString(clean, "\n\n")
	return strings.TrimSpace(clean)
}

func parseCreateCommand(body string) (Command, bool) {
	fields := parseFields(body)
	name := strings.TrimSpace(fields["name"])
	prompt := strings.TrimSpace(fields["message"])
	if prompt == "" {
		prompt = strings.TrimSpace(fields["prompt"])
	}
	scheduleText := strings.TrimSpace(fields["schedule"])
	if name == "" || prompt == "" || scheduleText == "" {
		return Command{}, false
	}
	schedule, ok := ParseSchedule(scheduleText)
	if !ok {
		return Command{}, false
	}
	return Command{
		Kind:                "create",
		Name:                name,
		Schedule:            schedule,
		ScheduleDescription: strings.TrimSpace(fields["schedule_description"]),
		Prompt:              prompt,
	}, true
}

func parseFields(body string) map[string]string {
	keys := regexp.MustCompile(`(?im)^(name|schedule|schedule_description|message|prompt):\s*`)
	matches := keys.FindAllStringIndex(body, -1)
	names := keys.FindAllStringSubmatch(body, -1)
	fields := map[string]string{}
	for i, match := range matches {
		key := strings.ToLower(names[i][1])
		start := match[1]
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		fields[key] = strings.TrimSpace(body[start:end])
	}
	return fields
}

func ParseSchedule(value string) (Schedule, bool) {
	text := strings.TrimSpace(strings.ToLower(value))
	if strings.HasPrefix(text, "interval:") {
		minutes, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(text, "interval:")))
		if err != nil || minutes <= 0 {
			return Schedule{}, false
		}
		return Schedule{Type: "interval", EverySeconds: int64(minutes) * 60}, true
	}
	if strings.HasPrefix(text, "once:") {
		raw := strings.TrimSpace(strings.TrimPrefix(value, "once:"))
		if ts, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return Schedule{Type: "once", RunAt: ts}, true
		}
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return Schedule{Type: "once", RunAt: parsed.UnixMilli()}, true
		}
		if parsed, err := time.ParseInLocation("2006-01-02 15:04", raw, time.Local); err == nil {
			return Schedule{Type: "once", RunAt: parsed.UnixMilli()}, true
		}
		return Schedule{}, false
	}
	if strings.HasPrefix(text, "every ") && strings.HasSuffix(text, " minutes") {
		raw := strings.TrimSuffix(strings.TrimPrefix(text, "every "), " minutes")
		minutes, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || minutes <= 0 {
			return Schedule{}, false
		}
		return Schedule{Type: "interval", EverySeconds: int64(minutes) * 60}, true
	}
	return Schedule{}, false
}
