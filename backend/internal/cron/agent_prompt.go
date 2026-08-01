package cron

import (
	"fmt"
	"strings"
)

const AgentToolInstructions = `You are running inside Lumi.

When the user asks you to do something on a schedule, use Bash:

  lumi cron add --cron "<min> <hour> <day> <month> <weekday>" --prompt "<task description>" --desc "<short label>"

If LUMI_CLI is set, use "$LUMI_CLI" instead of "lumi":

  "$LUMI_CLI" cron add --cron "<min> <hour> <day> <month> <weekday>" --prompt "<task description>" --desc "<short label>"

The Lumi runtime preconfigures these values when available:

  LUMI_API_BASE
  LUMI_WORKSPACE_ID
  LUMI_WORKSPACE_PATH
  LUMI_CLI

Session-specific channel, conversation, and agent routing values are supplied
separately in the current Lumi Session context. Use the routing flags shown
there for commands that must remain scoped to this conversation.

Examples:

  lumi cron add --cron "0 8 * * *" --prompt "检查项目状态并总结" --desc "每日项目状态"
  lumi cron add --cron "0 9 * * 1" --prompt "生成本周项目进展报告" --desc "每周项目报告"
  lumi cron add --cron "*/30 * * * *" --exec "df -h" --session-mode new-per-run --timeout-mins 5 --desc "磁盘空间检查"
  lumi cron list
  lumi cron info <job-id>
  lumi cron edit <job-id> cronExpr "0 10 * * *"
  lumi cron edit <job-id> enabled false
  lumi cron edit <job-id> enabled true
  lumi cron edit <job-id> mute true
  lumi cron edit <job-id> mute false
  lumi cron edit <job-id> silent true
  lumi cron edit <job-id> timeoutMins 60
  lumi cron del <job-id>

Pause or stop a scheduled task by setting enabled false. Resume it with enabled true.
Mute means the task still runs but sends no start or result messages. Silent only suppresses the start notification.

Do not output internal scheduling protocols. Use the CLI for scheduling control.`

const IMRunToolInstructions = `When running inside an IM channel, if a long-running command generates an intermediate image or file that the user must see before the command exits, wrap it with:

  "$LUMI_CLI" im run --image-out IMAGE_PATH --sh '<command that writes "$IMAGE_PATH">'

Prefer the command's own file output flag when it has one, for example a QR or image output option. Do not use shell redirection from stdout into the image path for QR-login flows unless the command explicitly writes a binary image file there.

Do not directly run QR-login commands that wait for user interaction, because their output may not reach the IM user until the command exits.
Use this for QR codes, screenshots, reports, and other intermediate files that must be delivered while the command is still running.`

// AgentBaseInstructionsForChannel returns stable instructions only. Runtime
// addresses, workspace paths, requester identities, reply handles, and tokens
// are deliberately excluded.
func AgentBaseInstructionsForChannel(channel string) string {
	instructions := AgentToolInstructions
	if channel == ChannelWeCom {
		instructions += "\n\n" + IMRunToolInstructions
	}
	return instructions
}

func WithAgentToolInstructions(prompt string) string {
	return WithAgentToolInstructionsForContext(prompt, ToolContext{})
}

type ToolContext struct {
	APIBase        string
	Channel        string
	ConversationID string
	AgentID        string
	WorkspaceID    string
	WorkspacePath  string
	Target         Target
}

func AgentToolInstructionsForContext(ctx ToolContext) string {
	instructions := AgentBaseInstructionsForChannel(ctx.Channel)
	if routing := AgentRoutingInstructionsForContext(ctx); routing != "" {
		instructions += "\n\n" + routing
	}
	return instructions
}

// AgentRoutingInstructionsForContext provides only stable Session routing
// handles. API addresses, absolute paths, requester identity, reply handles,
// and tokens remain out of band and are never rendered into these commands.
func AgentRoutingInstructionsForContext(ctx ToolContext) string {
	channel := strings.TrimSpace(ctx.Channel)
	conversationID := strings.TrimSpace(ctx.ConversationID)
	agentID := strings.TrimSpace(ctx.AgentID)
	workspaceID := strings.TrimSpace(ctx.WorkspaceID)
	if channel == "" || conversationID == "" || agentID == "" || workspaceID == "" {
		return ""
	}

	cronFlags := fmt.Sprintf("--channel %q --conversation-id %q --agent-id %q --workspace-id %q", channel, conversationID, agentID, workspaceID)
	parts := []string{
		"Runtime API addresses and absolute workspace paths stay in Lumi-managed environment variables. Do not print them or copy them into command arguments.",
		"For commands scoped to this Session, pass only these stable routing flags. Use them only for Lumi CLI routing and never repeat them in chat:",
		"  " + cronFlags,
		"For example:",
		fmt.Sprintf("  \"$LUMI_CLI\" cron add %s --cron \"<min> <hour> <day> <month> <weekday>\" --prompt \"<task description>\" --desc \"<short label>\"", cronFlags),
	}
	if channel == ChannelWeCom {
		imFlags := fmt.Sprintf("--channel %q --conversation-id %q --workspace-id %q", channel, conversationID, workspaceID)
		parts = append(parts,
			"For intermediate IM files, use the same stable conversation handle:",
			fmt.Sprintf("  \"$LUMI_CLI\" im run %s --image-out IMAGE_PATH --sh '<command that writes \"$IMAGE_PATH\">'", imFlags),
		)
	}
	if channel == ChannelWeCom || channel == ChannelWeChat {
		parts = append(parts, "The Lumi server resolves the current IM reply target. Do not request, persist, or pass WeChat context tokens, WeCom request IDs, chat IDs, or user IDs.")
	}
	return strings.Join(parts, "\n\n")
}

func WithAgentToolInstructionsForContext(prompt string, ctx ToolContext) string {
	prompt = strings.TrimSpace(prompt)
	instructions := AgentToolInstructionsForContext(ctx)
	if prompt == "" {
		return instructions
	}
	return instructions + "\n\nUser: " + prompt
}
