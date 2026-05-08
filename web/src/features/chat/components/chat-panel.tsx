'use client'

import { useEffect, useMemo, useRef } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

import { SandboxWorkspaceAlert } from '@/components/sandbox-workspace-alert'
import { ChatComposer } from '@/features/chat/components/chat-composer'
import { ChatMessage } from '@/features/chat/components/chat-message'
import { PermissionRequestCard } from '@/features/chat/components/permission-request-card'
import { ThinkingBlock } from '@/features/chat/components/thinking-block'
import { ToolSteps } from '@/features/chat/components/tool-steps'
import { isWorkspaceInteractionBlocked } from '@/lib/sandbox'
import type {
  Agent,
  Message,
  MessageFile,
  PermissionRequest,
  SlashCommand,
  StreamItem,
  ToolCall,
  Workspace,
} from '@/lib/types'
import { useI18n } from '@/features/i18n/i18n-provider'

type ConversationViewItem =
  | { type: 'message'; message: Message; hideAgentTag: boolean; key: string }
  | { type: 'tool_steps'; tools: ToolCall[]; agent?: string; hideAgentTag: boolean; key: string }

type StreamViewItem =
  | { type: 'text'; data: string; hideAgentTag: boolean; key: string }
  | { type: 'thinking'; data: StreamItem & { type: 'thinking' }; hideAgentTag: boolean; key: string }
  | { type: 'tool_steps'; tools: ToolCall[]; hideAgentTag: boolean; key: string }

function buildVisibleItems(messages: Message[]) {
  const result: ConversationViewItem[] = []
  let lastAgent: string | null = null
  let lastRole: string | null = null

  const nextAssistantHide = (agent?: string) => {
    const hideAgentTag = lastRole === 'assistant' && lastAgent === agent
    lastRole = 'assistant'
    lastAgent = agent || null
    return hideAgentTag
  }

  messages.forEach((message) => {
    const isVisible =
      Boolean(message.content) || Boolean(message.toolCall) || Boolean(message.isError) || message.role !== 'assistant'
    if (!isVisible) return

    if (message.toolCall) {
      const previous = result[result.length - 1]
      if (previous?.type === 'tool_steps' && previous.agent === message.agent) {
        previous.tools.push(message.toolCall)
        return
      }

      result.push({
        type: 'tool_steps',
        agent: message.agent,
        hideAgentTag: nextAssistantHide(message.agent),
        key: `tool-steps-${result.length}-${message.toolCall.toolCallId}`,
        tools: [message.toolCall],
      })
      return
    }

    if (message.role !== 'assistant') {
      lastRole = message.role
      lastAgent = null
      result.push({
        type: 'message',
        hideAgentTag: false,
        key: `message-${result.length}-${message.content}`,
        message,
      })
      return
    }

    result.push({
      type: 'message',
      hideAgentTag: nextAssistantHide(message.agent),
      key: `message-${result.length}-${message.content}`,
      message,
    })
  })

  return result
}

function buildStreamViewItems(items: StreamItem[], streamStartsNewAgent: boolean) {
  const result: StreamViewItem[] = []

  items.forEach((item) => {
    const hideAgentTag = result.length > 0 || !streamStartsNewAgent

    if (item.type === 'tool') {
      const previous = result[result.length - 1]
      if (previous?.type === 'tool_steps') {
        previous.tools.push(item.data)
        return
      }

      result.push({
        type: 'tool_steps',
        hideAgentTag,
        key: `stream-tool-steps-${result.length}-${item.data.toolCallId}`,
        tools: [item.data],
      })
      return
    }

    if (item.type === 'thinking') {
      result.push({
        type: 'thinking',
        data: item,
        hideAgentTag,
        key: `stream-thinking-${result.length}`,
      })
      return
    }

    result.push({
      type: 'text',
      data: item.data,
      hideAgentTag,
      key: `stream-text-${result.length}`,
    })
  })

  return result
}

function getLastVisibleAssistant(items: ConversationViewItem[]) {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index]
    if (!item) continue
    if (item.type === 'tool_steps') return { agent: item.agent }
    if (item.message.role === 'assistant') return item.message
    return null
  }
  return null
}

function AgentTag({ agent, hidden }: { agent?: string; hidden?: boolean }) {
  if (!agent || hidden) return null

  return (
    <div className="mb-1 inline-block text-[10px] font-bold uppercase tracking-[0.1em] text-muted-foreground">
      {agent}
    </div>
  )
}

