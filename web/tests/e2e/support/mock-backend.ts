import type { Page, Request, Route } from '@playwright/test'

export interface MockSlashCommand {
  name: string
  description: string
  input?: {
    hint?: string
  } | null
}

export interface MockAgent {
  id: string
  name: string
  backend?: string
  permissionMode?: string
  sessionMode?: string
  command?: string
  args?: string[]
  commands?: MockSlashCommand[]
  env?: Record<string, string>
  availableModes?: Array<{
    value: string
    label: string
    description?: string
  }>
}

export interface MockWorkspace {
  id: string
  name: string
  path: string
  kind?: 'local' | 'remote' | 'sandbox'
  image?: string
  idleTimeoutSec?: number
  agents?: string[]
  deviceId?: string
  deviceName?: string
  remotePath?: string
  deviceStatus?: 'setup_required' | 'online' | 'offline' | 'busy' | 'error'
  setupReady?: boolean
  sandboxStatus?: 'pending' | 'running' | 'failed' | 'terminating' | 'terminated'
  sandboxStage?: 'checking_docker' | 'preparing_image' | 'starting_container' | 'connecting_executor'
  sandboxReady?: boolean
  sandboxExpiresAt?: number
  sandboxError?: string
}

export interface MockSessionMeta {
  id: string
  title: string
  activeAgent: string
  workspaceId?: string
  messageCount: number
  createdAt: number
  updatedAt: number
}

export interface MockMessageFile {
  name: string
  path: string
  size: number
}

export interface MockMessage {
  role: 'user' | 'assistant'
  content: string
  agent?: string
  files?: MockMessageFile[]
  isError?: boolean
  toolCall?: {
    toolCallId: string
    toolName: string
    status: 'pending' | 'completed' | 'error'
    title: string
    description?: string
    input?: string
    output?: string
    error?: string
  }
}

export interface MockSession {
  id: string
  title: string
  activeAgent: string
  workspaceId?: string
  createdAt: number
  updatedAt: number
  messages: MockMessage[]
}

export interface MockCronJob {
  id: string
  name: string
  description?: string
  enabled: boolean
  channel?: string
  workspaceId: string
  agentId: string
  conversationId?: string
  schedule:
    | { type: 'once'; runAt: number }
    | { type: 'interval'; everySeconds: number }
    | { type: 'cron'; cronExpr: string }
  prompt?: string
  exec?: string
  silent?: boolean
  mute?: boolean
  sessionMode?: string
  workDir?: string
  mode?: string
  timeoutMins?: number
  state: {
    lastRunAt?: number
    nextRunAt?: number
    lastStatus?: string
    lastError?: string
    runCount: number
  }
  createdAt: number
  updatedAt: number
}

export interface MockShareFile {
  path: string
}

export interface MockConversationShare {
  id: string
  token: string
  conversationId: string
  files: MockShareFile[]
  createdAt: number
  updatedAt: number
}

export interface MockFileInfo {
  path: string
  name: string
  isDir: boolean
}

interface MockWorkspaceTreeEntry {
  path: string
  name: string
  isDir: boolean
  previewKind?: 'code' | 'markdown' | 'image' | 'pdf' | 'html' | 'unsupported'
  children?: MockWorkspaceTreeEntry[]
}

export interface UploadedFile {
  name: string
  path: string
  size: number
}

export interface SetupDependencyItem {
  name: string
  command?: string
  package?: string
  status: 'checking' | 'ready' | 'missing' | 'not_installed' | 'installing' | 'error' | 'blocked'
  message?: string
  install?: string
}

export interface SetupSnapshot {
  ready: boolean
  environment?: SetupDependencyItem[]
  agents?: SetupDependencyItem[]
  acpPackages?: SetupDependencyItem[]
}

export interface MockDeviceAgentInfo {
  id: string
  name: string
}

export interface MockDevice {
  id: string
  name: string
  alias?: string
  displayName: string
  status: 'setup_required' | 'online' | 'offline' | 'busy' | 'error'
  setupReady: boolean
  setupStatus?: SetupSnapshot | null
  defaultAgentId?: string
  agents?: MockDeviceAgentInfo[]
  workspaceId?: string
  version?: string
  lastHeartbeat: number
  registeredAt: number
  updatedAt: number
  runningTaskIds?: string[]
}

export interface ChatRequestBody {
  message: string
  conversationId: string | null
  workspaceId: string | null
  files?: MockMessageFile[]
  deviceId?: string | null
  agentId?: string | null
}

type ChatResponseFactory =
  | string
  | ((context: {
      body: ChatRequestBody
      request: Request
      state: MockBackendState
    }) => string)

export interface MockBackendOptions {
  setupReady?: boolean
  setupStatusSequence?: boolean[]
  setupSubscribeEvents?: SetupSnapshot[]
  setupInstallEvents?: Array<Record<string, unknown>>
  agents?: MockAgent[]
  defaultAgent?: string
  workspaces?: MockWorkspace[]
  defaultWorkspace?: string
  devices?: MockDevice[]
  pairingCommand?: {
    command: string
    server: string
    configPath: string
  }
  workspaceFiles?: MockFileInfo[]
  uploadedFiles?: UploadedFile[]
  sessions?: MockSessionMeta[]
  sessionDetails?: Record<string, MockSession>
  shares?: MockConversationShare[]
  publicShareError?: string
  publicShareFileErrors?: Record<string, string>
  chatResponses?: ChatResponseFactory[]
  cronJobs?: MockCronJob[]
  wechatConfig?: {
    enabled: boolean
    loginMode: 'qr' | 'manual'
    accountId: string
    baseUrl: string
    workspaceId: string
    agentId: string
    hasToken: boolean
    maskedToken?: string
  }
  wechatStatus?: {
    running: boolean
    configured: boolean
    configError?: string
    lastError?: string
    lastSyncAt?: number
    lastLoginAt?: number
    lastMessageAt?: number
  }
  wechatBotToken?: string
  wechatTestResult?: {
    success: boolean
    message?: string
    error?: string
  }
  wechatLoginEvents?: Array<{
    event?: string
    data: Record<string, unknown>
  }>
}

