package cron

import (
	"fmt"
	"strings"
)

const AgentToolInstructions = `You are running inside Lumi.

When the user asks you to do something on a schedule, use Bash:

  lumi-cli cron add --cron "<min> <hour> <day> <month> <weekday>" --prompt "<task description>" --desc "<short label>"

If LUMI_CLI is set, use "$LUMI_CLI" instead of "lumi-cli":

  "$LUMI_CLI" cron add --cron "<min> <hour> <day> <month> <weekday>" --prompt "<task description>" --desc "<short label>"

Environment variables are already set:

  LUMI_API_BASE
  LUMI_CHANNEL
  LUMI_CONVERSATION_ID
  LUMI_AGENT_ID
  LUMI_WORKSPACE_ID
  LUMI_WORKSPACE_PATH
  LUMI_CLI

Examples:

  lumi-cli cron add --cron "0 8 * * *" --prompt "检查项目状态并总结" --desc "每日项目状态"
  lumi-cli cron add --cron "0 9 * * 1" --prompt "生成本周项目进展报告" --desc "每周项目报告"
  lumi-cli cron add --cron "*/30 * * * *" --exec "df -h" --session-mode new-per-run --timeout-mins 5 --desc "磁盘空间检查"
  lumi-cli cron list
  lumi-cli cron info <job-id>
  lumi-cli cron edit <job-id> cronExpr "0 10 * * *"
  lumi-cli cron edit <job-id> enabled false
  lumi-cli cron edit <job-id> enabled true
  lumi-cli cron edit <job-id> mute true
  lumi-cli cron edit <job-id> mute false
  lumi-cli cron edit <job-id> silent true
  lumi-cli cron edit <job-id> timeoutMins 60
  lumi-cli cron del <job-id>

Pause or stop a scheduled task by setting enabled false. Resume it with enabled true.
Mute means the task still runs but sends no start or result messages. Silent only suppresses the start notification.

Do not output internal scheduling protocols. Use the CLI for scheduling control.`

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

func WithAgentToolInstructionsForContext(prompt string, ctx ToolContext) string {
	prompt = strings.TrimSpace(prompt)
	instructions := AgentToolInstructions
	if ctx.APIBase != "" || ctx.Channel != "" || ctx.ConversationID != "" || ctx.AgentID != "" || ctx.WorkspaceID != "" || ctx.WorkspacePath != "" {
		instructions += fmt.Sprintf(`

Current Lumi context:

  LUMI_API_BASE=%s
  LUMI_CHANNEL=%s
  LUMI_CONVERSATION_ID=%s
  LUMI_AGENT_ID=%s
  LUMI_WORKSPACE_ID=%s
  LUMI_WORKSPACE_PATH=%s`, ctx.APIBase, ctx.Channel, ctx.ConversationID, ctx.AgentID, ctx.WorkspaceID, ctx.WorkspacePath)
		if ctx.Target.WeChat != nil {
			instructions += fmt.Sprintf(`
  LUMI_WECHAT_CONVERSATION_KEY=%s
  LUMI_WECHAT_CONTEXT_TOKEN=%s`, ctx.Target.WeChat.ConversationKey, ctx.Target.WeChat.ContextToken)
		}
		if ctx.Target.WeCom != nil {
			instructions += fmt.Sprintf(`
  LUMI_WECOM_REQ_ID=%s
  LUMI_WECOM_CHAT_ID=%s
  LUMI_WECOM_CHAT_TYPE=%s
  LUMI_WECOM_USER_ID=%s`, ctx.Target.WeCom.ReqID, ctx.Target.WeCom.ChatID, ctx.Target.WeCom.ChatType, ctx.Target.WeCom.UserID)
		}
		instructions += `

If your shell does not have these environment variables, pass the same values with CLI flags such as --channel, --conversation-id, --agent-id, and --workspace-id.`
		command := `lumi-cli`
		if ctx.APIBase != "" || ctx.Channel != "" || ctx.ConversationID != "" || ctx.AgentID != "" || ctx.WorkspaceID != "" {
			command = fmt.Sprintf(`lumi-cli cron add --api-base %q --channel %q --conversation-id %q --agent-id %q --workspace-id %q --cron "<min> <hour> <day> <month> <weekday>" --prompt "<task description>" --desc "<short label>"`,
				ctx.APIBase, ctx.Channel, ctx.ConversationID, ctx.AgentID, ctx.WorkspaceID)
			if ctx.WorkspacePath != "" {
				command = strings.Replace(command, ` --cron `, fmt.Sprintf(` --work-dir %q --cron `, ctx.WorkspacePath), 1)
			}
			if ctx.Target.WeChat != nil {
				command = strings.Replace(command, ` --cron `, fmt.Sprintf(` --wechat-conversation-key %q --wechat-context-token %q --cron `, ctx.Target.WeChat.ConversationKey, ctx.Target.WeChat.ContextToken), 1)
			}
			if ctx.Target.WeCom != nil {
				command = strings.Replace(command, ` --cron `, fmt.Sprintf(` --wecom-req-id %q --wecom-chat-id %q --wecom-chat-type %q --wecom-user-id %q --cron `, ctx.Target.WeCom.ReqID, ctx.Target.WeCom.ChatID, ctx.Target.WeCom.ChatType, ctx.Target.WeCom.UserID), 1)
			}
		}
		instructions += fmt.Sprintf(`

For this conversation, prefer this explicit form:

  %s`, command)
	}
	if prompt == "" {
		return instructions
	}
	return instructions + "\n\nUser: " + prompt
}
