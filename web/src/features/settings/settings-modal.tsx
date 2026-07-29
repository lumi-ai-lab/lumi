'use client'

import { useEffect, useRef, useState } from 'react'
import { QRCodeSVG } from 'qrcode.react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useI18n } from '@/features/i18n/i18n-provider'
import { useTheme } from '@/features/theme/theme-provider'
import {
  disableWeCom,
  disableWeChat,
  enableWeCom,
  enableWeChat,
  fetchWeComConfig,
  fetchWeComStatus,
  fetchWeChatConfig,
  fetchWeChatStatus,
  fetchWorkspaces,
  saveWeComConfig,
  saveWeChatConfig,
  startWeChatLogin,
  subscribeWeChatLogin,
  testWeComConnection,
  testWeChatConnection,
} from '@/lib/api'
import type {
  Agent,
  SaveWeComConfigInput,
  SaveWeChatConfigInput,
  WeComConfig,
  WeComStatus,
  WeChatConfig,
  WeChatStatus,
  Workspace,
} from '@/lib/types'
import { cn } from '@/lib/utils'

function formatCommand(agent: Agent) {
  return [agent.command, ...(agent.args || [])].filter(Boolean).join(' ') || '-'
}

function getAgentNameClass(agentId: string) {
  if (agentId === 'claude') {
    return 'bg-[linear-gradient(135deg,#667eea_0%,#764ba2_100%)] text-white'
  }

  if (agentId === 'codex') {
    return 'bg-[linear-gradient(135deg,#10a37f_0%,#1a7f5a_100%)] text-white'
  }

  return 'bg-muted text-foreground'
}

function getModeButtonClass(active: boolean) {
  return active
    ? 'border-transparent bg-primary text-primary-foreground'
    : 'border-border bg-transparent text-[rgb(var(--color-text-secondary))] hover:bg-accent hover:text-foreground'
}

function getErrorMessage(error: unknown, fallback: string) {
  if (error instanceof Error && error.message) {
    return error.message
  }

  return fallback
}

function formatChannelTimestamp(timestamp?: number) {
  if (!timestamp) return '-'

  const date = new Date(timestamp)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  return `${year}-${month}-${day} ${hours}:${minutes}`
}

function isLocalWorkspace(workspace: Workspace) {
  return !workspace.kind || workspace.kind === 'local'
}

type WeChatLoginPhase =
  | 'idle'
  | 'waiting'
  | 'scanned'
  | 'confirmed'
  | 'expired'
  | 'error'