export interface MockBackendState {
  setupReady: boolean
  setupStatusSequence: boolean[]
  setupSubscribeEvents: SetupSnapshot[]
  setupInstallEvents: Array<Record<string, unknown>>
  agents: MockAgent[]
  defaultAgent: string
  workspaces: MockWorkspace[]
  defaultWorkspace: string
  devices: MockDevice[]
  pairingCommand: {
    command: string
    server: string
    configPath: string
  }
  workspaceFiles: MockFileInfo[]
  uploadedFiles: UploadedFile[]
  sessions: MockSessionMeta[]
  sessionDetails: Record<string, MockSession>
  shares: MockConversationShare[]
  publicShareError?: string
  publicShareFileErrors: Record<string, string>
  shareCreateRequests: Array<{ conversationId: string; files: MockShareFile[] }>
  shareDeleteRequests: string[]
  chatResponses: ChatResponseFactory[]
  chatRequests: ChatRequestBody[]
  cronJobs: MockCronJob[]
  cronCreateRequests: Array<Record<string, unknown>>
  cronUpdateRequests: Array<Record<string, unknown>>
  cronDeleteRequests: string[]
  permissionConfirmations: Array<Record<string, unknown>>
  agentUpdateRequests: Array<Record<string, unknown>>
  workspaceCreates: Array<{
    name: string
    path: string
    kind?: string
    image?: string
    idleTimeoutSec?: number
    agents?: string[]
    deviceId?: string
    deviceName?: string
    remotePath?: string
  }>
  deviceSetupCheckRequests: string[]
  uploadRequests: number
  cancelRequests: Array<Record<string, unknown>>
  nextSessionNumber: number
  wechatConfig: {
    enabled: boolean
    loginMode: 'qr' | 'manual'
    accountId: string
    baseUrl: string
    workspaceId: string
    agentId: string
    hasToken: boolean
    maskedToken?: string
  }
  wechatStatus: {
    running: boolean
    configured: boolean
    configError?: string
    lastError?: string
    lastSyncAt?: number
    lastLoginAt?: number
    lastMessageAt?: number
  }
  wechatBotToken: string
  wechatSaveRequests: Array<Record<string, unknown>>
  wechatEnableRequests: number
  wechatDisableRequests: number
  wechatTestRequests: number
  wechatTestResult: {
    success: boolean
    message?: string
    error?: string
  }
  wechatLoginEvents: Array<{
    event?: string
    data: Record<string, unknown>
  }>
  wechatNextLoginId: number
}

const now = Date.UTC(2026, 3, 24, 5, 0, 0)
const DEFAULT_WECHAT_BASE_URL = 'https://ilinkai.weixin.qq.com'

export const DEFAULT_AGENTS: MockAgent[] = [
  {
    id: 'claude',
    name: 'Claude Code',
    backend: 'claude',
    permissionMode: 'default',
    sessionMode: 'default',
    command: 'npx',
    args: ['-y', '@agentclientprotocol/claude-agent-acp@0.30.0'],
    env: {
      ANTHROPIC_AUTH_TOKEN: 'token',
    },
    availableModes: [
      { value: 'default', label: 'Default' },
      { value: 'acceptEdits', label: 'Accept Edits' },
      { value: 'auto', label: 'Auto' },
      { value: 'bypassPermissions', label: 'YOLO' },
      { value: 'dontAsk', label: "Don't Ask" },
      { value: 'plan', label: 'Plan' },
    ],
    commands: [
      { name: 'plan', description: 'Create an execution plan' },
      { name: 'status', description: 'Show the current status' },
    ],
  },
  {
    id: 'codex',
    name: 'Codex CLI',
    backend: 'codex',
    permissionMode: 'default',
    sessionMode: 'default',
    command: 'npx',
    args: ['-y', '@zed-industries/codex-acp'],
    env: {
      OPENAI_API_KEY: 'token',
    },
    availableModes: [
      { value: 'default', label: 'Default' },
      { value: 'yolo', label: 'Full Auto' },
      { value: 'yoloNoSandbox', label: 'Full Auto (No Sandbox)' },
    ],
    commands: [
      { name: 'test', description: 'Run the relevant test suite' },
    ],
  },
  {
    id: 'qwen',
    name: 'Qwen Code',
    backend: 'qwen',
    permissionMode: 'default',
    sessionMode: 'default',
    command: 'npx',
    args: ['-y', '@qwen-code/qwen-code', '--acp'],
    env: {},
    availableModes: [
      { value: 'default', label: 'Default' },
      { value: 'yolo', label: 'YOLO' },
    ],
    commands: [],
  },
]

export const DEFAULT_WORKSPACES: MockWorkspace[] = [
  {
    id: 'ws-1',
    name: 'Lumi',
    path: '/tmp/lumi',
  },
  {
    id: 'ws-2',
    name: 'Wegent',
    path: '/Users/pengmd/c/Wegent',
  },
]

export const DEFAULT_DEVICES: MockDevice[] = []

export const DEFAULT_FILES: MockFileInfo[] = [
  {
    path: 'src/components/ChatPanel.tsx',
    name: 'ChatPanel.tsx',
    isDir: false,
  },
  {
    path: 'src/lib/request.ts',
    name: 'request.ts',
    isDir: false,
  },
]

export function buildSseStream(
  events: Array<{
    event?: string
    data: unknown
  }>
): string {
  return events
    .map(({ event, data }) => {
      const prefix = event ? `event: ${event}\n` : ''
      return `${prefix}data: ${JSON.stringify(data)}\n\n`
    })
    .join('')
}

