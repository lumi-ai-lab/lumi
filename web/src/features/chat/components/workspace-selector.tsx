'use client'

import { CheckCircle2, ChevronDown, Copy, ExternalLink, LoaderCircle, PanelLeftClose, Plus, RefreshCw } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'

import { Button } from '@/components/ui/button'
import { SandboxWorkspaceAlert } from '@/components/sandbox-workspace-alert'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import * as api from '@/lib/api'
import type { CreateWorkspaceOptions, SandboxRuntimeState } from '@/lib/api'
import {
  getSandboxErrorDisplay,
  getWorkspaceStatusBadgeMeta,
  isRemoteWorkspace,
  isSandboxWorkspace,
} from '@/lib/sandbox'
import type { Agent, DependencyItem, DeviceDTO, Workspace } from '@/lib/types'
import { isUrl } from '@/lib/utils'

const DEVICE_POLL_INTERVAL_MS = 2500
const SANDBOX_PREFLIGHT_DEBOUNCE_MS = 350
const DEFAULT_SANDBOX_IMAGE = 'lumi/sandbox:latest'
const DEFAULT_SANDBOX_IDLE_TIMEOUT_SEC = 1800

type WorkspaceMode = 'local' | 'remote' | 'sandbox'
type RemoteWizardStep = 1 | 2 | 3

function isReadyDevice(device: DeviceDTO) {
  return device.status === 'online' && device.setupReady
}

function getRegisteredDevices(devices: DeviceDTO[]) {
  return devices.filter((device) => device.status !== 'offline')
}

function getWorkspaceSubtitle(workspace: Workspace, deviceLookup: Map<string, DeviceDTO>) {
  if (!isRemoteWorkspace(workspace)) {
    return workspace.path
  }

  const remotePath = workspace.remotePath || workspace.path
  const device = workspace.deviceId ? deviceLookup.get(workspace.deviceId) : null
  const deviceName = device?.displayName || workspace.deviceName || 'Unknown device'
  return `${deviceName} · ${remotePath}`
}

function getRemoteWorkspaceStatus(workspace: Workspace, deviceLookup: Map<string, DeviceDTO>) {
  if (!isRemoteWorkspace(workspace)) {
    return null
  }

  if (workspace.deviceId && deviceLookup.has(workspace.deviceId)) {
    return deviceLookup.get(workspace.deviceId)?.status || null
  }

  return workspace.deviceStatus || null
}

type SetupIssueCategory = {
  id: 'environment' | 'agents' | 'acpPackages'
  title: string
  detailKey: 'command' | 'package'
  items: DependencyItem[]
}

function getSetupIssueCategories(setupStatus?: DeviceDTO['setupStatus'] | null): SetupIssueCategory[] {
  if (!setupStatus) return []

  return [
    {
      id: 'environment' as const,
      title: 'Environment',
      detailKey: 'command' as const,
      items: setupStatus.environment || [],
    },
    {
      id: 'agents' as const,
      title: 'Agent CLI',
      detailKey: 'command' as const,
      items: setupStatus.agents || [],
    },
    {
      id: 'acpPackages' as const,
      title: 'ACP Packages',
      detailKey: 'package' as const,
      items: setupStatus.acpPackages || [],
    },
  ].map((category) => ({
    ...category,
    items: category.items.filter((item) => item.status !== 'ready'),
  }))
}

function getSetupIssueCount(categories: SetupIssueCategory[]) {
  return categories.reduce((total, category) => total + category.items.length, 0)
}

function getSetupDetail(item: DependencyItem, detailKey: 'command' | 'package') {
  return detailKey === 'command' ? item.command : item.package
}

function getDefaultSandboxAgentIds(agents: Agent[]) {
  return agents.map((agent) => agent.id)
}

function WorkspaceStatusBadge({
  deviceLookup,
  workspace,
}: {
  deviceLookup: Map<string, DeviceDTO>
  workspace: Workspace
}) {
  const statusMeta = getWorkspaceStatusBadgeMeta(
    workspace,
    getRemoteWorkspaceStatus(workspace, deviceLookup),
  )
  if (!statusMeta) return null

  return (
    <span
      className={`rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.08em] ${statusMeta.tone}`}
    >
      {statusMeta.label}
    </span>
  )
}

function DeviceStatusBadge({ status }: { status?: DeviceDTO['status'] | null }) {
  if (!status) return null

  const tone = getWorkspaceStatusBadgeMeta(
    {
      id: 'remote-status',
      name: '',
      path: '',
      kind: 'remote',
      deviceStatus: status,
    },
    status,
  )?.tone

  return (
    <span
      className={`rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.08em] ${tone || 'bg-zinc-500/15 text-zinc-300'}`}
    >
      {status.replace('_', ' ')}
    </span>
  )
}

