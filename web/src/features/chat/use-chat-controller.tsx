'use client'

import { useCallback, useEffect, useRef, useState } from 'react'

import * as api from '@/lib/api'
import { getReadableSandboxErrorMessage, isWorkspaceInteractionBlocked } from '@/lib/sandbox'
import type {
  Agent,
  CronJob,
  Message,
  MessageFile,
  PermissionRequest,
  Session,
  SessionMeta,
  SessionUpdate,
  SlashCommand,
  StreamEvent,
  StreamItem,
  ThinkingData,
  ToolCall,
  Workspace,
} from '@/lib/types'

interface UseChatControllerOptions {
  routeSessionId: string | null | undefined
  pushRoute: (sessionId: string | null) => void
}

const WORKSPACE_TREE_REFRESH_WINDOW_MS = 1500
const SKILL_COMMAND_REFRESH_MS = 3000
const thinkBlockPattern = /<\s*think(?:ing)?\s*>[\s\S]*?<\s*\/\s*think(?:ing)?\s*>/gi
const orphanThinkClosePrefixPattern = /^[\s\S]*?<\s*\/\s*think(?:ing)?\s*>/i
const thinkTagPattern = /<\s*\/?\s*think(?:ing)?\s*>/gi

function stripThinkTags(content: string) {
  return content
    .replace(thinkBlockPattern, '')
    .replace(orphanThinkClosePrefixPattern, '')
    .replace(thinkTagPattern, '')
    .replace(/\n{3,}/g, '\n\n')
    .replace(/^\s*\n/, '')
}

function mergeSlashCommands(baseCommands: SlashCommand[], skillCommands: SlashCommand[]) {
  const merged = new Map<string, SlashCommand>()
  const skillAliases = new Map<string, string>()

  for (const command of skillCommands) {
    for (const alias of command.aliases || []) {
      skillAliases.set(normalizeSlashCommandName(alias), command.name)
    }
  }

  for (const command of baseCommands) {
    if (!skillAliases.has(normalizeSlashCommandName(command.name))) {
      merged.set(command.name, command)
    }
  }
  for (const command of skillCommands) {
    if (!merged.has(command.name)) {
      merged.set(command.name, command)
    }
  }

  return Array.from(merged.values())
}

function normalizeSlashCommandName(value: string) {
  return value.trim().toLowerCase().replaceAll('-', '_')
}

function toToolStatus(update: SessionUpdate): ToolCall['status'] {
  if (update.error || update._meta?.claudeCode?.error) return 'error'
  if (update.status === 'completed') return 'completed'
  return 'pending'
}

function normalizeToolInput(rawInput?: Record<string, unknown>) {
  if (!rawInput) return ''
  if (rawInput.command) return String(rawInput.command)
  if (rawInput.file_path) return String(rawInput.file_path)
  if (rawInput.pattern) return String(rawInput.pattern)
  if (rawInput.old_string) return `old_string: ${String(rawInput.old_string).slice(0, 100)}...`
  return JSON.stringify(rawInput, null, 2)
}

function normalizeWorkspace(workspace: Workspace): Workspace {
  const kind = workspace.kind || 'local'
  if (kind === 'sandbox') {
    return {
      ...workspace,
      kind: 'sandbox',
      sandboxReady:
        workspace.sandboxReady ??
        workspace.sandboxStatus === 'running',
    }
  }

  if (kind === 'remote') {
    const remotePath = workspace.remotePath || workspace.path
    return {
      ...workspace,
      kind: 'remote',
      remotePath,
      path: remotePath,
      setupReady: workspace.setupReady ?? false,
    }
  }

  return {
    ...workspace,
    kind: 'local',
  }
}