export async function installMockBackend(
  page: Page,
  options: MockBackendOptions = {}
): Promise<MockBackendState> {
  const state = createMockBackendState(options)

  await page.addInitScript(() => {
    type MockCronEventDetail = {
      type: string
      payload: unknown
    }
    class MockEventSource extends EventTarget {
      url: string
      readyState = 1
      onopen: ((event: Event) => void) | null = null
      onmessage: ((event: MessageEvent) => void) | null = null
      onerror: ((event: Event) => void) | null = null

      constructor(url: string | URL) {
        super()
        this.url = String(url)
        window.addEventListener('mock-cron-event', this.handleMockCronEvent as EventListener)
        setTimeout(() => {
          const event = new Event('open')
          this.dispatchEvent(event)
          this.onopen?.(event)
        }, 0)
        void this.readStream()
      }

      close() {
        this.readyState = 2
        window.removeEventListener('mock-cron-event', this.handleMockCronEvent as EventListener)
      }

      private handleMockCronEvent = (event: Event) => {
        const detail = (event as CustomEvent<MockCronEventDetail>).detail
        if (!detail?.type) return
        const message = new MessageEvent(detail.type, { data: JSON.stringify(detail.payload) })
        this.dispatchEvent(message)
        if (detail.type === 'message') {
          this.onmessage?.(message)
        }
      }

      private async readStream() {
        try {
          const response = await fetch(this.url, { headers: { Accept: 'text/event-stream' } })
          if (!response.ok) throw new Error(`EventSource request failed: ${response.status}`)
          const text = await response.text()
          if (this.readyState === 2) return
          for (const frame of text.split('\n\n')) {
            const lines = frame.split('\n')
            let eventType = 'message'
            const dataLines: string[] = []
            for (const line of lines) {
              if (line.startsWith('event: ')) {
                eventType = line.slice(7)
              } else if (line.startsWith('data: ')) {
                dataLines.push(line.slice(6))
              }
            }
            if (dataLines.length === 0) continue
            const message = new MessageEvent(eventType, { data: dataLines.join('\n') })
            this.dispatchEvent(message)
            if (eventType === 'message') {
              this.onmessage?.(message)
            }
          }
        } catch {
          if (this.readyState === 2) return
          const event = new Event('error')
          this.dispatchEvent(event)
          this.onerror?.(event)
        }
      }
    }

    Object.defineProperty(window, 'EventSource', {
      configurable: true,
      writable: true,
      value: MockEventSource,
    })
  })

  if (process.env.DEBUG_E2E === '1') {
    page.on('console', (message) => {
      if (message.type() === 'error' || message.type() === 'warning') {
        console.error(`[browser:${message.type()}] ${message.text()}`)
      }
    })
    page.on('pageerror', (error) => {
      console.error(`[pageerror] ${error.message}`)
    })
    page.on('requestfailed', (request) => {
      console.error(`[requestfailed] ${request.method()} ${request.url()} ${request.failure()?.errorText ?? ''}`)
    })
  }

  await page.route(/^https?:\/\/[^/]+\/api(?:\/.*)?(?:\?.*)?$/, async (route, request) => {
    const url = new URL(request.url())
    const { pathname, searchParams } = url
    const method = request.method()

    if (pathname === '/api/setup/status' && method === 'GET') {
      const ready =
        state.setupStatusSequence.length > 0
          ? state.setupStatusSequence.shift() ?? state.setupReady
          : state.setupReady

      state.setupReady = ready

      await route.fulfill(jsonResponse({ ready }))
      return
    }

    if (pathname === '/api/setup/subscribe' && method === 'GET') {
      if (state.setupSubscribeEvents.length > 0) {
        state.setupReady = state.setupSubscribeEvents[state.setupSubscribeEvents.length - 1]?.ready ?? state.setupReady
      }

      await route.fulfill(sseResponse(buildSseStream(
        state.setupSubscribeEvents.map((snapshot) => ({ data: snapshot }))
      )))
      return
    }

    if (pathname === '/api/setup/install' && method === 'POST') {
      if (state.setupInstallEvents.length > 0) {
        const lastEvent = state.setupInstallEvents[state.setupInstallEvents.length - 1]
        if (lastEvent && typeof lastEvent.success === 'boolean') {
          state.setupReady = Boolean(lastEvent.success)
        }
      }

      const installEvents = state.setupInstallEvents.length > 0
        ? state.setupInstallEvents
        : [{ success: true }]

      await route.fulfill(sseResponse(buildSseStream(
        installEvents.map((eventData) => ({ data: eventData }))
      )))
      return
    }

    if (pathname === '/api/agents' && method === 'GET') {
      await route.fulfill(jsonResponse({
        agents: state.agents,
        default: state.defaultAgent,
      }))
      return
    }

    if (pathname === '/api/agents/update' && method === 'POST') {
      const body = readJson<Record<string, unknown>>(request)
      state.agentUpdateRequests.push(body)

      const agent = state.agents.find((item) => item.id === body.agentId)
      if (agent) {
        if (body.updateEnv && body.env && typeof body.env === 'object') {
          agent.env = body.env as Record<string, string>
        } else if (typeof body.sessionMode === 'string') {
          agent.sessionMode = body.sessionMode
        } else if (typeof body.permissionMode === 'string') {
          agent.permissionMode = body.permissionMode
        }
      }

      await route.fulfill(jsonResponse({ success: true }))
      return
    }

    if (pathname === '/api/workspaces/files' && method === 'GET') {
      const query = searchParams.get('q')?.toLowerCase() ?? ''
      const files = query
        ? state.workspaceFiles.filter((file) =>
            file.path.toLowerCase().includes(query) || file.name.toLowerCase().includes(query)
          )
        : state.workspaceFiles

      await route.fulfill(jsonResponse({ files }))
      return
    }

    if (pathname === '/api/workspaces/tree' && method === 'GET') {
      await route.fulfill(jsonResponse({ tree: buildWorkspaceTree(state.workspaceFiles) }))
      return
    }

    if (pathname === '/api/workspaces/meta' && method === 'GET') {
      const path = searchParams.get('path') ?? ''
      await route.fulfill(jsonResponse({ meta: buildWorkspaceMeta(path) }))
      return
    }

    if (pathname === '/api/workspaces/file' && method === 'GET') {
      const path = searchParams.get('path') ?? ''
      const meta = buildWorkspaceMeta(path)
      await route.fulfill(jsonResponse({
        meta,
        content: buildWorkspaceTextContent(path),
        truncated: false,
      }))
      return
    }

    if (pathname === '/api/workspaces/file-buffer' && method === 'GET') {
      const path = searchParams.get('path') ?? ''
      const meta = buildWorkspaceMeta(path)
      const ext = path.split('.').pop()?.toLowerCase()

      if (ext === 'pdf') {
        await route.fulfill({
          status: 200,
          contentType: 'application/pdf',
          body: buildMockPdf(),
        })
        return
      }

      await route.fulfill({
        status: 200,
        contentType: meta.mime || 'image/svg+xml',
        body: buildMockImage(path),
      })
      return
    }

    if (pathname === '/api/workspaces/html-preview' && method === 'GET') {
      const path = searchParams.get('path') ?? ''
      await route.fulfill({
        status: 200,
        contentType: 'text/html; charset=utf-8',
        body: buildMockHtmlPreview(path),
      })
      return
    }

    if (pathname === '/api/workspaces' && method === 'GET') {
      await route.fulfill(jsonResponse({
        workspaces: state.workspaces,
        default: state.defaultWorkspace,
      }))
      return
    }

    if (pathname === '/api/wechat/config' && method === 'GET') {
      syncWeChatStatus(state)
      await route.fulfill(jsonResponse(state.wechatConfig))
      return
    }

    if (pathname === '/api/cron/jobs' && method === 'GET') {
      const channel = searchParams.get('channel') ?? ''
      const conversationId = searchParams.get('conversationId') ?? ''
      const jobs = state.cronJobs.filter((job) => {
        if (channel && (job.channel || 'web') !== channel) return false
        if (conversationId && job.conversationId !== conversationId) return false
        return true
      })
      await route.fulfill(jsonResponse({ jobs }))
      return
    }

    if (pathname === '/api/cron/jobs' && method === 'POST') {
      const body = readJson<Record<string, unknown>>(request)
      state.cronCreateRequests.push(body)
      const schedule = body.schedule as MockCronJob['schedule']
      const job: MockCronJob = {
        id: `cron-${state.cronJobs.length + 1}`,
        name: String(body.name ?? ''),
        description: typeof body.description === 'string' ? body.description : undefined,
        enabled: typeof body.enabled === 'boolean' ? body.enabled : true,
        channel: typeof body.channel === 'string' ? body.channel : 'web',
        workspaceId: String(body.workspaceId ?? ''),
        agentId: String(body.agentId ?? ''),
        conversationId: typeof body.conversationId === 'string' ? body.conversationId : undefined,
        schedule,
        prompt: typeof body.prompt === 'string' ? body.prompt : undefined,
        exec: typeof body.exec === 'string' ? body.exec : undefined,
        silent: Boolean(body.silent),
        mute: Boolean(body.mute),
        sessionMode: typeof body.sessionMode === 'string' ? body.sessionMode : 'reuse',
        workDir: typeof body.workDir === 'string' ? body.workDir : undefined,
        mode: typeof body.mode === 'string' ? body.mode : undefined,
        timeoutMins: typeof body.timeoutMins === 'number' ? body.timeoutMins : undefined,
        state: {
          nextRunAt: now + 60 * 60 * 1000,
          runCount: 0,
        },
        createdAt: now,
        updatedAt: now,
      }
      state.cronJobs.unshift(job)
      await route.fulfill(jsonResponse({ job }))
      return
    }

    if (pathname.startsWith('/api/cron/jobs/')) {
      const trimmed = pathname.replace('/api/cron/jobs/', '')
      const [rawId, action] = trimmed.split('/')
      const id = decodeURIComponent(rawId ?? '')
      const job = state.cronJobs.find((item) => item.id === id)
      if (!job) {
        await route.fulfill(jsonResponse({ error: 'Job not found' }, 404))
        return
      }
      if (action === 'run' && method === 'POST') {
        await route.fulfill(jsonResponse({ success: true, conversationId: job.conversationId }))
        return
      }
      if (method === 'GET') {
        await route.fulfill(jsonResponse({ job }))
        return
      }
      if (method === 'PUT') {
        const body = readJson<Record<string, unknown>>(request)
        state.cronUpdateRequests.push(body)
        Object.assign(job, body)
        if (body.schedule) {
          job.schedule = body.schedule as MockCronJob['schedule']
        }
        job.updatedAt = now + state.cronUpdateRequests.length
        await route.fulfill(jsonResponse({ job }))
        return
      }
      if (method === 'DELETE') {
        state.cronDeleteRequests.push(id)
        state.cronJobs = state.cronJobs.filter((item) => item.id !== id)
        await route.fulfill(jsonResponse({ success: true }))
        return
      }
    }

    if (pathname === '/api/cron/events' && method === 'GET') {
      await route.fulfill(sseResponse(''))
      return
    }

    if (pathname === '/api/wechat/config' && method === 'POST') {
      const body = readJson<Record<string, unknown>>(request)
      state.wechatSaveRequests.push(body)

      state.wechatConfig.enabled = Boolean(body.enabled)
      state.wechatConfig.loginMode =
        body.loginMode === 'manual' ? 'manual' : 'qr'
      state.wechatConfig.accountId = typeof body.accountId === 'string' ? body.accountId : ''
      state.wechatConfig.baseUrl =
        typeof body.baseUrl === 'string' && body.baseUrl.trim()
          ? body.baseUrl
          : DEFAULT_WECHAT_BASE_URL
      state.wechatConfig.workspaceId = typeof body.workspaceId === 'string' ? body.workspaceId : ''
      state.wechatConfig.agentId = typeof body.agentId === 'string' ? body.agentId : ''

      if (Object.prototype.hasOwnProperty.call(body, 'botToken')) {
        state.wechatBotToken = typeof body.botToken === 'string' ? body.botToken : ''
      }

      state.wechatConfig.hasToken = Boolean(state.wechatBotToken)
      state.wechatConfig.maskedToken = maskToken(state.wechatBotToken)
      syncWeChatStatus(state)

      await route.fulfill(jsonResponse({
        success: true,
        config: state.wechatConfig,
      }))
      return
    }

    if (pathname === '/api/wechat/status' && method === 'GET') {
      syncWeChatStatus(state)
      await route.fulfill(jsonResponse(state.wechatStatus))
      return
    }

    if (pathname === '/api/wechat/test' && method === 'POST') {
      state.wechatTestRequests += 1
      syncWeChatStatus(state)

      if (!state.wechatStatus.configured) {
        await route.fulfill(jsonResponse({
          success: false,
          error: state.wechatStatus.configError || 'wechat config is invalid',
        }))
        return
      }

      await route.fulfill(jsonResponse(state.wechatTestResult))
      return
    }

    if (pathname === '/api/wechat/enable' && method === 'POST') {
      state.wechatEnableRequests += 1
      syncWeChatStatus(state)
      if (!state.wechatStatus.configured) {
        await route.fulfill(jsonResponse({
          error: state.wechatStatus.configError || 'wechat config is invalid',
        }, 400))
        return
      }

      state.wechatConfig.enabled = true
      state.wechatStatus.running = true
      await route.fulfill(jsonResponse({ success: true }))
      return
    }

    if (pathname === '/api/wechat/disable' && method === 'POST') {
      state.wechatDisableRequests += 1
      state.wechatConfig.enabled = false
      state.wechatStatus.running = false
      await route.fulfill(jsonResponse({ success: true }))
      return
    }

    if (pathname === '/api/wechat/login/start' && method === 'POST') {
      await route.fulfill(jsonResponse({
        loginId: `wxlogin_${state.wechatNextLoginId++}`,
      }))
      return
    }

    if (pathname === '/api/wechat/login/events' && method === 'GET') {
      applyWeChatLoginEvents(state)
      await route.fulfill(sseResponse(buildSseStream(state.wechatLoginEvents)))
      return
    }

    if (pathname === '/api/devices' && method === 'GET') {
      await route.fulfill(jsonResponse({ devices: state.devices }))
      return
    }

    if (pathname === '/api/devices/pairing-command' && method === 'GET') {
      await route.fulfill(jsonResponse(state.pairingCommand))
      return
    }

    if (pathname.startsWith('/api/devices/') && pathname.endsWith('/alias') && method === 'PUT') {
      const parts = pathname.split('/')
      const deviceId = decodeURIComponent(parts[3] ?? '')
      const body = readJson<{ alias?: string }>(request)
      const device = state.devices.find((item) => item.id === deviceId)
      if (!device) {
        await route.fulfill(jsonResponse({ error: 'Device not found' }, 404))
        return
      }

      device.alias = body.alias?.trim() || ''
      device.displayName = device.alias || device.name
      await route.fulfill(jsonResponse({ device }))
      return
    }

    if (pathname.startsWith('/api/devices/') && pathname.endsWith('/setup/check') && method === 'POST') {
      const parts = pathname.split('/')
      const deviceId = decodeURIComponent(parts[3] ?? '')
      const device = state.devices.find((item) => item.id === deviceId)
      if (!device) {
        await route.fulfill(jsonResponse({ error: 'Device not found' }, 404))
        return
      }

      if (device.status === 'offline') {
        await route.fulfill(jsonResponse({ error: 'Device is offline' }, 409))
        return
      }

      state.deviceSetupCheckRequests.push(deviceId)
      device.status = 'online'
      device.setupReady = true
      device.setupStatus = {
        ready: true,
        environment: [
          {
            name: 'npm',
            command: 'npm -v',
            status: 'ready',
            message: '10.0.0',
          },
        ],
        agents: [
          {
            name: 'Claude Code',
            command: 'npx -y @agentclientprotocol/claude-agent-acp@0.30.0',
            status: 'ready',
            message: 'Installed',
          },
        ],
        acpPackages: [
          {
            name: 'ACP package',
            package: '@agentclientprotocol/claude-agent-acp',
            status: 'ready',
            message: 'Installed',
          },
        ],
      }
      device.updatedAt = now + state.deviceSetupCheckRequests.length

      await route.fulfill(jsonResponse({ success: true, message: 'Setup check requested' }, 202))
      return
    }

    if (pathname === '/api/workspaces' && method === 'POST') {
      const body = readJson<{
        name?: string
        path?: string
        kind?: 'local' | 'remote' | 'sandbox'
        image?: string
        idleTimeoutSec?: number
        agents?: string[]
        deviceId?: string
        deviceName?: string
        remotePath?: string
      }>(request)
      const workspace: MockWorkspace = {
        id: `ws-${state.workspaces.length + 1}`,
        name: body.name || 'Workspace',
        path: body.path || '/tmp/workspace',
        kind: body.kind,
        image: body.image,
        idleTimeoutSec: body.idleTimeoutSec,
        agents: body.agents,
        deviceId: body.deviceId,
        deviceName: body.deviceName,
        remotePath: body.remotePath,
        sandboxStatus: body.kind === 'sandbox' ? 'terminated' : undefined,
        sandboxReady: body.kind === 'sandbox' ? false : undefined,
      }

      state.workspaceCreates.push({
        name: workspace.name,
        path: workspace.path,
        kind: body.kind,
        image: body.image,
        idleTimeoutSec: body.idleTimeoutSec,
        agents: body.agents,
        deviceId: body.deviceId,
        deviceName: body.deviceName,
        remotePath: body.remotePath,
      })
      state.workspaces.push(workspace)

      await route.fulfill(jsonResponse({ workspace }))
      return
    }

    if (pathname === '/api/workspaces/sandbox/preflight' && method === 'POST') {
      const body = readJson<{ path?: string; image?: string }>(request)
      const path = body.path ?? ''
      const isAbsolute =
        path === '' || path.startsWith('/') || /^[A-Za-z]:[\\/]/.test(path)
      await route.fulfill(
        jsonResponse(
          isAbsolute
            ? {
                ok: true,
                code: 'ready',
                message: '',
                recoverable: true,
                details: '',
              }
            : {
                ok: false,
                code: 'path_invalid',
                message: 'Path must be absolute',
                recoverable: false,
                details: '',
              },
        ),
      )
      return
    }

    if (pathname.startsWith('/api/shares/conversations/by-conversation/')) {
      const conversationId = decodeURIComponent(pathname.replace('/api/shares/conversations/by-conversation/', ''))

      if (method === 'GET') {
        await route.fulfill(jsonResponse({
          share: state.shares.find((share) => share.conversationId === conversationId) ?? null,
        }))
        return
      }

      if (method === 'DELETE') {
        state.shareDeleteRequests.push(conversationId)
        state.shares = state.shares.filter((share) => share.conversationId !== conversationId)
        await route.fulfill(jsonResponse({ success: true }))
        return
      }
    }

    if (pathname === '/api/shares/conversations' && method === 'POST') {
      const body = readJson<{ conversationId?: string; files?: MockShareFile[] }>(request)
      const conversationId = body.conversationId ?? ''
      const files = normalizeShareFiles(body.files ?? [])
      state.shareCreateRequests.push({ conversationId, files })

      let share = state.shares.find((item) => item.conversationId === conversationId)
      if (share) {
        share.files = files
        share.updatedAt = now + state.shareCreateRequests.length
      } else {
        share = {
          id: `share-${state.shares.length + 1}`,
          token: `share-token-${state.shares.length + 1}`,
          conversationId,
          files,
          createdAt: now,
          updatedAt: now,
        }
        state.shares.push(share)
      }

      await route.fulfill(jsonResponse({ share }))
      return
    }

    if (pathname.startsWith('/api/public/shares/conversations/')) {
      const trimmed = pathname.replace('/api/public/shares/conversations/', '')
      const [rawToken, resource] = trimmed.split('/')
      const token = decodeURIComponent(rawToken ?? '')
      const share = state.shares.find((item) => item.token === token)
      if (!share) {
        await route.fulfill(jsonResponse({ error: 'Shared conversation not found' }, 404))
        return
      }
      if (state.publicShareError) {
        await route.fulfill(jsonResponse({ error: state.publicShareError }, 503))
        return
      }

      const session = state.sessionDetails[share.conversationId]
      if (!session) {
        await route.fulfill(jsonResponse({ error: 'Shared conversation not found' }, 404))
        return
      }

      if (!resource && method === 'GET') {
        await route.fulfill(jsonResponse(buildPublicSharedConversation(state, share, session)))
        return
      }

      if (resource === 'file-meta' && method === 'GET') {
        const fileId = searchParams.get('fileId') ?? ''
        if (!isSharedFileAllowed(share, fileId)) {
          await route.fulfill(jsonResponse({ error: 'Shared file not found' }, 404))
          return
        }
        if (state.publicShareFileErrors[fileId]) {
          await route.fulfill(jsonResponse({ error: state.publicShareFileErrors[fileId] }, 503))
          return
        }

        await route.fulfill(jsonResponse({ meta: buildWorkspaceMeta(fileId) }))
        return
      }

      if (resource === 'file-content' && method === 'GET') {
        const fileId = searchParams.get('fileId') ?? ''
        if (!isSharedFileAllowed(share, fileId)) {
          await route.fulfill(jsonResponse({ error: 'Shared file not found' }, 404))
          return
        }
        if (state.publicShareFileErrors[fileId]) {
          await route.fulfill(jsonResponse({ error: state.publicShareFileErrors[fileId] }, 503))
          return
        }

        await route.fulfill(jsonResponse({
          meta: buildWorkspaceMeta(fileId),
          content: buildWorkspaceTextContent(fileId),
          truncated: false,
        }))
        return
      }
    }

    if (pathname === '/api/sessions' && method === 'GET') {
      await route.fulfill(jsonResponse({ sessions: state.sessions }))
      return
    }

    if (pathname === '/api/sessions/new' && method === 'POST') {
      const body = readJson<{ workspaceId?: string }>(request)
      const sessionId = `sess-${state.nextSessionNumber++}`
      const session: MockSession = {
        id: sessionId,
        title: 'New Chat',
        activeAgent: state.defaultAgent,
        workspaceId: body.workspaceId || state.defaultWorkspace,
        createdAt: now,
        updatedAt: now,
        messages: [],
      }

      state.sessionDetails[sessionId] = session
      state.sessions.unshift({
        id: session.id,
        title: session.title,
        activeAgent: session.activeAgent,
        workspaceId: session.workspaceId,
        messageCount: 0,
        createdAt: session.createdAt,
        updatedAt: session.updatedAt,
      })

      await route.fulfill(jsonResponse({
        session: {
          id: session.id,
          title: session.title,
          activeAgent: session.activeAgent,
          workspaceId: session.workspaceId,
          createdAt: session.createdAt,
          updatedAt: session.updatedAt,
        },
      }))
      return
    }

    if (pathname.startsWith('/api/sessions/') && pathname !== '/api/sessions/new') {
      const id = pathname.replace('/api/sessions/', '')

      if (method === 'GET') {
        const session = state.sessionDetails[id]
        if (!session) {
          await route.fulfill(jsonResponse({ error: 'Not found' }, 404))
          return
        }

        await route.fulfill(jsonResponse({ session }))
        return
      }

      if (method === 'DELETE') {
        delete state.sessionDetails[id]
        state.sessions = state.sessions.filter((session) => session.id !== id)
        await route.fulfill(jsonResponse({ success: true }))
        return
      }
    }

    if (pathname === '/api/chat' && method === 'POST') {
      const body = readJson<ChatRequestBody>(request)
      state.chatRequests.push(body)

      if (body.deviceId) {
        const device = state.devices.find((item) => item.id === body.deviceId)
        if (!device || device.status === 'offline') {
          await route.fulfill(sseResponse(buildSseStream([
            {
              event: 'error',
              data: {
                message: 'Device is offline',
              },
            },
          ])))
          return
        }

        if (!device.setupReady || device.status === 'setup_required') {
          await route.fulfill(sseResponse(buildSseStream([
            {
              event: 'error',
              data: {
                message: 'Device setup is not ready',
              },
            },
          ])))
          return
        }

        if (device.status === 'busy') {
          await route.fulfill(sseResponse(buildSseStream([
            {
              event: 'error',
              data: {
                message: 'Device is busy',
              },
            },
          ])))
          return
        }
      }

      if (body.conversationId && state.sessionDetails[body.conversationId]) {
        state.sessionDetails[body.conversationId].messages.push({
          role: 'user',
          content: body.message,
          files: body.files,
        })
      }

      const nextResponse =
        state.chatResponses.shift() ??
        buildSseStream([
          {
            data: {
              conversationId: body.conversationId || 'sess-default',
              agent: state.defaultAgent,
              sessionId: 'agent-session-1',
            },
          },
          {
            data: {
              update: {
                sessionUpdate: 'agent_message_chunk',
                content: {
                  type: 'text',
                  text: 'Mock response',
                },
              },
            },
          },
          {
            data: {
              stopReason: 'end_turn',
            },
          },
        ])

      const bodyText =
        typeof nextResponse === 'function'
          ? nextResponse({ body, request, state })
          : nextResponse

      await route.fulfill(sseResponse(bodyText))
      return
    }

    if (pathname === '/api/chat/cancel' && method === 'POST') {
      state.cancelRequests.push(readJson<Record<string, unknown>>(request))
      await route.fulfill(jsonResponse({ success: true }))
      return
    }

    if (pathname === '/api/permission/confirm' && method === 'POST') {
      state.permissionConfirmations.push(readJson<Record<string, unknown>>(request))
      await route.fulfill(jsonResponse({ success: true }))
      return
    }

    if (pathname === '/api/upload' && method === 'POST') {
      state.uploadRequests += 1
      await route.fulfill(jsonResponse({
        success: true,
        files: state.uploadedFiles,
      }))
      return
    }

    if (pathname === '/api/upload/cleanup' && method === 'POST') {
      await route.fulfill(jsonResponse({ success: true }))
      return
    }

    await route.fulfill(jsonResponse({ error: `Unhandled mock for ${method} ${pathname}` }, 500))
  })

  return state
}