export function WorkspaceSelector({
  agents,
  currentWorkspaceId,
  onAddWorkspace,
  onCollapse,
  onSelectWorkspace,
  onPollSandboxReady,
  onWarmupSandbox,
  workspaces,
}: {
  agents: Agent[]
  currentWorkspaceId: string
  onAddWorkspace: (
    name: string,
    path: string,
    options?: CreateWorkspaceOptions,
  ) => Promise<{ workspace: Workspace | null; error?: string }>
  onCollapse: () => void
  onSelectWorkspace: (workspaceId: string) => void
  onPollSandboxReady: (workspaceId: string) => Promise<SandboxRuntimeState>
  onWarmupSandbox: (workspaceId: string) => Promise<SandboxRuntimeState>
  workspaces: Workspace[]
}) {
  const [isOpen, setIsOpen] = useState(false)
  const [showAddDialog, setShowAddDialog] = useState(false)
  const [workspaceMode, setWorkspaceMode] = useState<WorkspaceMode>('local')
  const [localName, setLocalName] = useState('')
  const [localPath, setLocalPath] = useState('')
  const [localError, setLocalError] = useState('')
  const [devices, setDevices] = useState<DeviceDTO[]>([])
  const [isLoadingDevices, setIsLoadingDevices] = useState(false)
  const [pairingCommand, setPairingCommand] = useState('')
  const [isLoadingPairingCommand, setIsLoadingPairingCommand] = useState(false)
  const [copyState, setCopyState] = useState<'idle' | 'copied'>('idle')
  const [setupInstallCopyKey, setSetupInstallCopyKey] = useState('')
  const [remoteStep, setRemoteStep] = useState<RemoteWizardStep>(1)
  const [selectedDeviceId, setSelectedDeviceId] = useState('')
  const [remoteName, setRemoteName] = useState('')
  const [remotePath, setRemotePath] = useState('')
  const [remoteError, setRemoteError] = useState('')
  const [setupCheckMessage, setSetupCheckMessage] = useState('')
  const [isRequestingSetupCheck, setIsRequestingSetupCheck] = useState(false)
  const [isAddingWorkspace, setIsAddingWorkspace] = useState(false)
  const [sandboxName, setSandboxName] = useState('')
  const [sandboxPath, setSandboxPath] = useState('')
  const [sandboxImage, setSandboxImage] = useState(DEFAULT_SANDBOX_IMAGE)
  const [sandboxIdleTimeoutSec, setSandboxIdleTimeoutSec] = useState(
    String(DEFAULT_SANDBOX_IDLE_TIMEOUT_SEC),
  )
  const [sandboxSelectedAgentIds, setSandboxSelectedAgentIds] = useState<string[]>(
    getDefaultSandboxAgentIds(agents),
  )
  const [showSandboxAdvanced, setShowSandboxAdvanced] = useState(false)
  const [sandboxError, setSandboxError] = useState('')
  const [sandboxPathError, setSandboxPathError] = useState('')
  const [sandboxPreflightWarning, setSandboxPreflightWarning] = useState('')
  const [sandboxPreflightCode, setSandboxPreflightCode] = useState('')
  const [isSandboxPreflightLoading, setIsSandboxPreflightLoading] = useState(false)
  const [sandboxPreflightReady, setSandboxPreflightReady] = useState(false)
  const [sandboxCreationWorkspace, setSandboxCreationWorkspace] = useState<Workspace | null>(null)
  const [sandboxCreationState, setSandboxCreationState] = useState<SandboxRuntimeState | null>(null)
  const isSandboxCreating = Boolean(sandboxCreationWorkspace)

  const currentWorkspace =
    workspaces.find((workspace) => workspace.id === currentWorkspaceId) || null

  const deviceLookup = useMemo(
    () => new Map(devices.map((device) => [device.id, device])),
    [devices],
  )
  const localWorkspaces = useMemo(
    () => workspaces.filter((workspace) => !workspace.kind || workspace.kind === 'local'),
    [workspaces],
  )
  const remoteWorkspaces = useMemo(
    () => workspaces.filter((workspace) => isRemoteWorkspace(workspace)),
    [workspaces],
  )
  const sandboxWorkspaces = useMemo(
    () => workspaces.filter((workspace) => isSandboxWorkspace(workspace)),
    [workspaces],
  )
  const registeredDevices = useMemo(() => getRegisteredDevices(devices), [devices])
  const readyDevices = useMemo(() => devices.filter(isReadyDevice), [devices])
  const selectedDevice =
    devices.find((device) => device.id === selectedDeviceId) ||
    registeredDevices[0] ||
    readyDevices[0] ||
    null
  const selectedSetupIssueCategories = getSetupIssueCategories(selectedDevice?.setupStatus)
  const selectedDeviceIssueCount = getSetupIssueCount(selectedSetupIssueCategories)

  const resetDialogState = () => {
    setWorkspaceMode('local')
    setLocalName('')
    setLocalPath('')
    setLocalError('')
    setDevices([])
    setIsLoadingDevices(false)
    setPairingCommand('')
    setIsLoadingPairingCommand(false)
    setCopyState('idle')
    setSetupInstallCopyKey('')
    setRemoteStep(1)
    setSelectedDeviceId('')
    setRemoteName('')
    setRemotePath('')
    setRemoteError('')
    setSetupCheckMessage('')
    setIsRequestingSetupCheck(false)
    setIsAddingWorkspace(false)
    setSandboxName('')
    setSandboxPath('')
    setSandboxImage(DEFAULT_SANDBOX_IMAGE)
    setSandboxIdleTimeoutSec(String(DEFAULT_SANDBOX_IDLE_TIMEOUT_SEC))
    setSandboxSelectedAgentIds(getDefaultSandboxAgentIds(agents))
    setShowSandboxAdvanced(false)
    setSandboxError('')
    setSandboxPathError('')
    setSandboxPreflightWarning('')
    setSandboxPreflightCode('')
    setIsSandboxPreflightLoading(false)
    setSandboxPreflightReady(false)
  }

  const copySetupInstallHint = async (value: string, key: string) => {
    if (!navigator.clipboard) return

    await navigator.clipboard.writeText(value)
    setSetupInstallCopyKey(key)
    window.setTimeout(() => {
      setSetupInstallCopyKey((current) => (current === key ? '' : current))
    }, 1200)
  }

  const loadDevices = async (silent = false) => {
    if (!silent) {
      setIsLoadingDevices(true)
    }

    try {
      const nextDevices = await api.fetchDevices()
      setDevices(nextDevices)
      setRemoteError('')
    } catch (error) {
      if (!silent) {
        setRemoteError(error instanceof Error ? error.message : 'Failed to load devices')
      }
    } finally {
      if (!silent) {
        setIsLoadingDevices(false)
      }
    }
  }

  const loadPairingCommand = async () => {
    setIsLoadingPairingCommand(true)
    try {
      const response = await api.fetchDevicePairingCommand()
      setPairingCommand(response.command)
      setRemoteError('')
    } catch (error) {
      setRemoteError(error instanceof Error ? error.message : 'Failed to load pairing command')
    } finally {
      setIsLoadingPairingCommand(false)
    }
  }

  const runSandboxPreflight = async (
    input: {
      path?: string
      image?: string
    } = {},
  ) => {
    setIsSandboxPreflightLoading(true)

    try {
      const result = await api.preflightSandboxWorkspace({
        path: input.path,
        image: input.image,
        checkImagePull: false,
      })
      const display = getSandboxErrorDisplay(result.code)

      setSandboxPreflightReady(result.code === 'ready')
      setSandboxPreflightCode(result.code)
      setSandboxPathError(
        result.code === 'path_invalid'
          ? result.message || display.description
          : '',
      )
      setSandboxPreflightWarning(
        result.code !== 'ready' && result.code !== 'path_invalid'
          ? result.message || display.description
          : '',
      )
      setSandboxError('')
      return result
    } catch (error) {
      setSandboxPreflightReady(false)
      setSandboxPreflightCode('unknown')
      setSandboxPathError('')
      setSandboxPreflightWarning('')
      setSandboxError(
        error instanceof Error
          ? error.message
          : 'Failed to run sandbox preflight',
      )
      return null
    } finally {
      setIsSandboxPreflightLoading(false)
    }
  }

  useEffect(() => {
    const handleClick = (event: MouseEvent) => {
      const target = event.target as HTMLElement
      if (!target.closest('[data-workspace-selector]')) {
        setIsOpen(false)
      }
    }

    document.addEventListener('click', handleClick)
    return () => document.removeEventListener('click', handleClick)
  }, [])

  useEffect(() => {
    if (!workspaces.some((workspace) => isRemoteWorkspace(workspace))) {
      return
    }

    void loadDevices(true)
  }, [workspaces])

  useEffect(() => {
    if (!isOpen || !workspaces.some((workspace) => isRemoteWorkspace(workspace))) {
      return
    }

    void loadDevices(true)
  }, [isOpen, workspaces])

  useEffect(() => {
    if (!showAddDialog || workspaceMode !== 'remote') {
      return
    }

    void loadPairingCommand()
    void loadDevices()

    const intervalId = window.setInterval(() => {
      void loadDevices(true)
    }, DEVICE_POLL_INTERVAL_MS)

    return () => window.clearInterval(intervalId)
  }, [showAddDialog, workspaceMode])

  useEffect(() => {
    if (!showAddDialog || workspaceMode !== 'remote') {
      return
    }

    if (!registeredDevices.length) {
      setSelectedDeviceId('')
      return
    }

    setSelectedDeviceId((current) => {
      if (registeredDevices.some((device) => device.id === current)) {
        return current
      }
      return registeredDevices[0]?.id || ''
    })
  }, [registeredDevices, showAddDialog, workspaceMode])

  useEffect(() => {
    if (workspaceMode !== 'remote' || remoteStep !== 3 || !readyDevices.length) {
      return
    }

    setSelectedDeviceId((current) => {
      if (readyDevices.some((device) => device.id === current)) {
        return current
      }
      return readyDevices[0]?.id || ''
    })
  }, [readyDevices, remoteStep, workspaceMode])

  useEffect(() => {
    setSandboxSelectedAgentIds((current) => {
      const availableAgentIds = new Set(agents.map((agent) => agent.id))
      const next = current.filter((agentId) => availableAgentIds.has(agentId))

      if (next.length > 0) {
        return next
      }

      return getDefaultSandboxAgentIds(agents)
    })
  }, [agents])

  useEffect(() => {
    if (!showAddDialog || workspaceMode !== 'sandbox') {
      return
    }

    const timeoutId = window.setTimeout(() => {
      void runSandboxPreflight({
        path: sandboxPath.trim() || undefined,
        image: sandboxImage.trim() || undefined,
      })
    }, SANDBOX_PREFLIGHT_DEBOUNCE_MS)

    return () => window.clearTimeout(timeoutId)
  }, [sandboxImage, sandboxPath, showAddDialog, workspaceMode])

  return (
    <div className="relative border-b border-border px-[14px] pb-2 pt-4" data-workspace-selector>
      <div className="mb-2 flex items-center justify-between">
        <div className="text-[10px] font-bold uppercase tracking-[0.1em] text-muted-foreground">
          Workspace
        </div>
        <div className="flex items-center gap-1">
          <button
            className="flex h-6 w-6 items-center justify-center rounded-sm bg-transparent text-muted-foreground transition hover:bg-muted hover:text-foreground"
            onClick={() => {
              resetDialogState()
              setShowAddDialog(true)
              setIsOpen(false)
            }}
            title="Add Workspace"
            type="button"
          >
            <Plus className="h-3.5 w-3.5" />
          </button>
          <button
            className="flex h-6 w-6 items-center justify-center rounded-sm bg-transparent text-muted-foreground transition hover:bg-muted hover:text-foreground"
            onClick={onCollapse}
            title="Hide sidebar"
            type="button"
          >
            <PanelLeftClose className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>

      <button
        className="w-full rounded-md border border-border bg-background px-[10px] py-2 text-left transition hover:border-[rgb(var(--color-text-secondary))] hover:bg-accent"
        onClick={() => setIsOpen((current) => !current)}
        type="button"
      >
        <div className="flex items-center justify-between gap-3">
          <span className="truncate text-[13px] font-semibold text-foreground">
            {currentWorkspace?.name || 'Select Workspace'}
          </span>
          <div className="flex items-center gap-2">
            {currentWorkspace ? (
              <WorkspaceStatusBadge
                deviceLookup={deviceLookup}
                workspace={currentWorkspace}
              />
            ) : null}
            <ChevronDown className={`h-3.5 w-3.5 text-muted-foreground transition ${isOpen ? 'rotate-180' : ''}`} />
          </div>
        </div>
        {currentWorkspace ? (
          <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">
            {getWorkspaceSubtitle(currentWorkspace, deviceLookup)}
          </div>
        ) : null}
      </button>

      {isOpen ? (
        <div className="absolute left-3 right-3 top-full z-20 mt-1.5 max-h-[320px] overflow-y-auto rounded-md border border-border bg-card p-1 shadow-floating">
          {localWorkspaces.length ? (
            <div className="mb-1 px-2 pb-1 pt-2 text-[10px] font-bold uppercase tracking-[0.12em] text-muted-foreground">
              Local
            </div>
          ) : null}
          {localWorkspaces.map((workspace) => (
            <button
              className={`mb-0.5 w-full rounded-sm px-3 py-2 text-left transition ${
                workspace.id === currentWorkspaceId
                  ? 'border-l-2 border-l-foreground bg-accent'
                  : 'hover:bg-muted'
              }`}
              key={workspace.id}
              onClick={() => {
                onSelectWorkspace(workspace.id)
                setIsOpen(false)
              }}
              type="button"
            >
              <div className="text-[13px] font-medium text-foreground">{workspace.name}</div>
              <div className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">
                {workspace.path}
              </div>
            </button>
          ))}

          {remoteWorkspaces.length ? (
            <div className="mb-1 mt-2 px-2 pb-1 pt-2 text-[10px] font-bold uppercase tracking-[0.12em] text-muted-foreground">
              Remote
            </div>
          ) : null}
          {remoteWorkspaces.map((workspace) => (
            <button
              className={`mb-0.5 w-full rounded-sm px-3 py-2 text-left transition ${
                workspace.id === currentWorkspaceId
                  ? 'border-l-2 border-l-foreground bg-accent'
                  : 'hover:bg-muted'
              }`}
              key={workspace.id}
              onClick={() => {
                onSelectWorkspace(workspace.id)
                setIsOpen(false)
              }}
              type="button"
            >
              <div className="flex items-center justify-between gap-2">
                <div className="min-w-0 text-[13px] font-medium text-foreground">{workspace.name}</div>
                <WorkspaceStatusBadge deviceLookup={deviceLookup} workspace={workspace} />
              </div>
              <div className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">
                {getWorkspaceSubtitle(workspace, deviceLookup)}
              </div>
            </button>
          ))}

          {sandboxWorkspaces.length ? (
            <div className="mb-1 mt-2 px-2 pb-1 pt-2 text-[10px] font-bold uppercase tracking-[0.12em] text-muted-foreground">
              Sandbox
            </div>
          ) : null}
          {sandboxWorkspaces.map((workspace) => (
            <button
              className={`mb-0.5 w-full rounded-sm px-3 py-2 text-left transition ${
                workspace.id === currentWorkspaceId
                  ? 'border-l-2 border-l-foreground bg-accent'
                  : 'hover:bg-muted'
              }`}
              key={workspace.id}
              onClick={() => {
                onSelectWorkspace(workspace.id)
                setIsOpen(false)
              }}
              type="button"
            >
              <div className="flex items-center justify-between gap-2">
                <div className="min-w-0 text-[13px] font-medium text-foreground">{workspace.name}</div>
                <WorkspaceStatusBadge deviceLookup={deviceLookup} workspace={workspace} />
              </div>
              <div className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">
                {getWorkspaceSubtitle(workspace, deviceLookup)}
              </div>
            </button>
          ))}

          {!workspaces.length ? (
            <div className="px-3 py-4 text-center text-sm text-muted-foreground">
              No workspaces
            </div>
          ) : null}
        </div>
      ) : null}

      <Dialog
        onOpenChange={(open) => {
          setShowAddDialog(open)
          if (!open) {
            resetDialogState()
          }
        }}
        open={showAddDialog}
      >
        <DialogContent className="border-border bg-[rgb(var(--color-bg-muted))] p-6">
          <DialogHeader className="space-y-2">
            <DialogTitle>Add Workspace</DialogTitle>
            <DialogDescription>
              Add a local workspace, connect a remote device, or configure a sandbox runtime.
            </DialogDescription>
          </DialogHeader>

          <div className="mt-5 space-y-5">
            <div className="space-y-2">
              <div className="text-[11px] font-medium text-[rgb(var(--color-text-secondary))]">
                Workspace type
              </div>
              <div className="grid grid-cols-3 gap-2">
                <button
                  className={`rounded-md border px-3 py-2 text-sm transition ${
                    workspaceMode === 'local'
                      ? 'border-foreground bg-background text-foreground'
                      : 'border-border bg-background/40 text-muted-foreground hover:bg-background'
                  }`}
                  onClick={() => {
                    setWorkspaceMode('local')
                    setLocalError('')
                  }}
                  type="button"
                >
                  Local
                </button>
                <button
                  className={`rounded-md border px-3 py-2 text-sm transition ${
                    workspaceMode === 'remote'
                      ? 'border-foreground bg-background text-foreground'
                      : 'border-border bg-background/40 text-muted-foreground hover:bg-background'
                  }`}
                  onClick={() => {
                    setWorkspaceMode('remote')
                    setRemoteError('')
                    setSetupCheckMessage('')
                  }}
                  type="button"
                >
                  Remote Device
                </button>
                <button
                  className={`rounded-md border px-3 py-2 text-sm transition ${
                    workspaceMode === 'sandbox'
                      ? 'border-foreground bg-background text-foreground'
                      : 'border-border bg-background/40 text-muted-foreground hover:bg-background'
                  }`}
                  onClick={() => {
                    setWorkspaceMode('sandbox')
                    setSandboxError('')
                    setSandboxPathError('')
                    setSandboxPreflightWarning('')
                    setSandboxPreflightCode('')
                  }}
                  type="button"
                >
                  Sandbox
                </button>
              </div>
            </div>

            {workspaceMode === 'local' ? (
              <div className="space-y-4">
                <div className="space-y-2">
                  <label className="text-[11px] font-medium text-[rgb(var(--color-text-secondary))]">Name</label>
                  <Input
                    aria-label="Name"
                    className="h-9 rounded-sm border-border bg-background"
                    onChange={(event) => setLocalName(event.target.value)}
                    placeholder="My Project"
                    value={localName}
                  />
                </div>
                <div className="space-y-2">
                  <label className="text-[11px] font-medium text-[rgb(var(--color-text-secondary))]">Path</label>
                  <Input
                    aria-label="Path"
                    className="h-9 rounded-sm border-border bg-background"
                    onChange={(event) => setLocalPath(event.target.value)}
                    placeholder="/path/to/project"
                    value={localPath}
                  />
                </div>
                {localError ? (
                  <div className="rounded-sm bg-destructive/10 px-3 py-2 text-sm text-destructive">{localError}</div>
                ) : null}
                <div className="flex justify-end gap-3">
                  <Button className="rounded-md" onClick={() => setShowAddDialog(false)} type="button" variant="outline">
                    Cancel
                  </Button>
                  <Button
                    className="rounded-md"
                    onClick={async () => {
                      const trimmedName = localName.trim()
                      const trimmedPath = localPath.trim()

                      if (!trimmedName || !trimmedPath) {
                        setLocalError('Name and path are required')
                        return
                      }

                      setIsAddingWorkspace(true)
                      const result = await onAddWorkspace(trimmedName, trimmedPath)
                      setIsAddingWorkspace(false)

                      if (result.error) {
                        setLocalError(result.error)
                        return
                      }

                      setShowAddDialog(false)
                    }}
                    disabled={isAddingWorkspace}
                    type="button"
                  >
                    Add
                  </Button>
                </div>
              </div>
            ) : workspaceMode === 'remote' ? (
              <div className="space-y-4">
                <div className="rounded-md border border-border bg-background/50 px-3 py-2">
                  <div className="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                    Step {remoteStep} / 3
                  </div>
                  <div className="mt-1 text-sm font-medium text-foreground">
                    {remoteStep === 1
                      ? 'Connect Device'
                      : remoteStep === 2
                        ? 'Check Device Setup'
                        : 'Add Remote Workspace'}
                  </div>
                </div>

                {remoteStep === 1 ? (
                  <div className="space-y-4">
                    <div className="space-y-2">
                      <div className="text-sm font-medium text-foreground">Run this on the remote device</div>
                      <div className="rounded-md border border-border bg-background px-3 py-3">
                        {isLoadingPairingCommand ? (
                          <div className="flex items-center gap-2 text-sm text-muted-foreground">
                            <LoaderCircle className="h-4 w-4 animate-spin" />
                            Loading pairing command…
                          </div>
                        ) : (
                          <div className="space-y-3">
                            <pre className="overflow-x-auto whitespace-pre-wrap font-mono text-xs text-foreground">
                              {pairingCommand || 'Unable to load pairing command'}
                            </pre>
                            <div className="flex justify-end">
                              <Button
                                className="rounded-md"
                                onClick={async () => {
                                  if (!pairingCommand || !navigator.clipboard) return
                                  await navigator.clipboard.writeText(pairingCommand)
                                  setCopyState('copied')
                                  window.setTimeout(() => setCopyState('idle'), 1200)
                                }}
                                size="sm"
                                type="button"
                                variant="outline"
                              >
                                <Copy className="h-3.5 w-3.5" />
                                {copyState === 'copied' ? 'Copied' : 'Copy'}
                              </Button>
                            </div>
                          </div>
                        )}
                      </div>
                    </div>

                    <div className="rounded-md border border-border bg-background/50 px-3 py-3 text-sm">
                      <div className="font-medium text-foreground">Status</div>
                      <div className="mt-1 text-muted-foreground">
                        {registeredDevices.length
                          ? `Detected ${registeredDevices.length} device${registeredDevices.length > 1 ? 's' : ''}.`
                          : 'Waiting for device'}
                      </div>
                      {registeredDevices.length ? (
                        <div className="mt-3 space-y-2">
                          {registeredDevices.map((device) => (
                            <button
                              className={`flex w-full items-center justify-between rounded-md border px-3 py-2 text-left transition ${
                                device.id === selectedDeviceId
                                  ? 'border-foreground bg-background'
                                  : 'border-border bg-background/60 hover:bg-background'
                              }`}
                              key={device.id}
                              onClick={() => setSelectedDeviceId(device.id)}
                              type="button"
                            >
                              <div>
                                <div className="text-sm font-medium text-foreground">{device.displayName}</div>
                                <div className="font-mono text-[10px] text-muted-foreground">{device.id}</div>
                              </div>
                              <DeviceStatusBadge status={device.status} />
                            </button>
                          ))}
                        </div>
                      ) : null}
                    </div>

                    <div className="flex justify-end gap-3">
                      <Button className="rounded-md" onClick={() => setShowAddDialog(false)} type="button" variant="outline">
                        Cancel
                      </Button>
                      <Button
                        className="rounded-md"
                        disabled={!registeredDevices.length || isLoadingDevices || isLoadingPairingCommand}
                        onClick={() => {
                          setRemoteStep(2)
                          setRemoteError('')
                        }}
                        type="button"
                      >
                        Next
                      </Button>
                    </div>
                  </div>
                ) : null}

                {remoteStep === 2 ? (
                  <div className="space-y-4">
                    <div className="rounded-md border border-border bg-background px-4 py-4">
                      <div className="flex items-center justify-between gap-3">
                        <div>
                          <div className="text-sm font-medium text-foreground">
                            {selectedDevice?.displayName || 'No device selected'}
                          </div>
                          <div className="mt-1 font-mono text-[10px] text-muted-foreground">
                            {selectedDevice?.id || 'Select a detected device first'}
                          </div>
                        </div>
                        <DeviceStatusBadge status={selectedDevice?.status} />
                      </div>

                      {selectedDevice ? (
                        <div className="mt-4 space-y-3">
                          {selectedDevice.setupReady ? (
                            <div className="flex items-center gap-2 rounded-md bg-emerald-500/10 px-3 py-2 text-sm text-emerald-100">
                              <CheckCircle2 className="h-4 w-4" />
                              Device setup is ready.
                            </div>
                          ) : (
                            <div className="space-y-3">
                              {!selectedDevice.setupStatus ? (
                                <div className="rounded-md border border-border bg-background/60 px-3 py-3">
                                  <div className="text-sm font-medium text-foreground">
                                    Waiting for setup check results from the remote device.
                                  </div>
                                  <div className="mt-1 text-xs text-muted-foreground">
                                    Keep this device connected, then use Refresh or Recheck when the remote Mac finishes reporting.
                                  </div>
                                </div>
                              ) : selectedDeviceIssueCount ? (
                                <div className="space-y-3">
                                  <div className="rounded-md border border-rose-500/30 bg-rose-500/10 px-3 py-2">
                                    <div className="text-sm font-medium text-rose-100">Setup Required</div>
                                    <div className="mt-1 text-xs text-rose-100/80">
                                      Install or fix these items on the remote Mac, then click Recheck.
                                    </div>
                                  </div>

                                  {selectedSetupIssueCategories.map((category) =>
                                    category.items.length ? (
                                      <section className="space-y-2" key={category.id}>
                                        <div className="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                                          {category.title}
                                        </div>
                                        <div className="space-y-2">
                                          {category.items.map((item, itemIndex) => {
                                            const installHint = item.install || ''
                                            const installIsUrl = isUrl(installHint)
                                            const detail = getSetupDetail(item, category.detailKey)
                                            const itemKey = `${category.id}-${item.command || item.package || item.name}-${itemIndex}`

                                            return (
                                              <div
                                                className="rounded-md border border-border bg-background/60 px-3 py-3"
                                                key={itemKey}
                                              >
                                                <div className="flex items-start justify-between gap-3">
                                                  <div className="min-w-0">
                                                    <div className="text-sm font-medium text-foreground">{item.name}</div>
                                                    {detail ? (
                                                      <div className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">
                                                        {detail}
                                                      </div>
                                                    ) : null}
                                                  </div>
                                                  <span className="shrink-0 rounded-full bg-rose-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-rose-200">
                                                    {item.status.replace('_', ' ')}
                                                  </span>
                                                </div>

                                                {item.message ? (
                                                  <div className="mt-2 text-xs text-muted-foreground">{item.message}</div>
                                                ) : null}

                                                {installHint ? (
                                                  <div className="mt-3 rounded-md border border-border bg-background">
                                                    <div className="flex items-center justify-between gap-2 border-b border-border px-2 py-1.5">
                                                      <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                                                        {installIsUrl ? 'Install link' : 'Install command'}
                                                      </div>
                                                      <Button
                                                        aria-label={`Copy install hint for ${item.name}`}
                                                        className="h-7 rounded-sm px-2"
                                                        onClick={() => void copySetupInstallHint(installHint, itemKey)}
                                                        size="sm"
                                                        type="button"
                                                        variant="outline"
                                                      >
                                                        <Copy className="h-3.5 w-3.5" />
                                                        {setupInstallCopyKey === itemKey ? 'Copied' : 'Copy'}
                                                      </Button>
                                                    </div>
                                                    <div className="px-2 py-2">
                                                      {installIsUrl ? (
                                                        <a
                                                          className="inline-flex max-w-full items-center gap-1 text-[11px] font-medium text-emerald-300"
                                                          href={installHint}
                                                          rel="noreferrer"
                                                          target="_blank"
                                                        >
                                                          <span className="break-all">{installHint}</span>
                                                          <ExternalLink className="h-3 w-3 shrink-0" />
                                                        </a>
                                                      ) : (
                                                        <pre className="overflow-x-auto whitespace-pre-wrap break-all font-mono text-[11px] text-foreground">{installHint}</pre>
                                                      )}
                                                    </div>
                                                  </div>
                                                ) : null}
                                              </div>
                                            )
                                          })}
                                        </div>
                                      </section>
                                    ) : null,
                                  )}
                                </div>
                              ) : (
                                <div className="rounded-md border border-border bg-background/60 px-3 py-3">
                                  <div className="text-sm font-medium text-foreground">
                                    Setup is not ready, but no missing dependency details were reported.
                                  </div>
                                  <div className="mt-1 text-xs text-muted-foreground">
                                    Use Refresh or Recheck to request an updated setup result from the remote Mac.
                                  </div>
                                </div>
                              )}
                            </div>
                          )}
                        </div>
                      ) : null}
                    </div>

                    {setupCheckMessage ? (
                      <div className="rounded-sm bg-emerald-500/10 px-3 py-2 text-sm text-emerald-100">
                        {setupCheckMessage}
                      </div>
                    ) : null}
                    {remoteError ? (
                      <div className="rounded-sm bg-destructive/10 px-3 py-2 text-sm text-destructive">{remoteError}</div>
                    ) : null}

                    <div className="flex justify-between gap-3">
                      <Button
                        className="rounded-md"
                        onClick={async () => {
                          setRemoteError('')
                          setSetupCheckMessage('')
                          await loadDevices()
                        }}
                        type="button"
                        variant="outline"
                      >
                        <RefreshCw className={`h-3.5 w-3.5 ${isLoadingDevices ? 'animate-spin' : ''}`} />
                        Refresh
                      </Button>
                      <div className="flex gap-3">
                        <Button
                          className="rounded-md"
                          onClick={async () => {
                            if (!selectedDeviceId) return
                            setRemoteError('')
                            setSetupCheckMessage('')
                            setIsRequestingSetupCheck(true)
                            const result = await api.requestDeviceSetupCheck(selectedDeviceId)
                            setIsRequestingSetupCheck(false)

                            if (!result.success) {
                              setRemoteError(result.error)
                              return
                            }

                            setSetupCheckMessage(result.message || 'Setup check requested')
                            await loadDevices()
                          }}
                          disabled={!selectedDeviceId || isRequestingSetupCheck}
                          type="button"
                          variant="outline"
                        >
                          {isRequestingSetupCheck ? (
                            <LoaderCircle className="h-3.5 w-3.5 animate-spin" />
                          ) : null}
                          Recheck
                        </Button>
                        <Button
                          className="rounded-md"
                          onClick={() => {
                            setRemoteStep(1)
                            setRemoteError('')
                            setSetupCheckMessage('')
                          }}
                          type="button"
                          variant="outline"
                        >
                          Back
                        </Button>
                        <Button
                          className="rounded-md"
                          disabled={!selectedDevice || !selectedDevice.setupReady || selectedDevice.status !== 'online'}
                          onClick={() => {
                            setRemoteStep(3)
                            setRemoteError('')
                            setSetupCheckMessage('')
                          }}
                          type="button"
                        >
                          Next
                        </Button>
                      </div>
                    </div>
                  </div>
                ) : null}

                {remoteStep === 3 ? (
                  <div className="space-y-4">
                    <div className="space-y-2">
                      <label className="text-[11px] font-medium text-[rgb(var(--color-text-secondary))]">Device</label>
                      <select
                        aria-label="Device"
                        className="flex h-9 w-full rounded-sm border border-border bg-background px-3 text-sm text-foreground"
                        onChange={(event) => setSelectedDeviceId(event.target.value)}
                        value={selectedDeviceId}
                      >
                        {readyDevices.map((device) => (
                          <option key={device.id} value={device.id}>
                            {device.displayName}
                          </option>
                        ))}
                      </select>
                      {!readyDevices.length ? (
                        <div className="text-xs text-muted-foreground">
                          No online devices with a ready setup are available yet.
                        </div>
                      ) : null}
                    </div>
                    <div className="space-y-2">
                      <label className="text-[11px] font-medium text-[rgb(var(--color-text-secondary))]">
                        Workspace name
                      </label>
                      <Input
                        aria-label="Workspace name"
                        className="h-9 rounded-sm border-border bg-background"
                        onChange={(event) => setRemoteName(event.target.value)}
                        placeholder="Website"
                        value={remoteName}
                      />
                    </div>
                    <div className="space-y-2">
                      <label className="text-[11px] font-medium text-[rgb(var(--color-text-secondary))]">
                        Remote path
                      </label>
                      <Input
                        aria-label="Remote path"
                        className="h-9 rounded-sm border-border bg-background"
                        onChange={(event) => setRemotePath(event.target.value)}
                        placeholder="/Users/me/site"
                        value={remotePath}
                      />
                    </div>
                    {remoteError ? (
                      <div className="rounded-sm bg-destructive/10 px-3 py-2 text-sm text-destructive">{remoteError}</div>
                    ) : null}
                    <div className="flex justify-end gap-3">
                      <Button
                        className="rounded-md"
                        onClick={() => {
                          setRemoteStep(2)
                          setRemoteError('')
                        }}
                        type="button"
                        variant="outline"
                      >
                        Back
                      </Button>
                      <Button
                        className="rounded-md"
                        disabled={!readyDevices.length || isAddingWorkspace}
                        onClick={async () => {
                          const trimmedName = remoteName.trim()
                          const trimmedPath = remotePath.trim()
                          const device = readyDevices.find((item) => item.id === selectedDeviceId) || null

                          if (!device) {
                            setRemoteError('Select an online device with a ready setup')
                            return
                          }

                          if (!trimmedName || !trimmedPath) {
                            setRemoteError('Workspace name and remote path are required')
                            return
                          }

                          setIsAddingWorkspace(true)
                          const result = await onAddWorkspace(trimmedName, trimmedPath, {
                            kind: 'remote',
                            deviceId: device.id,
                            deviceName: device.displayName,
                            remotePath: trimmedPath,
                          })
                          setIsAddingWorkspace(false)

                          if (result.error) {
                            setRemoteError(result.error)
                            return
                          }

                          setShowAddDialog(false)
                        }}
                        type="button"
                      >
                        Add
                      </Button>
                    </div>
                  </div>
                ) : null}
              </div>
            ) : (
              <div className="space-y-4">
                {isSandboxCreating && sandboxCreationWorkspace ? (
                  <div className="space-y-3">
                    <SandboxWorkspaceAlert
                      compact={false}
                      workspace={{
                        ...sandboxCreationWorkspace,
                        sandboxStatus: sandboxCreationState?.status || 'pending',
                        sandboxStage: sandboxCreationState?.stage || 'checking_docker',
                        sandboxReady: sandboxCreationState?.status === 'running',
                        sandboxExpiresAt: sandboxCreationState?.expiresAt,
                        sandboxError: sandboxCreationState?.errorCode,
                      }}
                    />
                    {sandboxError ? (
                      <div className="rounded-sm bg-destructive/10 px-3 py-2 text-sm text-destructive">
                        {sandboxError}
                      </div>
                    ) : null}
                  </div>
                ) : null}
                <div className="space-y-2">
                  <label className="text-[11px] font-medium text-[rgb(var(--color-text-secondary))]">
                    Workspace name
                  </label>
                  <Input
                    aria-label="Sandbox workspace name"
                    className="h-9 rounded-sm border-border bg-background"
                    onChange={(event) => setSandboxName(event.target.value)}
                    placeholder="Sandbox App"
                    value={sandboxName}
                    disabled={isSandboxCreating}
                  />
                </div>
                <div className="space-y-2">
                  <label className="text-[11px] font-medium text-[rgb(var(--color-text-secondary))]">
                    Host path
                  </label>
                  <Input
                    aria-label="Sandbox host path"
                    className="h-9 rounded-sm border-border bg-background"
                    onChange={(event) => setSandboxPath(event.target.value)}
                    placeholder="/Users/me/project"
                    value={sandboxPath}
                    disabled={isSandboxCreating}
                  />
                  {sandboxPathError ? (
                    <div className="rounded-sm bg-destructive/10 px-3 py-2 text-sm text-destructive">
                      {sandboxPathError}
                    </div>
                  ) : null}
                </div>

                <button
                  className="flex w-full items-center justify-between rounded-md border border-border bg-background/50 px-3 py-2 text-left text-sm text-foreground transition hover:bg-background"
                  onClick={() => setShowSandboxAdvanced((current) => !current)}
                  disabled={isSandboxCreating}
                  type="button"
                >
                  <span>Advanced settings</span>
                  <ChevronDown
                    className={`h-4 w-4 text-muted-foreground transition ${
                      showSandboxAdvanced ? 'rotate-180' : ''
                    }`}
                  />
                </button>

                {showSandboxAdvanced ? (
                  <div className="space-y-4 rounded-md border border-border bg-background/50 px-3 py-3">
                    <div className="space-y-2">
                      <label className="text-[11px] font-medium text-[rgb(var(--color-text-secondary))]">
                        Image
                      </label>
                      <Input
                        aria-label="Sandbox image"
                        className="h-9 rounded-sm border-border bg-background"
                        onChange={(event) => setSandboxImage(event.target.value)}
                        placeholder={DEFAULT_SANDBOX_IMAGE}
                        value={sandboxImage}
                      />
                    </div>
                    <div className="space-y-2">
                      <label className="text-[11px] font-medium text-[rgb(var(--color-text-secondary))]">
                        Idle timeout (seconds)
                      </label>
                      <Input
                        aria-label="Sandbox idle timeout"
                        className="h-9 rounded-sm border-border bg-background"
                        inputMode="numeric"
                        onChange={(event) => setSandboxIdleTimeoutSec(event.target.value)}
                        placeholder={String(DEFAULT_SANDBOX_IDLE_TIMEOUT_SEC)}
                        value={sandboxIdleTimeoutSec}
                      />
                    </div>
                    <div className="space-y-2">
                      <div className="text-[11px] font-medium text-[rgb(var(--color-text-secondary))]">
                        Agents
                      </div>
                      <div className="grid gap-2 sm:grid-cols-2">
                        {agents.map((agent) => {
                          const checked = sandboxSelectedAgentIds.includes(agent.id)
                          return (
                            <label
                              className="flex items-center gap-2 rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground"
                              key={agent.id}
                            >
                              <input
                                checked={checked}
                                className="h-3.5 w-3.5 accent-primary"
                                onChange={() => {
                                  setSandboxSelectedAgentIds((current) =>
                                    current.includes(agent.id)
                                      ? current.filter((item) => item !== agent.id)
                                      : [...current, agent.id],
                                  )
                                }}
                                type="checkbox"
                              />
                              <span>{agent.name}</span>
                            </label>
                          )
                        })}
                      </div>
                    </div>
                  </div>
                ) : null}

                <div className="rounded-md border border-border bg-background/50 px-3 py-3 text-sm">
                  <div className="flex items-center justify-between gap-3">
                    <div className="font-medium text-foreground">Sandbox preflight</div>
                    {isSandboxPreflightLoading ? (
                      <div className="flex items-center gap-2 text-xs text-muted-foreground">
                        <LoaderCircle className="h-3.5 w-3.5 animate-spin" />
                        Checking
                      </div>
                    ) : sandboxPreflightReady ? (
                      <div className="flex items-center gap-2 text-xs text-emerald-200">
                        <CheckCircle2 className="h-3.5 w-3.5" />
                        Ready
                      </div>
                    ) : null}
                  </div>
                  {sandboxPreflightWarning ? (
                    <div className="mt-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-amber-100">
                      <div className="text-sm font-medium">
                        {getSandboxErrorDisplay(sandboxPreflightCode).title}
                      </div>
                      <div className="mt-1 text-xs text-amber-100/85">
                        {sandboxPreflightWarning}
                      </div>
                    </div>
                  ) : sandboxPreflightReady ? (
                    <div className="mt-2 text-xs text-muted-foreground">
                      Docker and sandbox settings look ready for startup.
                    </div>
                  ) : (
                    <div className="mt-2 text-xs text-muted-foreground">
                      Lumi checks Docker availability and the sandbox image before creation.
                    </div>
                  )}
                  {sandboxPreflightCode && sandboxPreflightCode !== 'ready' ? (
                    <div className="mt-2 font-mono text-[11px] uppercase tracking-[0.08em] text-muted-foreground">
                      {sandboxPreflightCode}
                    </div>
                  ) : null}
                </div>

                {sandboxError ? (
                  <div className="rounded-sm bg-destructive/10 px-3 py-2 text-sm text-destructive">
                    {sandboxError}
                  </div>
                ) : null}

                <div className="flex justify-between gap-3">
                  <Button
                    className="rounded-md"
                    disabled={isSandboxCreating}
                    onClick={() => {
                      void runSandboxPreflight({
                        path: sandboxPath.trim() || undefined,
                        image: sandboxImage.trim() || undefined,
                      })
                    }}
                    type="button"
                    variant="outline"
                  >
                    <RefreshCw className={`h-3.5 w-3.5 ${isSandboxPreflightLoading ? 'animate-spin' : ''}`} />
                    Recheck
                  </Button>
                  <div className="flex gap-3">
                    <Button
                      className="rounded-md"
                      disabled={isSandboxCreating}
                      onClick={() => setShowAddDialog(false)}
                      type="button"
                      variant="outline"
                    >
                      Cancel
                    </Button>
                    <Button
                      className="rounded-md"
                      disabled={isAddingWorkspace || isSandboxPreflightLoading || Boolean(sandboxPathError)}
                      onClick={async () => {
                        const trimmedName = sandboxName.trim()
                        const trimmedPath = sandboxPath.trim()
                        const trimmedImage = sandboxImage.trim() || DEFAULT_SANDBOX_IMAGE
                        const parsedIdleTimeout = Number.parseInt(sandboxIdleTimeoutSec, 10)
                        const idleTimeoutSec =
                          Number.isFinite(parsedIdleTimeout) && parsedIdleTimeout > 0
                            ? parsedIdleTimeout
                            : DEFAULT_SANDBOX_IDLE_TIMEOUT_SEC

                        if (!trimmedName || !trimmedPath) {
                          setSandboxError('Workspace name and host path are required')
                          return
                        }

                        setIsAddingWorkspace(true)
                        const result = await onAddWorkspace(trimmedName, trimmedPath, {
                          kind: 'sandbox',
                          image: trimmedImage,
                          idleTimeoutSec,
                          agents: sandboxSelectedAgentIds,
                        })

                        if (result.error || !result.workspace) {
                          setIsAddingWorkspace(false)
                          setSandboxError(result.error || 'Failed to create workspace')
                          return
                        }

                        setSandboxCreationWorkspace(result.workspace)
                        setSandboxError('')
                        try {
                          const initialState = await onWarmupSandbox(result.workspace.id)
                          setSandboxCreationState(initialState)
                          const readyState = initialState.status === 'running'
                            ? initialState
                            : await onPollSandboxReady(result.workspace.id)
                          setSandboxCreationState(readyState)
                          setShowAddDialog(false)
                        } catch (error) {
                          setSandboxError(error instanceof Error ? error.message : 'Sandbox initialization failed')
                        } finally {
                          setIsAddingWorkspace(false)
                          setSandboxCreationWorkspace(null)
                        }
                      }}
                      type="button"
                    >
                      Add
                    </Button>
                  </div>
                </div>
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