export function SettingsModal({
  agents,
  defaultAgent,
  onOpenChange,
  onUpdateAgentEnv,
  onUpdateAgentMode,
  open,
}: {
  agents: Agent[]
  defaultAgent: string
  onOpenChange: (open: boolean) => void
  onUpdateAgentEnv: (agentId: string, env: Record<string, string>) => Promise<{ success: boolean; error?: string }>
  onUpdateAgentMode: (
    agentId: string,
    mode: string
  ) => Promise<{ success: boolean; error?: string }>
  open: boolean
}) {
  const { currentLang, setLang, t } = useI18n()
  const { currentTheme, toggleTheme } = useTheme()
  const [editingEnv, setEditingEnv] = useState<string | null>(null)
  const [envDrafts, setEnvDrafts] = useState<Record<string, Array<{ key: string; value: string }>>>({})
  const [error, setError] = useState<string | null>(null)
  const [wechatConfig, setWeChatConfig] = useState<WeChatConfig | null>(null)
  const [wechatStatus, setWeChatStatus] = useState<WeChatStatus | null>(null)
  const [wechatWorkspaces, setWeChatWorkspaces] = useState<Workspace[]>([])
  const [wechatLoading, setWeChatLoading] = useState(false)
  const [wechatOperationError, setWeChatOperationError] = useState<string | null>(null)
  const [wechatBotTokenDraft, setWeChatBotTokenDraft] = useState('')
  const [wechatAdvancedOpen, setWeChatAdvancedOpen] = useState(false)
  const [wechatSaving, setWeChatSaving] = useState(false)
  const [wechatTesting, setWeChatTesting] = useState(false)
  const [wechatEnabling, setWeChatEnabling] = useState(false)
  const [wechatDisabling, setWeChatDisabling] = useState(false)
  const [wechatLoginStarting, setWeChatLoginStarting] = useState(false)
  const [wechatLoginPhase, setWeChatLoginPhase] = useState<WeChatLoginPhase>('idle')
  const [wechatQrTicket, setWeChatQrTicket] = useState('')
  const [wechatQrPageUrl, setWeChatQrPageUrl] = useState('')
  const [wecomConfig, setWeComConfig] = useState<WeComConfig | null>(null)
  const [wecomStatus, setWeComStatus] = useState<WeComStatus | null>(null)
  const [wecomWorkspaces, setWeComWorkspaces] = useState<Workspace[]>([])
  const [wecomLoading, setWeComLoading] = useState(false)
  const [wecomOperationError, setWeComOperationError] = useState<string | null>(null)
  const [wecomBotSecretDraft, setWeComBotSecretDraft] = useState('')
  const [wecomAdvancedOpen, setWeComAdvancedOpen] = useState(false)
  const [wecomSaving, setWeComSaving] = useState(false)
  const [wecomTesting, setWeComTesting] = useState(false)
  const [wecomEnabling, setWeComEnabling] = useState(false)
  const [wecomDisabling, setWeComDisabling] = useState(false)
  const loginSubscriptionRef = useRef<null | (() => void)>(null)

  const closeWeChatLoginStream = () => {
    loginSubscriptionRef.current?.()
    loginSubscriptionRef.current = null
  }

  const resetWeChatLoginState = () => {
    setWeChatLoginPhase('idle')
    setWeChatQrTicket('')
    setWeChatQrPageUrl('')
  }

  const loadWeChatData = async () => {
    const [configResult, statusResult, workspaceResult] = await Promise.allSettled([
      fetchWeChatConfig(),
      fetchWeChatStatus(),
      fetchWorkspaces(),
    ])

    const failures = [configResult, statusResult, workspaceResult].filter(
      (result): result is PromiseRejectedResult => result.status === 'rejected'
    )
    if (failures.length > 0) {
      throw failures[0].reason
    }

    if (
      configResult.status !== 'fulfilled' ||
      statusResult.status !== 'fulfilled' ||
      workspaceResult.status !== 'fulfilled'
    ) {
      throw new Error(t('settings.wechat.load_failed'))
    }

    const nextConfig = configResult.value
    const nextStatus = statusResult.value
    const nextWorkspaces = workspaceResult.value.workspaces.filter(isLocalWorkspace)

    setWeChatConfig(nextConfig)
    setWeChatStatus(nextStatus)
    setWeChatWorkspaces(nextWorkspaces)
    setWeChatBotTokenDraft('')
    return { config: nextConfig, status: nextStatus }
  }

  const loadWeComData = async () => {
    const [configResult, statusResult, workspaceResult] = await Promise.allSettled([
      fetchWeComConfig(),
      fetchWeComStatus(),
      fetchWorkspaces(),
    ])

    const failures = [configResult, statusResult, workspaceResult].filter(
      (result): result is PromiseRejectedResult => result.status === 'rejected'
    )
    if (failures.length > 0) {
      throw failures[0].reason
    }

    if (
      configResult.status !== 'fulfilled' ||
      statusResult.status !== 'fulfilled' ||
      workspaceResult.status !== 'fulfilled'
    ) {
      throw new Error(t('settings.wecom.load_failed'))
    }

    const nextConfig = configResult.value
    const nextStatus = statusResult.value
    const nextWorkspaces = workspaceResult.value.workspaces.filter(isLocalWorkspace)

    setWeComConfig(nextConfig)
    setWeComStatus(nextStatus)
    setWeComWorkspaces(nextWorkspaces)
    setWeComBotSecretDraft('')
    return { config: nextConfig, status: nextStatus }
  }

  useEffect(() => {
    if (!open) return

    setEnvDrafts(
      Object.fromEntries(
        agents.map((agent) => [
          agent.id,
          Object.entries(agent.env || {}).map(([key, value]) => ({ key, value })),
        ])
      )
    )
  }, [agents, open])

  useEffect(() => {
    if (!open) {
      closeWeChatLoginStream()
      resetWeChatLoginState()
      setWeChatAdvancedOpen(false)
      setWeChatOperationError(null)
      setWeComAdvancedOpen(false)
      setWeComOperationError(null)
      return
    }

    let cancelled = false
    setWeChatLoading(true)
    setWeChatOperationError(null)
    setWeComLoading(true)
    setWeComOperationError(null)

    void Promise.allSettled([loadWeChatData(), loadWeComData()]).then((results) => {
      if (cancelled) return
      if (results[0].status === 'rejected') {
        setWeChatOperationError(getErrorMessage(results[0].reason, t('settings.wechat.load_failed')))
      }
      if (results[1].status === 'rejected') {
        setWeComOperationError(getErrorMessage(results[1].reason, t('settings.wecom.load_failed')))
      }
      setWeChatLoading(false)
      setWeComLoading(false)
    })

    return () => {
      cancelled = true
    }
  }, [open, t])

  useEffect(() => () => {
    closeWeChatLoginStream()
  }, [])

  const refreshWeChatData = async () => {
    setWeChatOperationError(null)
    await loadWeChatData()
  }

  const refreshWeComData = async () => {
    setWeComOperationError(null)
    await loadWeComData()
  }

  const handleWeChatConfigChange = <K extends keyof WeChatConfig>(
    key: K,
    value: WeChatConfig[K]
  ) => {
    setWeChatConfig((current) => (current ? { ...current, [key]: value } : current))
  }

  const handleWeComConfigChange = <K extends keyof WeComConfig>(
    key: K,
    value: WeComConfig[K]
  ) => {
    setWeComConfig((current) => (current ? { ...current, [key]: value } : current))
  }

  const persistWeChatConfig = async (botToken?: string) => {
    if (!wechatConfig) return

    const input: SaveWeChatConfigInput = {
      enabled: wechatConfig.enabled,
      loginMode: wechatConfig.loginMode,
      accountId: wechatConfig.accountId,
      baseUrl: wechatConfig.baseUrl,
      workspaceId: wechatConfig.workspaceId,
      agentId: wechatConfig.agentId,
    }
    if (botToken !== undefined) {
      input.botToken = botToken
    } else if (wechatBotTokenDraft !== '') {
      input.botToken = wechatBotTokenDraft
    }

    await saveWeChatConfig(input)
    await refreshWeChatData()
  }

  const persistWeComConfig = async (botSecret?: string) => {
    if (!wecomConfig) return

    const input: SaveWeComConfigInput = {
      enabled: wecomConfig.enabled,
      mode: wecomConfig.mode,
      botId: wecomConfig.botId,
      workspaceId: wecomConfig.workspaceId,
      agentId: wecomConfig.agentId,
      allowFrom: wecomConfig.allowFrom,
      requesterConfigPath: wecomConfig.requesterConfigPath,
      connectTimeoutMs: wecomConfig.connectTimeoutMs,
      heartbeatIntervalMs: wecomConfig.heartbeatIntervalMs,
      messageAckTimeoutMs: wecomConfig.messageAckTimeoutMs,
    }
    if (botSecret !== undefined) {
      input.botSecret = botSecret
    } else if (wecomBotSecretDraft !== '') {
      input.botSecret = wecomBotSecretDraft
    }

    await saveWeComConfig(input)
    await refreshWeComData()
  }

  const handleWeChatSave = async (botToken?: string) => {
    setWeChatSaving(true)
    setWeChatOperationError(null)
    try {
      await persistWeChatConfig(botToken)
    } catch (saveError) {
      setWeChatOperationError(getErrorMessage(saveError, t('settings.wechat.save_failed')))
    } finally {
      setWeChatSaving(false)
    }
  }

  const handleWeChatTest = async () => {
    setWeChatTesting(true)
    setWeChatOperationError(null)

    try {
      const result = await testWeChatConnection()
      if (!result.success) {
        setWeChatOperationError(result.error)
      }
    } catch (testError) {
      setWeChatOperationError(getErrorMessage(testError, t('settings.wechat.test_failed')))
    } finally {
      setWeChatTesting(false)
    }
  }

  const handleWeChatEnable = async () => {
    setWeChatEnabling(true)
    setWeChatOperationError(null)

    try {
      await persistWeChatConfig()
      await enableWeChat()
      await refreshWeChatData()
    } catch (enableError) {
      setWeChatOperationError(getErrorMessage(enableError, t('settings.wechat.enable_failed')))
    } finally {
      setWeChatEnabling(false)
    }
  }

  const handleWeChatDisable = async () => {
    setWeChatDisabling(true)
    setWeChatOperationError(null)

    try {
      await disableWeChat()
      await refreshWeChatData()
    } catch (disableError) {
      setWeChatOperationError(getErrorMessage(disableError, t('settings.wechat.disable_failed')))
    } finally {
      setWeChatDisabling(false)
    }
  }

  const handleWeComSave = async (botSecret?: string) => {
    setWeComSaving(true)
    setWeComOperationError(null)
    try {
      await persistWeComConfig(botSecret)
    } catch (saveError) {
      setWeComOperationError(getErrorMessage(saveError, t('settings.wecom.save_failed')))
    } finally {
      setWeComSaving(false)
    }
  }

  const handleWeComTest = async () => {
    setWeComTesting(true)
    setWeComOperationError(null)

    try {
      const result = await testWeComConnection()
      if (!result.success) {
        setWeComOperationError(result.error)
      }
    } catch (testError) {
      setWeComOperationError(getErrorMessage(testError, t('settings.wecom.test_failed')))
    } finally {
      setWeComTesting(false)
    }
  }

  const handleWeComEnable = async () => {
    setWeComEnabling(true)
    setWeComOperationError(null)

    try {
      await persistWeComConfig()
      await enableWeCom()
      await refreshWeComData()
    } catch (enableError) {
      setWeComOperationError(getErrorMessage(enableError, t('settings.wecom.enable_failed')))
    } finally {
      setWeComEnabling(false)
    }
  }

  const handleWeComDisable = async () => {
    setWeComDisabling(true)
    setWeComOperationError(null)

    try {
      await disableWeCom()
      await refreshWeComData()
    } catch (disableError) {
      setWeComOperationError(getErrorMessage(disableError, t('settings.wecom.disable_failed')))
    } finally {
      setWeComDisabling(false)
    }
  }

  const handleWeChatLoginStart = async () => {
    closeWeChatLoginStream()
    setWeChatLoginStarting(true)
    setWeChatOperationError(null)
    setWeChatLoginPhase('waiting')
    setWeChatQrTicket('')
    setWeChatQrPageUrl('')

    try {
      const { loginId } = await startWeChatLogin()
      loginSubscriptionRef.current = subscribeWeChatLogin(loginId, {
        onQR: (event) => {
          setWeChatLoginPhase('waiting')
          setWeChatQrTicket(event.ticket)
          setWeChatQrPageUrl(event.imageUrl)
        },
        onScanned: () => {
          setWeChatLoginPhase('scanned')
        },
        onConfirmed: (event) => {
          setWeChatLoginPhase('confirmed')
          setWeChatConfig((current) =>
            current
              ? {
                  ...current,
                  accountId: event.accountId,
                  baseUrl: event.baseUrl,
                  hasToken: event.hasToken,
                  loginMode: 'qr',
                }
              : current
          )
        },
        onExpired: () => {
          setWeChatLoginPhase('expired')
          closeWeChatLoginStream()
        },
        onError: (event) => {
          setWeChatLoginPhase('error')
          setWeChatOperationError(event.message)
          closeWeChatLoginStream()
        },
        onDone: () => {
          closeWeChatLoginStream()
          void refreshWeChatData().catch((refreshError) => {
            setWeChatOperationError(getErrorMessage(refreshError, t('settings.wechat.load_failed')))
          })
        },
      })
    } catch (loginError) {
      setWeChatLoginPhase('error')
      setWeChatOperationError(getErrorMessage(loginError, t('settings.wechat.login_failed')))
    } finally {
      setWeChatLoginStarting(false)
    }
  }

  const wechatErrorMessage =
    wechatOperationError || wechatStatus?.configError || wechatStatus?.lastError || null
  const wechatRunning = Boolean(wechatStatus?.running)
  const wechatConfigured = Boolean(wechatStatus?.configured)
  const canSaveWeChat = Boolean(wechatConfig) && !wechatSaving
  const canTestWeChat = wechatConfigured && Boolean(wechatConfig?.hasToken) && !wechatTesting
  const canEnableWeChat = wechatConfigured && !wechatRunning && !wechatEnabling
  const canDisableWeChat = wechatRunning && !wechatDisabling
  const savedTokenText = wechatConfig?.hasToken
    ? wechatConfig.maskedToken || t('settings.wechat.token_saved')
    : t('settings.wechat.token_missing')
  const loginStatusLabel = t(`settings.wechat.login_status.${wechatLoginPhase}`)
  const wecomErrorMessage =
    wecomOperationError || wecomStatus?.configError || wecomStatus?.lastError || null
  const wecomRunning = Boolean(wecomStatus?.running)
  const wecomConfigured = Boolean(wecomStatus?.configured)
  const canSaveWeCom = Boolean(wecomConfig) && !wecomSaving
  const canTestWeCom = wecomConfigured && Boolean(wecomConfig?.hasSecret) && !wecomTesting
  const canEnableWeCom = wecomConfigured && !wecomRunning && !wecomEnabling
  const canDisableWeCom = wecomRunning && !wecomDisabling
  const savedWeComSecretText = wecomConfig?.hasSecret
    ? wecomConfig.maskedSecret || t('settings.wecom.secret_saved')
    : t('settings.wecom.secret_missing')

  return (
    <Dialog onOpenChange={onOpenChange} open={open}>
      <DialogContent className="max-h-[85vh] overflow-hidden border-border bg-card p-0 shadow-floating">
        <div className="border-b border-border bg-accent px-6 py-4">
          <DialogHeader>
            <DialogTitle>{t('settings.title')}</DialogTitle>
          </DialogHeader>
        </div>

        <div className="legacy-hidden-scrollbar max-h-[calc(85vh-72px)] overflow-y-auto px-6 py-6">
          {error ? (
            <div className="mb-4 rounded-sm border border-destructive bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </div>
          ) : null}

          <section className="mb-8">
            <h3 className="mb-3 text-[12px] font-bold uppercase tracking-[0.1em] text-muted-foreground">
              {t('settings.agents')}
            </h3>
            <p className="mb-4 text-sm text-[rgb(var(--color-text-secondary))]">
              {t('settings.agents.desc')} Default: <code>{defaultAgent}</code>
            </p>

            <div className="space-y-4">
              {agents.map((agent) => {
                const currentMode = agent.sessionMode || 'default'
                const activeMode = (agent.availableModes || []).find((mode) => mode.value === currentMode) || agent.availableModes?.[0]
                const envEntries = envDrafts[agent.id] || []

                return (
                  <div className="rounded-md border border-border bg-accent px-4 py-4" key={agent.id}>
                    <div className="mb-4 flex items-center gap-2">
                      <span className={cn('rounded-sm px-2 py-1 text-sm font-bold', getAgentNameClass(agent.id))}>
                        {agent.name}
                      </span>
                      {agent.id === defaultAgent ? (
                        <span className="rounded-sm border border-border px-2 py-1 text-[11px] uppercase tracking-[0.08em] text-muted-foreground">
                          {t('settings.default')}
                        </span>
                      ) : null}
                    </div>

                    <div className="space-y-3 text-sm">
                      <div className="grid gap-2 md:grid-cols-[110px_1fr]">
                        <div className="text-[rgb(var(--color-text-secondary))]">ID:</div>
                        <code>{agent.id}</code>
                      </div>

                      <div className="grid gap-2 md:grid-cols-[110px_1fr]">
                        <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.mode')}:</div>
                        <div className="space-y-2">
                          <div className="flex flex-wrap gap-2">
                            {(agent.availableModes || []).map((mode) => (
                              <button
                                className={cn(
                                  'rounded-md border px-3 py-1.5 text-[13px] font-medium transition',
                                  getModeButtonClass(currentMode === mode.value)
                                )}
                                key={`${agent.id}-${mode.value}`}
                                onClick={async () => {
                                  if (currentMode === mode.value) return
                                  const result = await onUpdateAgentMode(agent.id, mode.value)
                                  if (!result.success) {
                                    setError(result.error || 'Failed to update agent')
                                  } else {
                                    setError(null)
                                  }
                                }}
                                type="button"
                              >
                                {mode.label}
                              </button>
                            ))}
                          </div>
                          {activeMode?.description ? (
                            <div className="text-[12px] leading-5 text-[rgb(var(--color-text-secondary))]">
                              {activeMode.description}
                            </div>
                          ) : null}
                        </div>
                      </div>

                      <div className="grid gap-2 md:grid-cols-[110px_1fr]">
                        <div className="text-[rgb(var(--color-text-secondary))]">Command:</div>
                        <code className="break-all">{formatCommand(agent)}</code>
                      </div>

                      <div className="grid gap-2 md:grid-cols-[110px_1fr]">
                        <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.env')}:</div>
                        <div>
                          <button
                            className="flex items-center gap-2 rounded-md border border-border px-3 py-1.5 text-[13px] text-[rgb(var(--color-text-secondary))] transition hover:bg-background hover:text-foreground"
                            onClick={() => setEditingEnv((current) => (current === agent.id ? null : agent.id))}
                            type="button"
                          >
                            <span>{Object.keys(agent.env || {}).length} variables</span>
                            <span className="text-[10px]">{editingEnv === agent.id ? '▲' : '▼'}</span>
                          </button>
                        </div>
                      </div>
                    </div>

                    {editingEnv === agent.id ? (
                      <div className="mt-4 rounded-sm border border-border bg-card p-3">
                        <div className="space-y-3">
                          {envEntries.map((item, index) => (
                            <div className="grid gap-3 md:grid-cols-[1fr_auto_1fr_auto]" key={`${agent.id}-${index}`}>
                              <Input
                                className="h-9 rounded-sm border-border bg-background"
                                onChange={(event) =>
                                  setEnvDrafts((current) => ({
                                    ...current,
                                    [agent.id]: (current[agent.id] || []).map((entry, entryIndex) =>
                                      entryIndex === index ? { ...entry, key: event.target.value } : entry
                                    ),
                                  }))
                                }
                                placeholder="KEY"
                                value={item.key}
                              />
                              <div className="self-center text-muted-foreground">=</div>
                              <Input
                                className="h-9 rounded-sm border-border bg-background"
                                onChange={(event) =>
                                  setEnvDrafts((current) => ({
                                    ...current,
                                    [agent.id]: (current[agent.id] || []).map((entry, entryIndex) =>
                                      entryIndex === index ? { ...entry, value: event.target.value } : entry
                                    ),
                                  }))
                                }
                                placeholder="value"
                                value={item.value}
                              />
                              <button
                                className="flex h-9 w-9 items-center justify-center rounded-sm text-muted-foreground transition hover:bg-accent hover:text-foreground"
                                onClick={() =>
                                  setEnvDrafts((current) => ({
                                    ...current,
                                    [agent.id]: (current[agent.id] || []).filter((_, entryIndex) => entryIndex !== index),
                                  }))
                                }
                                type="button"
                              >
                                ×
                              </button>
                            </div>
                          ))}
                        </div>

                        <div className="mt-3 flex flex-wrap justify-end gap-3">
                          <Button
                            className="rounded-md"
                            onClick={() =>
                              setEnvDrafts((current) => ({
                                ...current,
                                [agent.id]: [...(current[agent.id] || []), { key: '', value: '' }],
                              }))
                            }
                            type="button"
                            variant="outline"
                          >
                            + Add
                          </Button>
                          <Button
                            className="rounded-md"
                            onClick={async () => {
                              const nextEnv: Record<string, string> = {}
                              ;(envDrafts[agent.id] || []).forEach((entry) => {
                                if (entry.key.trim()) {
                                  nextEnv[entry.key.trim()] = entry.value
                                }
                              })
                              const result = await onUpdateAgentEnv(agent.id, nextEnv)
                              if (!result.success) {
                                setError(result.error || 'Failed to save env')
                              } else {
                                setError(null)
                                setEditingEnv(null)
                              }
                            }}
                            type="button"
                          >
                            {t('settings.save')}
                          </Button>
                        </div>
                      </div>
                    ) : null}
                  </div>
                )
              })}
            </div>

            <div className="mt-4 rounded-md border border-border bg-accent px-4 py-4 text-sm">
              <div className="mb-3 text-[11px] font-bold uppercase tracking-[0.1em] text-muted-foreground">
                {t('settings.mode.note.title')}
              </div>
              <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.mode.note.body')}</div>
            </div>
          </section>

          <section className="mb-8">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
              <h3 className="text-[12px] font-bold uppercase tracking-[0.1em] text-muted-foreground">
                {t('settings.wechat.title')}
              </h3>
              <span
                className={cn(
                  'rounded-sm border px-2 py-1 text-[11px] uppercase tracking-[0.08em]',
                  wechatRunning
                    ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                    : 'border-border bg-muted text-muted-foreground'
                )}
              >
                {wechatRunning ? t('settings.wechat.running') : t('settings.wechat.stopped')}
              </span>
            </div>

            {wechatErrorMessage ? (
              <div className="mb-4 rounded-sm border border-destructive bg-destructive/10 px-3 py-2 text-sm text-destructive">
                {wechatErrorMessage}
              </div>
            ) : null}

            <div className="space-y-4">
              <div className="rounded-md border border-border bg-accent px-4 py-4">
                <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <div className="text-sm font-medium text-foreground">{t('settings.wechat.status')}</div>
                    <div className="mt-1 text-xs text-[rgb(var(--color-text-secondary))]">
                      {t('settings.wechat.config_label')}:{' '}
                      {wechatTesting
                        ? t('settings.wechat.testing')
                        : wechatConfigured
                          ? t('settings.wechat.config_ok')
                          : t('settings.wechat.config_error')}
                    </div>
                  </div>
                  {wechatLoading ? (
                    <span className="text-xs text-muted-foreground">{t('settings.wechat.loading')}</span>
                  ) : null}
                </div>

                <div className="grid gap-2 text-sm md:grid-cols-[140px_1fr]">
                  <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.running_label')}</div>
                  <div>{wechatRunning ? t('settings.wechat.yes') : t('settings.wechat.no')}</div>
                  <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.last_sync')}</div>
                  <div>{formatChannelTimestamp(wechatStatus?.lastSyncAt)}</div>
                  <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.last_login')}</div>
                  <div>{formatChannelTimestamp(wechatStatus?.lastLoginAt)}</div>
                  <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.last_message')}</div>
                  <div>{formatChannelTimestamp(wechatStatus?.lastMessageAt)}</div>
                  <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.saved_token')}</div>
                  <div>{savedTokenText}</div>
                </div>

                <div className="mt-4 flex flex-wrap gap-3">
                  <Button className="rounded-md" disabled={!canSaveWeChat} onClick={() => void handleWeChatSave()} type="button">
                    {wechatSaving ? t('settings.saving') : t('settings.save')}
                  </Button>
                  <Button
                    className="rounded-md"
                    disabled={!canTestWeChat}
                    onClick={() => void handleWeChatTest()}
                    type="button"
                    variant="outline"
                  >
                    {wechatTesting ? t('settings.wechat.testing') : t('settings.wechat.test')}
                  </Button>
                  <Button
                    className="rounded-md"
                    disabled={!canEnableWeChat}
                    onClick={() => void handleWeChatEnable()}
                    type="button"
                    variant="outline"
                  >
                    {wechatEnabling ? t('settings.wechat.enabling') : t('settings.wechat.enable')}
                  </Button>
                  <Button
                    className="rounded-md"
                    disabled={!canDisableWeChat}
                    onClick={() => void handleWeChatDisable()}
                    type="button"
                    variant="outline"
                  >
                    {wechatDisabling ? t('settings.wechat.disabling') : t('settings.wechat.disable')}
                  </Button>
                </div>
              </div>

              <div className="rounded-md border border-border bg-accent px-4 py-4">
                <div className="mb-4 flex items-center justify-between gap-3">
                  <div className="text-sm font-medium text-foreground">{t('settings.wechat.login')}</div>
                  <div className="text-xs text-[rgb(var(--color-text-secondary))]">
                    {t('settings.wechat.saved_token')}: {savedTokenText}
                  </div>
                </div>

                <div className="space-y-4">
                  <div className="flex flex-wrap gap-4 text-sm">
                    <label className="flex items-center gap-2">
                      <input
                        checked={wechatConfig?.loginMode === 'qr'}
                        name="wechat-login-mode"
                        onChange={() => handleWeChatConfigChange('loginMode', 'qr')}
                        type="radio"
                      />
                      <span>{t('settings.wechat.login_mode.qr')}</span>
                    </label>
                    <label className="flex items-center gap-2">
                      <input
                        checked={wechatConfig?.loginMode === 'manual'}
                        name="wechat-login-mode"
                        onChange={() => handleWeChatConfigChange('loginMode', 'manual')}
                        type="radio"
                      />
                      <span>{t('settings.wechat.login_mode.manual')}</span>
                    </label>
                  </div>

                  <div className="flex flex-wrap gap-3">
                    <Button
                      className="rounded-md"
                      disabled={!wechatConfig || wechatLoginStarting}
                      onClick={() => void handleWeChatLoginStart()}
                      type="button"
                      variant="outline"
                    >
                      {wechatLoginStarting ? t('settings.wechat.login_starting') : t('settings.wechat.start_qr_login')}
                    </Button>
                  </div>

                  <div className="grid gap-2 text-sm md:grid-cols-[140px_1fr]">
                    <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.qr_status')}</div>
                    <div>{loginStatusLabel}</div>
                    <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.ticket')}</div>
                    <div>{wechatQrTicket || '-'}</div>
                  </div>

                  {wechatQrPageUrl ? (
                    <div className="rounded-sm border border-border bg-card p-3 text-sm">
                      <div className="flex justify-center rounded-sm bg-white p-4" data-testid="wechat-qr-code">
                        <QRCodeSVG
                          bgColor="#ffffff"
                          fgColor="#111111"
                          includeMargin
                          level="M"
                          size={192}
                          value={wechatQrPageUrl}
                        />
                      </div>
                      <div className="mt-3 text-[rgb(var(--color-text-secondary))]">
                        {t('settings.wechat.qr_inline_hint')}
                      </div>
                      <div className="mt-3 flex flex-wrap gap-3">
                        <a
                          className="inline-flex items-center rounded-md border border-border px-3 py-2 text-[13px] text-[rgb(var(--color-text-secondary))] transition hover:bg-background hover:text-foreground"
                          href={wechatQrPageUrl}
                          rel="noreferrer"
                          target="_blank"
                        >
                          {t('settings.wechat.open_qr_page')}
                        </a>
                      </div>
                    </div>
                  ) : null}
                </div>
              </div>

              <div className="rounded-md border border-border bg-accent px-4 py-4">
                <div className="mb-4 text-sm font-medium text-foreground">{t('settings.wechat.binding')}</div>
                <div className="grid gap-4 md:grid-cols-2">
                  <label className="space-y-2 text-sm">
                    <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.workspace')}</span>
                    <select
                      className="h-10 w-full rounded-md border border-border bg-background px-3 text-foreground outline-none transition focus:border-primary"
                      disabled={!wechatConfig}
                      onChange={(event) => handleWeChatConfigChange('workspaceId', event.target.value)}
                      value={wechatConfig?.workspaceId || ''}
                    >
                      <option value="">{t('settings.wechat.workspace_placeholder')}</option>
                      {wechatWorkspaces.map((workspace) => (
                        <option key={workspace.id} value={workspace.id}>
                          {workspace.name}
                        </option>
                      ))}
                    </select>
                  </label>

                  <label className="space-y-2 text-sm">
                    <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.agent')}</span>
                    <select
                      className="h-10 w-full rounded-md border border-border bg-background px-3 text-foreground outline-none transition focus:border-primary"
                      disabled={!wechatConfig}
                      onChange={(event) => handleWeChatConfigChange('agentId', event.target.value)}
                      value={wechatConfig?.agentId || ''}
                    >
                      <option value="">{t('settings.wechat.agent_placeholder')}</option>
                      {agents.map((agent) => (
                        <option key={agent.id} value={agent.id}>
                          {agent.name}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
              </div>

              <div className="rounded-md border border-border bg-accent px-4 py-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="text-sm font-medium text-foreground">{t('settings.wechat.advanced')}</div>
                  <button
                    className="rounded-md border border-border px-3 py-1.5 text-[13px] text-[rgb(var(--color-text-secondary))] transition hover:bg-background hover:text-foreground"
                    onClick={() => setWeChatAdvancedOpen((current) => !current)}
                    type="button"
                  >
                    {wechatAdvancedOpen ? t('settings.wechat.hide') : t('settings.wechat.show')}
                  </button>
                </div>

                {wechatAdvancedOpen ? (
                  <div className="mt-4 space-y-4">
                    <label className="space-y-2 text-sm">
                      <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.account_id')}</span>
                      <Input
                        className="h-10 rounded-sm border-border bg-background"
                        disabled={!wechatConfig}
                        onChange={(event) => handleWeChatConfigChange('accountId', event.target.value)}
                        value={wechatConfig?.accountId || ''}
                      />
                    </label>

                    <label className="space-y-2 text-sm">
                      <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.bot_token')}</span>
                      <Input
                        className="h-10 rounded-sm border-border bg-background"
                        disabled={!wechatConfig}
                        onChange={(event) => setWeChatBotTokenDraft(event.target.value)}
                        placeholder={t('settings.wechat.bot_token_placeholder')}
                        value={wechatBotTokenDraft}
                      />
                      <div className="text-xs text-[rgb(var(--color-text-secondary))]">
                        {t('settings.wechat.saved_token')}: {savedTokenText}
                      </div>
                    </label>

                    <label className="space-y-2 text-sm">
                      <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.base_url')}</span>
                      <Input
                        className="h-10 rounded-sm border-border bg-background"
                        disabled={!wechatConfig}
                        onChange={(event) => handleWeChatConfigChange('baseUrl', event.target.value)}
                        value={wechatConfig?.baseUrl || ''}
                      />
                    </label>

                    <label className="space-y-2 text-sm">
                      <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wechat.login_mode')}</span>
                      <select
                        className="h-10 w-full rounded-md border border-border bg-background px-3 text-foreground outline-none transition focus:border-primary"
                        disabled={!wechatConfig}
                        onChange={(event) =>
                          handleWeChatConfigChange('loginMode', event.target.value as WeChatConfig['loginMode'])
                        }
                        value={wechatConfig?.loginMode || 'qr'}
                      >
                        <option value="qr">{t('settings.wechat.login_mode.qr')}</option>
                        <option value="manual">{t('settings.wechat.login_mode.manual')}</option>
                      </select>
                    </label>

                    <div>
                      <Button
                        className="rounded-md"
                        disabled={!wechatConfig?.hasToken || wechatSaving}
                        onClick={() => void handleWeChatSave('')}
                        type="button"
                        variant="outline"
                      >
                        {t('settings.wechat.clear_saved_token')}
                      </Button>
                    </div>
                  </div>
                ) : null}
              </div>
            </div>
          </section>

          <section className="mb-8">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
              <h3 className="text-[12px] font-bold uppercase tracking-[0.1em] text-muted-foreground">
                {t('settings.wecom.title')}
              </h3>
              <span
                className={cn(
                  'rounded-sm border px-2 py-1 text-[11px] uppercase tracking-[0.08em]',
                  wecomRunning
                    ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                    : 'border-border bg-muted text-muted-foreground'
                )}
              >
                {wecomRunning ? t('settings.wecom.running') : t('settings.wecom.stopped')}
              </span>
            </div>

            {wecomErrorMessage ? (
              <div className="mb-4 rounded-sm border border-destructive bg-destructive/10 px-3 py-2 text-sm text-destructive">
                {wecomErrorMessage}
              </div>
            ) : null}

            <div className="space-y-4">
              <div className="rounded-md border border-border bg-accent px-4 py-4">
                <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <div className="text-sm font-medium text-foreground">{t('settings.wecom.status')}</div>
                    <div className="mt-1 text-xs text-[rgb(var(--color-text-secondary))]">
                      {t('settings.wecom.config_label')}:{' '}
                      {wecomTesting
                        ? t('settings.wecom.testing')
                        : wecomConfigured
                          ? t('settings.wecom.config_ok')
                          : t('settings.wecom.config_error')}
                    </div>
                  </div>
                  {wecomLoading ? (
                    <span className="text-xs text-muted-foreground">{t('settings.wecom.loading')}</span>
                  ) : null}
                </div>

                <div className="grid gap-2 text-sm md:grid-cols-[160px_1fr]">
                  <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.running_label')}</div>
                  <div>{wecomRunning ? t('settings.wecom.yes') : t('settings.wecom.no')}</div>
                  <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.last_connected')}</div>
                  <div>{formatChannelTimestamp(wecomStatus?.lastConnectedAt)}</div>
                  <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.last_message')}</div>
                  <div>{formatChannelTimestamp(wecomStatus?.lastMessageAt)}</div>
                  <div className="text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.saved_secret')}</div>
                  <div>{savedWeComSecretText}</div>
                </div>

                <div className="mt-4 flex flex-wrap gap-3">
                  <Button className="rounded-md" disabled={!canSaveWeCom} onClick={() => void handleWeComSave()} type="button">
                    {wecomSaving ? t('settings.saving') : t('settings.save')}
                  </Button>
                  <Button
                    className="rounded-md"
                    disabled={!canTestWeCom}
                    onClick={() => void handleWeComTest()}
                    type="button"
                    variant="outline"
                  >
                    {wecomTesting ? t('settings.wecom.testing') : t('settings.wecom.test')}
                  </Button>
                  <Button
                    className="rounded-md"
                    disabled={!canEnableWeCom}
                    onClick={() => void handleWeComEnable()}
                    type="button"
                    variant="outline"
                  >
                    {wecomEnabling ? t('settings.wecom.enabling') : t('settings.wecom.enable')}
                  </Button>
                  <Button
                    className="rounded-md"
                    disabled={!canDisableWeCom}
                    onClick={() => void handleWeComDisable()}
                    type="button"
                    variant="outline"
                  >
                    {wecomDisabling ? t('settings.wecom.disabling') : t('settings.wecom.disable')}
                  </Button>
                </div>
              </div>

              <div className="rounded-md border border-border bg-accent px-4 py-4">
                <div className="mb-4 text-sm font-medium text-foreground">{t('settings.wecom.binding')}</div>
                <div className="grid gap-4 md:grid-cols-2">
                  <label className="space-y-2 text-sm">
                    <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.workspace')}</span>
                    <select
                      className="h-10 w-full rounded-md border border-border bg-background px-3 text-foreground outline-none transition focus:border-primary"
                      disabled={!wecomConfig}
                      onChange={(event) => handleWeComConfigChange('workspaceId', event.target.value)}
                      value={wecomConfig?.workspaceId || ''}
                    >
                      <option value="">{t('settings.wecom.workspace_placeholder')}</option>
                      {wecomWorkspaces.map((workspace) => (
                        <option key={workspace.id} value={workspace.id}>
                          {workspace.name}
                        </option>
                      ))}
                    </select>
                  </label>

                  <label className="space-y-2 text-sm">
                    <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.agent')}</span>
                    <select
                      className="h-10 w-full rounded-md border border-border bg-background px-3 text-foreground outline-none transition focus:border-primary"
                      disabled={!wecomConfig}
                      onChange={(event) => handleWeComConfigChange('agentId', event.target.value)}
                      value={wecomConfig?.agentId || ''}
                    >
                      <option value="">{t('settings.wecom.agent_placeholder')}</option>
                      {agents.map((agent) => (
                        <option key={agent.id} value={agent.id}>
                          {agent.name}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
              </div>

              <div className="rounded-md border border-border bg-accent px-4 py-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="text-sm font-medium text-foreground">{t('settings.wecom.advanced')}</div>
                  <button
                    className="rounded-md border border-border px-3 py-1.5 text-[13px] text-[rgb(var(--color-text-secondary))] transition hover:bg-background hover:text-foreground"
                    onClick={() => setWeComAdvancedOpen((current) => !current)}
                    type="button"
                  >
                    {wecomAdvancedOpen ? t('settings.wecom.hide') : t('settings.wecom.show')}
                  </button>
                </div>

                {wecomAdvancedOpen ? (
                  <div className="mt-4 space-y-4">
                    <label className="space-y-2 text-sm">
                      <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.bot_id')}</span>
                      <Input
                        className="h-10 rounded-sm border-border bg-background"
                        disabled={!wecomConfig}
                        onChange={(event) => handleWeComConfigChange('botId', event.target.value)}
                        value={wecomConfig?.botId || ''}
                      />
                    </label>

                    <label className="space-y-2 text-sm">
                      <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.bot_secret')}</span>
                      <Input
                        className="h-10 rounded-sm border-border bg-background"
                        disabled={!wecomConfig}
                        onChange={(event) => setWeComBotSecretDraft(event.target.value)}
                        placeholder={t('settings.wecom.bot_secret_placeholder')}
                        value={wecomBotSecretDraft}
                      />
                      <div className="text-xs text-[rgb(var(--color-text-secondary))]">
                        {t('settings.wecom.saved_secret')}: {savedWeComSecretText}
                      </div>
                    </label>

                    <label className="space-y-2 text-sm">
                      <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.allow_from')}</span>
                      <Input
                        className="h-10 rounded-sm border-border bg-background"
                        disabled={!wecomConfig}
                        onChange={(event) => handleWeComConfigChange('allowFrom', event.target.value)}
                        value={wecomConfig?.allowFrom || ''}
                      />
                    </label>

                    <div className="grid gap-4 md:grid-cols-3">
                      <label className="space-y-2 text-sm">
                        <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.connect_timeout')}</span>
                        <Input
                          className="h-10 rounded-sm border-border bg-background"
                          disabled={!wecomConfig}
                          onChange={(event) => handleWeComConfigChange('connectTimeoutMs', Number(event.target.value) || 0)}
                          type="number"
                          value={wecomConfig?.connectTimeoutMs || 0}
                        />
                      </label>

                      <label className="space-y-2 text-sm">
                        <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.heartbeat_interval')}</span>
                        <Input
                          className="h-10 rounded-sm border-border bg-background"
                          disabled={!wecomConfig}
                          onChange={(event) => handleWeComConfigChange('heartbeatIntervalMs', Number(event.target.value) || 0)}
                          type="number"
                          value={wecomConfig?.heartbeatIntervalMs || 0}
                        />
                      </label>

                      <label className="space-y-2 text-sm">
                        <span className="block text-[rgb(var(--color-text-secondary))]">{t('settings.wecom.ack_timeout')}</span>
                        <Input
                          className="h-10 rounded-sm border-border bg-background"
                          disabled={!wecomConfig}
                          onChange={(event) => handleWeComConfigChange('messageAckTimeoutMs', Number(event.target.value) || 0)}
                          type="number"
                          value={wecomConfig?.messageAckTimeoutMs || 0}
                        />
                      </label>
                    </div>

                    <div>
                      <Button
                        className="rounded-md"
                        disabled={!wecomConfig?.hasSecret || wecomSaving}
                        onClick={() => void handleWeComSave('')}
                        type="button"
                        variant="outline"
                      >
                        {t('settings.wecom.clear_saved_secret')}
                      </Button>
                    </div>
                  </div>
                ) : null}
              </div>
            </div>
          </section>

          <section>
            <h3 className="mb-3 text-[12px] font-bold uppercase tracking-[0.1em] text-muted-foreground">
              {t('settings.appearance')}
            </h3>

            <div className="rounded-md border border-border bg-accent px-4 py-4">
              <div className="flex flex-wrap items-center justify-between gap-3 py-2">
                <span className="text-sm text-foreground">{t('settings.theme')}</span>
                <Button className="rounded-md" onClick={toggleTheme} type="button" variant="outline">
                  {currentTheme === 'dark' ? t('settings.theme.dark') : t('settings.theme.light')}
                </Button>
              </div>

              <div className="mt-2 flex flex-wrap items-center justify-between gap-3 py-2">
                <span className="text-sm text-foreground">{t('settings.language')}</span>
                <div className="flex flex-wrap gap-2">
                  <button
                    className={cn(
                      'rounded-md border px-3 py-1.5 text-[13px] font-medium transition',
                      currentLang === 'en'
                        ? 'border-transparent bg-primary text-primary-foreground'
                        : 'border-border bg-transparent text-[rgb(var(--color-text-secondary))] hover:bg-background hover:text-foreground'
                    )}
                    onClick={() => setLang('en')}
                    type="button"
                  >
                    English
                  </button>
                  <button
                    className={cn(
                      'rounded-md border px-3 py-1.5 text-[13px] font-medium transition',
                      currentLang === 'zh'
                        ? 'border-transparent bg-primary text-primary-foreground'
                        : 'border-border bg-transparent text-[rgb(var(--color-text-secondary))] hover:bg-background hover:text-foreground'
                    )}
                    onClick={() => setLang('zh')}
                    type="button"
                  >
                    中文
                  </button>
                </div>
              </div>
            </div>
          </section>
        </div>
      </DialogContent>
    </Dialog>
  )
}