export function useChatController({ routeSessionId, pushRoute }: UseChatControllerOptions) {
  const [sessions, setSessions] = useState<SessionMeta[]>([])
  const [cronJobs, setCronJobs] = useState<CronJob[]>([])
  const [sessionDetails, setSessionDetails] = useState<Record<string, Session>>({})
  const [agents, setAgents] = useState<Agent[]>([])
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [commandsByAgent, setCommandsByAgent] = useState<Record<string, SlashCommand[]>>({})
  const [skillCommandsByWorkspaceAgent, setSkillCommandsByWorkspaceAgent] = useState<Record<string, SlashCommand[]>>({})
  const [defaultAgent, setDefaultAgent] = useState('claude')
  const [defaultWorkspace, setDefaultWorkspace] = useState('')
  const [currentSessionId, setCurrentSessionId] = useState<string | null>(null)
  const [currentAgent, setCurrentAgent] = useState('claude')
  const [currentWorkspace, setCurrentWorkspace] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [isSending, setIsSending] = useState(false)
  const [agentSessionId, setAgentSessionId] = useState<string | null>(null)
  const [sendingSessionId, setSendingSessionId] = useState<string | null>(null)
  const [streamItemsBySession, setStreamItemsBySession] = useState<Record<string, StreamItem[]>>({})
  const [pendingStreamAgentBySession, setPendingStreamAgentBySession] = useState<Record<string, string>>({})
  const [pendingPermission, setPendingPermission] = useState<PermissionRequest | null>(null)
  const [workspaceTreeRefreshToken, setWorkspaceTreeRefreshToken] = useState(0)
  const [initialized, setInitialized] = useState(false)
  const initializedRef = useRef(false)
  const cronEventsSubscribedRef = useRef(false)
  const lastPushedRouteRef = useRef<string | null | undefined>(undefined)
  const sessionDetailsRef = useRef(sessionDetails)
  const currentSessionIdRef = useRef<string | null>(null)
  const currentWorkspaceRef = useRef('')
  const currentAgentRef = useRef('claude')
  const sendingSessionIdRef = useRef<string | null>(null)
  const pendingStreamAgentRef = useRef<Record<string, string>>({})
  const streamItemsRef = useRef<Record<string, StreamItem[]>>({})
  const workspacesRef = useRef<Workspace[]>([])
  const workspaceTreeRefreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const pendingWorkspaceTreeRefreshRef = useRef<string | null>(null)
  const skillRefreshRef = useRef({ key: '', timestamp: 0 })

  useEffect(() => {
    sessionDetailsRef.current = sessionDetails
  }, [sessionDetails])

  useEffect(() => {
    currentSessionIdRef.current = currentSessionId
  }, [currentSessionId])

  useEffect(() => {
    currentWorkspaceRef.current = currentWorkspace
  }, [currentWorkspace])

  useEffect(() => {
    currentAgentRef.current = currentAgent
  }, [currentAgent])

  useEffect(() => {
    sendingSessionIdRef.current = sendingSessionId
  }, [sendingSessionId])

  useEffect(() => {
    pendingStreamAgentRef.current = pendingStreamAgentBySession
  }, [pendingStreamAgentBySession])

  useEffect(() => {
    streamItemsRef.current = streamItemsBySession
  }, [streamItemsBySession])

  useEffect(() => {
    workspacesRef.current = workspaces
  }, [workspaces])

  useEffect(() => {
    return () => {
      if (workspaceTreeRefreshTimerRef.current) {
        clearTimeout(workspaceTreeRefreshTimerRef.current)
      }
    }
  }, [])

  const currentSession = currentSessionId ? sessionDetails[currentSessionId] || null : null
  const currentWorkspaceInfo = workspaces.find((workspace) => workspace.id === currentWorkspace) || null
  const filteredSessions = sessions.filter(
    (session) => !currentWorkspace || session.workspaceId === currentWorkspace || !session.workspaceId
  )
  const streamItems = currentSessionId ? streamItemsBySession[currentSessionId] || [] : []
  const skillCommandKey = currentWorkspace && currentAgent ? `${currentWorkspace}:${currentAgent}` : ''
  const commands = mergeSlashCommands(
    commandsByAgent[currentAgent] || [],
    skillCommandKey ? skillCommandsByWorkspaceAgent[skillCommandKey] || [] : []
  )

  const scheduleWorkspaceTreeRefreshWindow = () => {
    if (workspaceTreeRefreshTimerRef.current) {
      clearTimeout(workspaceTreeRefreshTimerRef.current)
    }

    workspaceTreeRefreshTimerRef.current = setTimeout(() => {
      workspaceTreeRefreshTimerRef.current = null

      const pendingWorkspaceId = pendingWorkspaceTreeRefreshRef.current
      pendingWorkspaceTreeRefreshRef.current = null

      if (pendingWorkspaceId && pendingWorkspaceId === currentWorkspaceRef.current) {
        setWorkspaceTreeRefreshToken((current) => current + 1)
        scheduleWorkspaceTreeRefreshWindow()
      }
    }, WORKSPACE_TREE_REFRESH_WINDOW_MS)
  }

  const resolveWorkspaceIdForSession = (sessionId?: string | null) => {
    if (!sessionId) {
      return currentWorkspaceRef.current || null
    }

    return (
      sessionDetailsRef.current[sessionId]?.workspaceId ||
      (sessionId === sendingSessionIdRef.current || sessionId === currentSessionIdRef.current
        ? currentWorkspaceRef.current || null
        : null)
    )
  }

  const requestWorkspaceTreeRefresh = (options?: {
    immediate?: boolean
    sessionId?: string | null
    workspaceId?: string | null
  }) => {
    const targetWorkspaceId = options?.workspaceId ?? resolveWorkspaceIdForSession(options?.sessionId)
    if (!targetWorkspaceId || targetWorkspaceId !== currentWorkspaceRef.current) {
      return
    }

    if (options?.immediate || !workspaceTreeRefreshTimerRef.current) {
      pendingWorkspaceTreeRefreshRef.current = null
      setWorkspaceTreeRefreshToken((current) => current + 1)
      scheduleWorkspaceTreeRefreshWindow()
      return
    }

    pendingWorkspaceTreeRefreshRef.current = targetWorkspaceId
  }

  const upsertSessionDetail = (session: Session) => {
    setSessionDetails((current) => ({
      ...current,
      [session.id]: session,
    }))
  }

  const updateSessionMessages = (sessionId: string, updater: (messages: Message[]) => Message[]) => {
    setSessionDetails((current) => {
      const session = current[sessionId]
      if (!session) return current

      return {
        ...current,
        [sessionId]: {
          ...session,
          messages: updater(session.messages),
          updatedAt: Date.now(),
        },
      }
    })
  }

  const mergeSessionDetail = (session: Session) => {
    setSessionDetails((current) => {
      const existing = current[session.id]
      if (!existing) {
        return {
          ...current,
          [session.id]: session,
        }
      }

      const existingMessages = existing.messages || []
      const nextMessages = session.messages || []
      if (existingMessages.length > nextMessages.length) {
        return {
          ...current,
          [session.id]: {
            ...session,
            messages: existingMessages,
          },
        }
      }

      return {
        ...current,
        [session.id]: session,
      }
    })
  }

  const upsertToolCall = (tool: ToolCall, targetSessionId?: string | null) => {
    const sessionId = targetSessionId || sendingSessionIdRef.current || currentSessionIdRef.current
    if (!sessionId) return

    setStreamItemsBySession((current) => {
      const existing = current[sessionId] || []
      const nextItems = [...existing]
      const existingIndex = nextItems.findIndex(
        (item) => item.type === 'tool' && item.data.toolCallId === tool.toolCallId
      )

      if (existingIndex >= 0) {
        const item = nextItems[existingIndex]
        if (item?.type === 'tool') {
          const merged: ToolCall = {
            ...item.data,
            ...tool,
            title:
              tool.title && !tool.title.startsWith('toolu_')
                ? tool.title
                : item.data.title,
            description:
              tool.status === 'completed' && item.data.description
                ? item.data.description
                : tool.description || item.data.description,
            input: tool.input || item.data.input,
            rawInput: tool.rawInput || item.data.rawInput,
            output: tool.output || item.data.output,
            error: tool.error || item.data.error,
          }

          nextItems[existingIndex] = { type: 'tool', data: merged }
        }
      } else {
        nextItems.push({ type: 'tool', data: tool })
      }

      return {
        ...current,
        [sessionId]: nextItems,
      }
    })
  }

  const addStreamingText = (text: string, targetSessionId?: string | null) => {
    const sessionId = targetSessionId || sendingSessionIdRef.current || currentSessionIdRef.current
    if (!sessionId) return

    setStreamItemsBySession((current) => {
      const existing = current[sessionId] || []
      const lastItem = existing[existing.length - 1]

      if (lastItem?.type === 'text') {
        return {
          ...current,
          [sessionId]: [
            ...existing.slice(0, -1),
            { type: 'text', data: `${lastItem.data}${text}` },
          ],
        }
      }

      return {
        ...current,
        [sessionId]: [...existing, { type: 'text', data: text }],
      }
    })
  }

  const upsertThinking = (thinking: ThinkingData, targetSessionId?: string | null) => {
    const sessionId = targetSessionId || sendingSessionIdRef.current || currentSessionIdRef.current
    if (!sessionId) return

    setStreamItemsBySession((current) => {
      const existing = current[sessionId] || []
      const nextItems = [...existing]
      const lastItem = nextItems[nextItems.length - 1]

      if (lastItem?.type === 'thinking') {
        nextItems[nextItems.length - 1] = { type: 'thinking', data: thinking }
      } else {
        nextItems.push({ type: 'thinking', data: thinking })
      }

      return {
        ...current,
        [sessionId]: nextItems,
      }
    })
  }

  const clearStreamItems = (sessionId?: string | null) => {
    const targetSessionId = sessionId || currentSessionIdRef.current
    if (!targetSessionId) return

    setStreamItemsBySession((current) => ({
      ...current,
      [targetSessionId]: [],
    }))
  }

  const finalizeStreamItems = (agent: string, sessionId?: string | null) => {
    const targetSessionId = sessionId || sendingSessionIdRef.current || currentSessionIdRef.current
    if (!targetSessionId) return

    setPendingStreamAgentBySession((current) => ({
      ...current,
      [targetSessionId]: agent,
    }))
  }

  const commitStreamItems = (sessionId?: string | null) => {
    const targetSessionId = sessionId || currentSessionIdRef.current
    if (!targetSessionId) return

    const items = streamItemsRef.current[targetSessionId] || []
    if (items.length === 0) return

    const agent = pendingStreamAgentRef.current[targetSessionId] || currentAgentRef.current || defaultAgent

    updateSessionMessages(targetSessionId, (messages) => {
      const committedMessages = items.map<Message>((item) => {
        if (item.type === 'text') {
          return {
            role: 'assistant',
            content: stripThinkTags(item.data),
            agent,
          }
        }

        if (item.type === 'thinking') {
          return {
            role: 'assistant',
            type: 'thinking',
            content: item.data.content,
            status: item.data.status === 'thinking' ? 'done' : item.data.status,
            duration: item.data.duration,
            agent,
          }
        }

        return {
          role: 'assistant',
          content: '',
          agent,
          toolCall: item.data,
        }
      })

      return [...messages, ...committedMessages]
        .filter((message) => message.type === 'thinking' || message.toolCall || message.content)
    })

    setStreamItemsBySession((current) => ({
      ...current,
      [targetSessionId]: [],
    }))

    setPendingStreamAgentBySession((current) => {
      const next = { ...current }
      delete next[targetSessionId]
      return next
    })
  }

  const setCommands = (agentId: string, nextCommands: SlashCommand[]) => {
    setCommandsByAgent((current) => ({
      ...current,
      [agentId]: nextCommands,
    }))
  }

  const refreshSkillCommands = useCallback(async (options?: { force?: boolean }) => {
    const workspaceId = currentWorkspaceRef.current
    const agentId = currentAgentRef.current
    if (!workspaceId || !agentId) return

    const key = `${workspaceId}:${agentId}`
    const now = Date.now()
    if (
      !options?.force &&
      skillRefreshRef.current.key === key &&
      now - skillRefreshRef.current.timestamp < SKILL_COMMAND_REFRESH_MS
    ) {
      return
    }

    skillRefreshRef.current = { key, timestamp: now }

    try {
      const data = await api.fetchSkills()
      const agent = agents.find((item) => item.id === agentId)
      const project = data.projects.find((item) => {
        if (item.project !== workspaceId) return false
        if (!agent?.backend) return true
        return item.agentType === agent.backend
      })

      setSkillCommandsByWorkspaceAgent((current) => ({
        ...current,
        [key]: (project?.skills || []).map((skill) => ({
          name: skill.name,
          aliases: skill.aliases || [],
          description: skill.description || skill.displayName || 'Run local skill',
          input: { hint: 'arguments' },
          isSkill: true,
        })),
      }))
    } catch (error) {
      console.warn('Failed to load skills', error)
      setSkillCommandsByWorkspaceAgent((current) => ({
        ...current,
        [key]: [],
      }))
    }
  }, [agents])

  const loadWorkspaces = useCallback(async () => {
    const data = await api.fetchWorkspaces()
    const nextWorkspaces = data.workspaces.map(normalizeWorkspace)
    setWorkspaces(nextWorkspaces)
    setDefaultWorkspace(data.default || '')
    setCurrentWorkspace((current) => {
      if (current && nextWorkspaces.some((workspace) => workspace.id === current)) return current
      if (data.default) return data.default
      return nextWorkspaces[0]?.id || ''
    })
  }, [])

  const loadAgents = async () => {
    const data = await api.fetchAgents()
    setAgents(data.agents)
    setDefaultAgent(data.default)
    setCurrentAgent((current) => current || data.default)
    setCommandsByAgent((current) => {
      const next = { ...current }
      data.agents.forEach((agent) => {
        if (agent.commands?.length) {
          next[agent.id] = agent.commands
        }
      })
      return next
    })
  }

  const selectSession = async (sessionId: string, syncRoute = true) => {
    setIsLoading(true)
    try {
      let session: Session | null = sessionDetailsRef.current[sessionId] || null
      if (!session) {
        session = await api.fetchSession(sessionId)
        if (!session) return
        upsertSessionDetail(session)
      }

	      setCurrentSessionId(session.id)
	      setCurrentAgent(session.activeAgent)
	      setPendingPermission(session.pendingPermission || null)
	      if (session.workspaceId) {
	        setCurrentWorkspace(session.workspaceId)
	      }

      if (syncRoute) {
        lastPushedRouteRef.current = session.id
        pushRoute(session.id)
      }
    } finally {
      setIsLoading(false)
    }
  }

  const loadSessions = async (skipAutoSelect = false) => {
    setIsLoading(true)
    try {
      const nextSessions = await api.fetchSessions()
      setSessions(nextSessions)

      if (!skipAutoSelect && !currentSessionIdRef.current && nextSessions[0]) {
        await selectSession(nextSessions[0].id)
      }
    } finally {
      setIsLoading(false)
    }
  }

  const loadCronJobs = async () => {
    try {
      const jobs = await api.fetchCronJobs()
      setCronJobs(jobs)
    } catch (error) {
      console.warn('Failed to load scheduled tasks', error)
      setCronJobs([])
    }
  }

  const initialize = async (nextRouteSessionId: string | null) => {
    if (initializedRef.current) return

    initializedRef.current = true
    await Promise.all([loadAgents(), loadWorkspaces(), loadCronJobs()])
    setInitialized(true)
    await loadSessions(Boolean(nextRouteSessionId))

	if (nextRouteSessionId) {
	  await selectSession(nextRouteSessionId, false)
	}
  }

  const syncRoute = async (nextRouteSessionId: string | null) => {
    await initialize(nextRouteSessionId)

    if (lastPushedRouteRef.current === nextRouteSessionId) {
      lastPushedRouteRef.current = undefined
      return
    }

    if (!nextRouteSessionId) {
      return
    }

    if (nextRouteSessionId !== currentSessionIdRef.current) {
      await selectSession(nextRouteSessionId, false)
    }
  }

  useEffect(() => {
    if (routeSessionId === undefined) {
      return
    }

    void syncRoute(routeSessionId)
  }, [routeSessionId])

  useEffect(() => {
    if (!isWorkspaceInteractionBlocked(currentWorkspaceInfo)) {
      return
    }

    const intervalId = window.setInterval(() => {
      void loadWorkspaces()
    }, 2500)

    return () => window.clearInterval(intervalId)
  }, [currentWorkspaceInfo, loadWorkspaces])

  useEffect(() => {
    if (!initialized || !currentWorkspace || !currentAgent) return

    void refreshSkillCommands()
  }, [currentAgent, currentWorkspace, initialized, refreshSkillCommands])

  const createNewSession = async () => {
    setIsLoading(true)

    try {
      const meta = await api.createSession(currentWorkspaceRef.current || undefined)
      const session: Session = {
        id: meta.id,
        title: meta.title,
        activeAgent: meta.activeAgent,
        workspaceId: meta.workspaceId,
        createdAt: meta.createdAt,
        updatedAt: meta.updatedAt,
        messages: [],
      }

      upsertSessionDetail(session)
      setSessions((current) => [meta, ...current.filter((item) => item.id !== meta.id)])
      setCurrentSessionId(meta.id)
      setCurrentAgent(meta.activeAgent)
      lastPushedRouteRef.current = meta.id
      pushRoute(meta.id)

      return meta.id
    } finally {
      setIsLoading(false)
    }
  }

  const removeSession = async (sessionId: string) => {
    await api.deleteSession(sessionId)
    setSessions((current) => current.filter((session) => session.id !== sessionId))
    setSessionDetails((current) => {
      const next = { ...current }
      delete next[sessionId]
      return next
    })
    setStreamItemsBySession((current) => {
      const next = { ...current }
      delete next[sessionId]
      return next
    })
    setPendingStreamAgentBySession((current) => {
      const next = { ...current }
      delete next[sessionId]
      return next
    })

    if (currentSessionIdRef.current === sessionId) {
      setCurrentSessionId(null)
      lastPushedRouteRef.current = null
      pushRoute(null)
    }
  }

  const addWorkspace = async (
    name: string,
    path: string,
    options?: api.CreateWorkspaceOptions,
  ) => {
    const result = await api.createWorkspace(name, path, options)
    const workspace = result.workspace
    if (result.error || !workspace) {
      return result.error || 'Failed to create workspace'
    }

    const normalizedWorkspace = normalizeWorkspace(workspace)
    setWorkspaces((current) => [...current, normalizedWorkspace])
    setCurrentWorkspace(workspace.id)
    setCurrentSessionId(null)
    lastPushedRouteRef.current = null
    pushRoute(null)
    return null
  }

  const setWorkspace = (workspaceId: string) => {
    setCurrentWorkspace(workspaceId)
    setCurrentSessionId(null)
    setPendingPermission(null)
    lastPushedRouteRef.current = null
    pushRoute(null)
  }

  const handleToolUpdate = (update: SessionUpdate, targetSessionId?: string | null) => {
    const toolCallId = update.toolCallId
    if (!toolCallId) return

    const toolName = update._meta?.claudeCode?.toolName || update.kind || 'Tool'
    let output = ''
    let error = ''

    if (update._meta?.claudeCode?.toolResponse) {
      const response = update._meta.claudeCode.toolResponse
      if (response.stdout) {
        output = response.stdout
      } else if (response.stderr) {
        error = response.stderr
      } else if (response.type === 'text' && response.file?.content) {
        output = `File: ${response.file.filePath}\n${response.file.content.slice(0, 500)}${
          response.file.content.length > 500 ? '...' : ''
        }`
      }
    }

    if (update.error) {
      error = update.error
    } else if (update._meta?.claudeCode?.error) {
      error = update._meta.claudeCode.error
    }

    upsertToolCall(
      {
        toolCallId,
        toolName,
        title: update.title || toolCallId,
        status: toToolStatus(update),
        input: normalizeToolInput(update.rawInput),
        output,
        error,
      },
      targetSessionId
    )
  }

  const finishStreaming = async (targetSessionId?: string | null) => {
    finalizeStreamItems(currentAgentRef.current, targetSessionId)
    commitStreamItems(targetSessionId)
    setIsSending(false)
    setSendingSessionId(null)
    setPendingPermission((current) => {
      if (current && current.sessionId !== (targetSessionId || currentSessionIdRef.current)) {
        return current
      }
      return null
    })
    await Promise.all([loadSessions(true), loadCronJobs()])
  }

  const handleStreamEvent = (
    event: StreamEvent &
      Partial<ToolCall> & {
	        permission_request?: PermissionRequest
	        _eventType?: string
	        commands?: SlashCommand[]
	        agent?: string
	        agentId?: string
	        content?: string
        duration?: number
        options?: PermissionRequest['options']
        toolCall?: PermissionRequest['toolCall']
      },
    targetSessionId?: string | null
  ) => {
    const targetWorkspaceId = resolveWorkspaceIdForSession(targetSessionId)

    if (event._eventType === 'commands' && event.commands) {
      setCommands(event.agent || currentAgentRef.current, event.commands)
      return
    }

    if (event._eventType === 'tool_call' && event.toolCallId) {
      requestWorkspaceTreeRefresh({ sessionId: targetSessionId, workspaceId: targetWorkspaceId })
      upsertToolCall(
        {
          toolCallId: event.toolCallId,
          toolName: event.toolName || 'Tool',
          kind: event.kind || '',
          title: event.title || event.toolCallId,
          description: event.description || '',
          status: event.status || 'pending',
          input: event.input || '',
          rawInput: event.rawInput || '',
          output: event.output || '',
          error: event.error || '',
        },
        targetSessionId
      )
      return
    }

    if (event._eventType === 'thinking') {
      const content = typeof event.content === 'string' ? event.content : ''
      const rawStatus = (event as { status?: unknown }).status
      const status = rawStatus === 'done' ? 'done' : 'thinking'
      const duration = typeof event.duration === 'number' ? event.duration : undefined
      upsertThinking({ content, status, duration }, targetSessionId)
      return
    }

	    if (event.options && event.toolCall) {
	      const permissionSessionId = targetSessionId || currentSessionIdRef.current || ''
	      if (permissionSessionId) {
	        setPendingPermission({
	          sessionId: permissionSessionId,
	          conversationId: typeof event.conversationId === 'string' ? event.conversationId : permissionSessionId,
	          agentId: typeof event.agentId === 'string' ? event.agentId : event.agent,
	          options: event.options,
	          toolCall: event.toolCall,
	        })
	      }
      return
    }

    if (event.conversationId && event.agent) {
      setCurrentAgent(event.agent)
    }

    if (event.sessionId) {
      setAgentSessionId(event.sessionId)
    }

    if (event.message && !event.update && !event.error && !event.stopReason) {
      return
    }

    const update = event.update
    if (update?.sessionUpdate === 'agent_message_chunk') {
      if (update.content?.type === 'text' && update.content.text) {
        const visibleText = stripThinkTags(update.content.text)
        if (visibleText) {
          addStreamingText(visibleText, targetSessionId)
        }
      }
    } else if (update?.sessionUpdate === 'agent_thought_chunk') {
      if (update.content?.type === 'text' && update.content.text) {
        upsertThinking({ content: update.content.text, status: 'thinking' }, targetSessionId)
      }
    } else if (update?.sessionUpdate === 'tool_call' || update?.sessionUpdate === 'tool_call_update') {
      requestWorkspaceTreeRefresh({ sessionId: targetSessionId, workspaceId: targetWorkspaceId })
      handleToolUpdate(update, targetSessionId)
    }

    if (event.stopReason) {
      requestWorkspaceTreeRefresh({
        immediate: true,
        sessionId: targetSessionId,
        workspaceId: targetWorkspaceId,
      })
      void finishStreaming(targetSessionId)
    }

    if (event.error) {
      void finishStreaming(targetSessionId)
      const targetId = targetSessionId || currentSessionIdRef.current
      const message = getReadableSandboxErrorMessage(event.error)
      if (targetId) {
        updateSessionMessages(targetId, (messages) => [
          ...messages,
          { role: 'assistant', content: message, isError: true },
        ])
      }
    }
  }

  useEffect(() => {
    if (!initialized || cronEventsSubscribedRef.current) return
    cronEventsSubscribedRef.current = true

    const eventSource = new EventSource('/api/cron/events')

    const updateJob = (event: MessageEvent<string>) => {
      const payload = JSON.parse(event.data) as { channel?: string; conversationId?: string; job?: CronJob }
      if (!payload.job) return
      setCronJobs((current) => {
        const next = current.filter((job) => job.id !== payload.job!.id)
        return [payload.job!, ...next]
      })
    }

    const deleteJob = (event: MessageEvent<string>) => {
      const payload = JSON.parse(event.data) as { channel?: string; conversationId?: string; jobId?: string }
      if (!payload.jobId) return
      setCronJobs((current) => current.filter((job) => job.id !== payload.jobId))
    }

    const updateSession = (event: MessageEvent<string>) => {
      const payload = JSON.parse(event.data) as { conversationId?: string }
      void loadSessions(true)
      if (payload.conversationId && sessionDetailsRef.current[payload.conversationId]) {
        void api.fetchSession(payload.conversationId).then((session) => {
          if (session) mergeSessionDetail(session)
        })
      }
    }

    const chatEvent = (event: MessageEvent<string>) => {
      const payload = JSON.parse(event.data) as {
        conversationId?: string
        event?: string
        data?: Record<string, unknown>
      }
      if (!payload.conversationId || !payload.event || !payload.data) return
      if (payload.conversationId !== currentSessionIdRef.current && !sessionDetailsRef.current[payload.conversationId]) return

      if (payload.event === 'cron_trigger') {
        const message = payload.data.message as Message | undefined
        if (message) {
          if (sessionDetailsRef.current[payload.conversationId]) {
            updateSessionMessages(payload.conversationId, (messages) => [...messages, message])
          } else {
            void api.fetchSession(payload.conversationId).then((session) => {
              if (session) mergeSessionDetail(session)
            })
          }
        }
        void loadSessions(true)
        return
      }

      handleStreamEvent(
        {
          ...(payload.data as StreamEvent & Record<string, unknown>),
          _eventType: payload.event,
        },
        payload.conversationId,
      )
    }

    eventSource.addEventListener('job_created', updateJob as EventListener)
    eventSource.addEventListener('job_updated', updateJob as EventListener)
    eventSource.addEventListener('job_deleted', deleteJob as EventListener)
    eventSource.addEventListener('session_updated', updateSession as EventListener)
    eventSource.addEventListener('chat_event', chatEvent as EventListener)

    return () => {
      cronEventsSubscribedRef.current = false
      eventSource.close()
    }
  }, [initialized, routeSessionId])

  const sendCurrentMessage = async (message: string, files: MessageFile[] = []) => {
    setIsSending(true)
    commitStreamItems()
    clearStreamItems()
    setPendingPermission(null)

    let targetSessionId = currentSessionIdRef.current
    if (!targetSessionId) {
      targetSessionId = await createNewSession()
    }

    if (!targetSessionId) {
      setIsSending(false)
      return
    }

    setSendingSessionId(targetSessionId)
    updateSessionMessages(targetSessionId, (messages) => [
      ...messages,
      {
        role: 'user',
        content: message,
        files: files.length ? files : undefined,
      },
    ])

    const workspace =
      workspacesRef.current.find((item) => item.id === currentWorkspaceRef.current) || null
    const deviceId =
      workspace?.kind === 'remote' && workspace.deviceId ? workspace.deviceId : undefined

    api.sendMessage(
      message,
      targetSessionId,
      currentWorkspaceRef.current || null,
      files,
      (event) => {
        handleStreamEvent(event as StreamEvent & Record<string, unknown>, targetSessionId)
      },
      deviceId,
    )
  }

  const cancelCurrentChat = async () => {
    if (!agentSessionId || !currentAgentRef.current) return false

    const result = await api.cancelChat(currentAgentRef.current, agentSessionId)
    if (result.success) {
      await finishStreaming()
    }
    return result.success
  }

  const saveAgentMode = async (agentId: string, mode: string) => {
    const result = await api.updateAgentMode(agentId, mode)
    if (result.success) {
      setAgents((current) =>
        current.map((agent) =>
          agent.id === agentId
            ? {
                ...agent,
                sessionMode: mode,
              }
            : agent
        )
      )
    }
    return result
  }

  const saveAgentEnv = async (agentId: string, env: Record<string, string>) => {
    const result = await api.updateAgentEnv(agentId, env)
    if (result.success) {
      setAgents((current) =>
        current.map((agent) => (agent.id === agentId ? { ...agent, env } : agent))
      )
    }
    return result
  }

  return {
    agents,
    cronJobs,
    commands,
    currentAgent,
    currentSession,
    currentSessionId,
    currentWorkspace,
    currentWorkspaceInfo,
    workspaceTreeRefreshToken,
    defaultAgent,
    defaultWorkspace,
    filteredSessions,
    isLoading,
    isSending,
    pendingPermission,
    sessions,
    streamItems,
    workspaces,
    addWorkspace,
    cancelCurrentChat,
    clearStreamItems,
    commandsByAgent,
    commitStreamItems,
    createNewSession,
    currentMessages: currentSession?.messages || [],
    handlePermissionConfirmed: () => setPendingPermission(null),
    loadSessions,
    refreshCronJobs: loadCronJobs,
    refreshSkillCommands,
    removeSession,
    selectSession,
    sendCurrentMessage,
    requestWorkspaceTreeRefresh,
    setCommands,
    setCronJobs,
    setCurrentAgent,
    setWorkspace,
    setPendingPermission,
    setSessions,
    saveAgentEnv,
    saveAgentMode,
    refreshWorkspaces: loadWorkspaces,
  }
}