function createMockBackendState(options: MockBackendOptions): MockBackendState {
  const agents = clone(options.agents ?? DEFAULT_AGENTS)
  const workspaces = clone(options.workspaces ?? DEFAULT_WORKSPACES)
  const devices = clone(options.devices ?? DEFAULT_DEVICES)
  const sessions = clone(options.sessions ?? [])
  const sessionDetails = clone(options.sessionDetails ?? {})
  const shares = clone(options.shares ?? [])
  const defaultWorkspace = options.defaultWorkspace ?? workspaces[0]?.id ?? ''
  const wechatBotToken = options.wechatBotToken ?? 'wechat-secret-token'
  const wechatConfig: MockBackendState['wechatConfig'] = clone(
    options.wechatConfig ?? {
      enabled: false,
      loginMode: 'qr',
      accountId: 'wx_account',
      baseUrl: DEFAULT_WECHAT_BASE_URL,
      workspaceId: defaultWorkspace,
      agentId: agents[0]?.id ?? 'claude',
      hasToken: Boolean(wechatBotToken),
      maskedToken: maskToken(wechatBotToken),
    }
  )
  wechatConfig.hasToken = Boolean(wechatBotToken)
  wechatConfig.maskedToken = maskToken(wechatBotToken)
  const wechatStatus = clone(
    options.wechatStatus ?? {
      running: false,
      configured: true,
      configError: '',
      lastError: '',
      lastSyncAt: now,
      lastLoginAt: now,
      lastMessageAt: now,
    }
  )
  const wechatLoginEvents = clone(
    options.wechatLoginEvents ?? [
      {
        event: 'qr',
        data: {
          ticket: 'wxlogin_ticket_1',
          imageUrl: 'https://example.com/mock-wechat-qr.png',
        },
      },
      {
        event: 'scanned',
        data: {},
      },
      {
        event: 'confirmed',
        data: {
          accountId: 'wx_confirmed_account',
          baseUrl: DEFAULT_WECHAT_BASE_URL,
          hasToken: true,
        },
      },
      {
        event: 'done',
        data: {},
      },
    ]
  )

  const state: MockBackendState = {
    setupReady: options.setupReady ?? true,
    setupStatusSequence: [...(options.setupStatusSequence ?? [])],
    setupSubscribeEvents: clone(
      options.setupSubscribeEvents ?? [
        {
          ready: options.setupReady ?? true,
          environment: [
            {
              name: 'Node.js',
              command: 'node -v',
              status: 'ready',
              message: 'v22.0.0',
            },
          ],
          agents: [
            {
              name: 'Claude Code',
              command: 'npx -y @agentclientprotocol/claude-agent-acp@0.30.0',
              status: 'ready',
              message: 'Installed',
            },
            {
              name: 'Qwen Code',
              command: 'qwen',
              status: 'ready',
              message: 'Installed',
            },
          ],
          acpPackages: [
            {
              name: 'Qwen Code',
              package: '@qwen-code/qwen-code',
              status: 'ready',
              message: 'Cached',
            },
          ],
        },
      ]
    ),
    setupInstallEvents: clone(options.setupInstallEvents ?? []),
    agents,
    defaultAgent: options.defaultAgent ?? agents[0]?.id ?? 'claude',
    workspaces,
    defaultWorkspace,
    devices,
    pairingCommand: clone(
      options.pairingCommand ?? {
        command: 'device-executor connect --server http://192.168.1.23:4173 --token mock-secret',
        server: 'http://192.168.1.23:4173',
        configPath: '~/.device-executor/config.json',
      }
    ),
    workspaceFiles: clone(options.workspaceFiles ?? DEFAULT_FILES),
    uploadedFiles: clone(
      options.uploadedFiles ?? [
        {
          name: 'spec.md',
          path: '/tmp/lumi/spec.md',
          size: 1024,
        },
      ]
    ),
    sessions,
    sessionDetails,
    shares,
    publicShareError: options.publicShareError,
    publicShareFileErrors: clone(options.publicShareFileErrors ?? {}),
    shareCreateRequests: [],
    shareDeleteRequests: [],
    chatResponses: [...(options.chatResponses ?? [])],
    chatRequests: [],
    cronJobs: clone(options.cronJobs ?? []),
    cronCreateRequests: [],
    cronUpdateRequests: [],
    cronDeleteRequests: [],
    permissionConfirmations: [],
    agentUpdateRequests: [],
    workspaceCreates: [],
    deviceSetupCheckRequests: [],
    uploadRequests: 0,
    cancelRequests: [],
    nextSessionNumber: sessions.length + 1,
    wechatConfig,
    wechatStatus,
    wechatBotToken,
    wechatSaveRequests: [],
    wechatEnableRequests: 0,
    wechatDisableRequests: 0,
    wechatTestRequests: 0,
    wechatTestResult: clone(
      options.wechatTestResult ?? {
        success: true,
        message: 'connection ok',
      }
    ),
    wechatLoginEvents,
    wechatNextLoginId: 1,
  }

  syncWeChatStatus(state)
  return state
}