export function ChatPanel({
  agents,
  commands,
  currentAgent,
  currentMessages,
  currentSessionId,
  currentWorkspace,
  currentWorkspaceInfo,
  readonly = false,
  shareToken,
  isSending,
  pendingPermission,
  streamItems,
  onCancel,
  onConfirmPermission,
  onRetryWorkspaceAccess,
  onSend,
  onWorkspaceFilesChanged,
}: {
  agents: Agent[]
  commands: SlashCommand[]
  currentAgent: string
  currentMessages: Message[]
  currentSessionId: string | null
  currentWorkspace: string
  currentWorkspaceInfo?: Workspace | null
  readonly?: boolean
  shareToken?: string
  isSending: boolean
  pendingPermission: PermissionRequest | null
  streamItems: StreamItem[]
  onCancel: () => Promise<boolean>
  onConfirmPermission: () => void
  onRetryWorkspaceAccess?: () => void
  onSend: (message: string, files: MessageFile[]) => Promise<void>
  onWorkspaceFilesChanged?: () => void
}) {
  const { t } = useI18n()
  const viewportRef = useRef<HTMLDivElement | null>(null)
  const displayMessages = useMemo(
    () => (readonly ? currentMessages.filter((message) => message.type !== 'thinking') : currentMessages),
    [currentMessages, readonly]
  )
  const visibleItems = useMemo(() => buildVisibleItems(displayMessages), [displayMessages])
  const visibleStreamItems = useMemo(
    () => (readonly ? streamItems.filter((item) => item.type !== 'thinking') : streamItems),
    [readonly, streamItems]
  )
  const isWorkspaceBlocked = isWorkspaceInteractionBlocked(currentWorkspaceInfo)
  const lastVisibleAssistant = getLastVisibleAssistant(visibleItems)
  const streamStartsNewAgent =
    Boolean(visibleStreamItems.length) &&
    (!lastVisibleAssistant || lastVisibleAssistant.agent !== currentAgent)
  const streamViewItems = useMemo(
    () => buildStreamViewItems(visibleStreamItems, streamStartsNewAgent),
    [streamStartsNewAgent, visibleStreamItems]
  )

  useEffect(() => {
    const viewport = viewportRef.current
    if (!viewport) return
    viewport.scrollTop = viewport.scrollHeight
  }, [currentMessages, pendingPermission, streamItems])

  return (
    <div className="flex h-full flex-col">
      <div className="legacy-hidden-scrollbar flex-1 overflow-y-auto" ref={viewportRef}>
        <div className="mx-auto flex w-full max-w-[800px] flex-col gap-0 px-5 pb-5 pt-10">
          {!currentSessionId || displayMessages.length === 0 ? (
            <div className="px-5 py-20 text-center text-sm text-muted-foreground">
              {!currentWorkspace ? (
                <p>{t('welcome.select_workspace')}</p>
              ) : isWorkspaceBlocked ? (
                <div className="mx-auto max-w-[720px]">
                  <SandboxWorkspaceAlert
                    className="text-left"
                    compact={false}
                    onRetry={onRetryWorkspaceAccess}
                    workspace={currentWorkspaceInfo}
                  />
                </div>
              ) : (
                <div className="space-y-3">
                  <p className="text-sm text-foreground">{t('welcome.start')}</p>
                  <p>
                    {t('welcome.mention')}:{' '}
                    {agents.map((agent) => (
                      <code className="mr-2" key={agent.id}>
                        @{agent.id}
                      </code>
                    ))}
                  </p>
                </div>
              )}
            </div>
          ) : null}

          {visibleItems.map((item) =>
            item.type === 'tool_steps' ? (
              <div className="mb-1" key={item.key}>
                <AgentTag agent={item.agent} hidden={item.hideAgentTag} />
                <ToolSteps tools={item.tools} />
              </div>
            ) : (
              <ChatMessage
                currentWorkspace={readonly ? undefined : currentWorkspace}
                hideAgentTag={item.hideAgentTag}
                key={item.key}
                message={item.message}
                shareToken={shareToken}
              />
            )
          )}

          {streamViewItems.map((item) => {
            if (item.type === 'tool_steps') {
              return (
                <div className="mb-1" key={item.key}>
                  <AgentTag agent={currentAgent} hidden={item.hideAgentTag} />
                  <ToolSteps tools={item.tools} />
                </div>
              )
            }

            if (item.type === 'thinking') {
              return (
                <div className="mb-1" key={item.key}>
                  <AgentTag agent={currentAgent} hidden={item.hideAgentTag} />
                  <ThinkingBlock thinking={item.data.data} />
                </div>
              )
            }

            return (
              <div className="mb-1" key={item.key}>
                <AgentTag agent={currentAgent} hidden={item.hideAgentTag} />
                <div className="markdown">
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>{item.data}</ReactMarkdown>
                </div>
              </div>
            )
          })}

          {pendingPermission && !readonly ? (
            <PermissionRequestCard
              agentId={currentAgent}
              onConfirmed={onConfirmPermission}
              request={pendingPermission}
            />
          ) : null}

          {isSending && !pendingPermission && !readonly ? (
            <div className="flex justify-start py-4">
              <div className="legacy-loading-dots" aria-label="Loading">
                <span />
                <span />
                <span />
              </div>
            </div>
          ) : null}
        </div>
      </div>

      {!readonly ? (
        <div className="w-full px-10 pb-10">
          <div className="mx-auto w-full max-w-[900px]">
            {isWorkspaceBlocked ? (
              <SandboxWorkspaceAlert
                className="mb-4"
                compact
                onRetry={onRetryWorkspaceAccess}
                workspace={currentWorkspaceInfo}
              />
            ) : null}
            <ChatComposer
              agents={agents}
              commands={commands}
              currentAgent={currentAgent}
              currentWorkspace={currentWorkspace}
              disabled={isSending || !currentWorkspace || isWorkspaceBlocked}
              isSending={isSending}
              onCancel={() => {
                void onCancel()
              }}
              onSend={onSend}
              onWorkspaceFilesChanged={onWorkspaceFilesChanged}
            />
          </div>
        </div>
      ) : null}
    </div>
  )
}