function readJson<T>(request: Request): T {
  const body = request.postData() ?? '{}'
  try {
    return JSON.parse(body) as T
  } catch {
    return {} as T
  }
}

function jsonResponse(data: unknown, status = 200) {
  return {
    status,
    contentType: 'application/json',
    body: JSON.stringify(data),
  }
}

function sseResponse(body: string) {
  return {
    status: 200,
    headers: {
      'cache-control': 'no-cache',
      connection: 'keep-alive',
      'content-type': 'text/event-stream',
    },
    body,
  }
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

function maskToken(token: string) {
  if (!token) return undefined
  if (token.length < 8) return '********'
  return `${token.slice(0, 4)}********${token.slice(-4)}`
}

function syncWeChatStatus(state: MockBackendState) {
  const configError = validateWeChatConfig(state)
  state.wechatConfig.hasToken = Boolean(state.wechatBotToken)
  state.wechatConfig.maskedToken = maskToken(state.wechatBotToken)
  state.wechatStatus.configured = !configError
  state.wechatStatus.configError = configError
  if (!state.wechatStatus.configured) {
    state.wechatStatus.running = false
  } else if (!state.wechatConfig.enabled) {
    state.wechatStatus.running = false
  }
}

function validateWeChatConfig(state: MockBackendState) {
  const config = state.wechatConfig
  if (!config.workspaceId) return 'workspace is required'

  const workspace = state.workspaces.find((item) => item.id === config.workspaceId)
  if (!workspace) return 'workspace not found'
  if (workspace.kind && workspace.kind !== 'local') return 'workspace must be local'

  if (!config.agentId) return 'agent is required'
  if (!state.agents.some((agent) => agent.id === config.agentId)) return 'agent not found'

  if (!config.accountId) return 'accountId is required'
  if (!state.wechatBotToken) return 'botToken is required'

  return ''
}

function applyWeChatLoginEvents(state: MockBackendState) {
  for (const event of state.wechatLoginEvents) {
    if (event.event !== 'confirmed') continue

    state.wechatConfig.loginMode = 'qr'
    state.wechatConfig.accountId =
      typeof event.data.accountId === 'string' ? event.data.accountId : state.wechatConfig.accountId
    state.wechatConfig.baseUrl =
      typeof event.data.baseUrl === 'string' ? event.data.baseUrl : state.wechatConfig.baseUrl

    const confirmedHasToken =
      typeof event.data.hasToken === 'boolean' ? event.data.hasToken : Boolean(state.wechatBotToken)
    state.wechatBotToken = confirmedHasToken ? state.wechatBotToken || 'confirmed-login-token' : ''
    state.wechatConfig.hasToken = Boolean(state.wechatBotToken)
    state.wechatConfig.maskedToken = maskToken(state.wechatBotToken)
    state.wechatStatus.lastLoginAt = now + 60_000
  }

  syncWeChatStatus(state)
}

function buildWorkspaceTree(files: MockFileInfo[]): MockWorkspaceTreeEntry[] {
  const root: MockWorkspaceTreeEntry[] = []
  const folders = new Map<string, MockWorkspaceTreeEntry>()

  const ensureFolder = (folderPath: string) => {
    if (!folderPath) return null
    if (folders.has(folderPath)) return folders.get(folderPath) || null

    const segments = folderPath.split('/')
    const name = segments[segments.length - 1] || folderPath
    const node: MockWorkspaceTreeEntry = {
      path: folderPath,
      name,
      isDir: true,
      children: [],
    }
    folders.set(folderPath, node)

    const parentPath = segments.slice(0, -1).join('/')
    if (parentPath) {
      const parent = ensureFolder(parentPath)
      parent?.children?.push(node)
    } else {
      root.push(node)
    }

    return node
  }

  files.forEach((file) => {
    const segments = file.path.split('/')
    const parentPath = segments.slice(0, -1).join('/')
    const entry: MockWorkspaceTreeEntry = {
      path: file.path,
      name: file.name,
      isDir: false,
      previewKind: detectPreviewKind(file.path),
    }

    if (parentPath) {
      const parent = ensureFolder(parentPath)
      parent?.children?.push(entry)
    } else {
      root.push(entry)
    }
  })

  return root
}

function normalizeShareFiles(files: MockShareFile[]) {
  const seen = new Set<string>()
  const normalized: MockShareFile[] = []

  for (const file of files) {
    const path = file.path.trim()
    if (!path || seen.has(path)) continue
    seen.add(path)
    normalized.push({ path })
  }

  return normalized
}

function isSharedFileAllowed(share: MockConversationShare, path: string) {
  return share.files.some((file) => file.path === path)
}

function buildPublicSharedConversation(
  state: MockBackendState,
  share: MockConversationShare,
  session: MockSession
) {
  const files = share.files.map((file) => {
    const workspaceFile = state.workspaceFiles.find((item) => item.path === file.path)
    return {
      name: workspaceFile?.name ?? file.path.split('/').pop() ?? file.path,
      path: file.path,
      size: 1024,
    }
  })

  return {
    id: share.conversationId,
    title: session.title,
    files,
    messages: session.messages.map((message) => ({
      ...message,
      files: (message.files || []).filter((file) => isSharedFileAllowed(share, file.path)),
    })),
    createdAt: session.createdAt,
    updatedAt: session.updatedAt,
  }
}

function buildWorkspaceMeta(path: string) {
  return {
    path,
    name: path.split('/').pop() || path,
    size: 1024,
    modifiedAt: now,
    mime: detectMime(path),
    previewKind: detectPreviewKind(path),
  }
}

function buildWorkspaceTextContent(path: string) {
  const ext = path.split('.').pop()?.toLowerCase()
  if (ext === 'md') {
    return `# ${path}\n\nMock markdown content from the e2e backend.\n`
  }
  if (ext === 'yml' || ext === 'yaml') {
    return `name: preview-test\nkind: yaml\nsource: mock-backend\n`
  }
  if (ext === 'json') {
    return JSON.stringify({ path, mode: 'mock-backend' }, null, 2)
  }

  return `// ${path}\nexport const preview = 'loaded from mock backend'\n`
}

function buildMockImage(path: string) {
  return `<?xml version="1.0" encoding="UTF-8"?>
<svg width="800" height="520" viewBox="0 0 800 520" xmlns="http://www.w3.org/2000/svg">
  <rect width="800" height="520" rx="28" fill="#0f172a"/>
  <rect x="24" y="24" width="752" height="472" rx="22" fill="#111827" stroke="#334155"/>
  <text x="56" y="120" fill="#e2e8f0" font-size="34" font-family="system-ui" font-weight="700">${path}</text>
  <text x="56" y="178" fill="#94a3b8" font-size="22" font-family="system-ui">mock binary preview payload</text>
  <circle cx="640" cy="250" r="92" fill="#1d4ed8" fill-opacity="0.24"/>
  <circle cx="640" cy="250" r="48" fill="#38bdf8"/>
</svg>`
}

function buildMockPdf() {
  return `%PDF-1.4
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Count 1 /Kids [3 0 R] >>
endobj
3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>
endobj
4 0 obj
<< /Length 63 >>
stream
BT
/F1 18 Tf
72 720 Td
(Mock PDF Preview) Tj
ET
endstream
endobj
trailer
<< /Root 1 0 R >>
%%EOF`
}

function detectPreviewKind(path: string): 'code' | 'markdown' | 'image' | 'pdf' | 'html' | 'unsupported' {
  const ext = path.split('.').pop()?.toLowerCase()
  if (!ext) return 'unsupported'
  if (['md', 'markdown', 'mdx'].includes(ext)) return 'markdown'
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(ext)) return 'image'
  if (ext === 'pdf') return 'pdf'
  if (['html', 'htm'].includes(ext)) return 'html'
  if (['ts', 'tsx', 'js', 'jsx', 'json', 'yml', 'yaml', 'go', 'rs', 'py', 'css', 'txt'].includes(ext)) {
    return 'code'
  }
  return 'unsupported'
}

function detectMime(path: string) {
  const ext = path.split('.').pop()?.toLowerCase()
  switch (ext) {
    case 'md':
      return 'text/markdown; charset=utf-8'
    case 'json':
      return 'application/json; charset=utf-8'
    case 'html':
    case 'htm':
      return 'text/html; charset=utf-8'
    case 'pdf':
      return 'application/pdf'
    case 'png':
      return 'image/png'
    case 'svg':
      return 'image/svg+xml'
    default:
      return 'text/plain; charset=utf-8'
  }
}

function buildMockHtmlPreview(path: string) {
  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>${path}</title>
  <style>
    body {
      margin: 0;
      min-height: 100vh;
      display: grid;
      place-items: center;
      background: linear-gradient(135deg, #0f172a, #111827);
      color: #e5eefc;
      font-family: system-ui, sans-serif;
    }
    .card {
      width: min(680px, calc(100vw - 64px));
      padding: 32px;
      border-radius: 24px;
      background: rgba(15, 23, 42, 0.78);
      border: 1px solid rgba(148, 163, 184, 0.24);
      box-shadow: 0 24px 80px rgba(0, 0, 0, 0.36);
    }
    h1 {
      margin: 0 0 12px;
      font-size: 30px;
    }
    p {
      margin: 0;
      line-height: 1.7;
      color: #cbd5e1;
    }
  </style>
</head>
<body>
  <section class="card">
    <h1>Mock HTML Preview</h1>
    <p>${path} is rendered inside a sandboxed iframe so E2E can exercise the HTML preview path.</p>
  </section>
</body>
</html>`
}
