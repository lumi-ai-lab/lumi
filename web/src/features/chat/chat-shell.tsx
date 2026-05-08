'use client'

import { PanelLeftOpen } from 'lucide-react'
import { startTransition } from 'react'
import { useRouter } from 'next/navigation'
import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { ChatPanel } from '@/features/chat/components/chat-panel'
import { Sidebar } from '@/features/chat/components/sidebar'
import { useChatController } from '@/features/chat/use-chat-controller'
import { CronClockButton } from '@/features/cron/cron-clock-button'
import { ScheduledTasksPage } from '@/features/cron/scheduled-tasks-page'
import { SettingsButton } from '@/features/settings/settings-button'
import { ShareButton } from '@/features/share/share-button'
import { WorkspacePreviewPane } from '@/features/workspace-preview'
import type { Message } from '@/lib/types'

function getRouteSessionId(pathname: string) {
  return pathname.startsWith('/c/') ? pathname.replace('/c/', '') : null
}

function getScheduledJobId(pathname: string) {
  if (pathname === '/scheduled') return null
  return pathname.startsWith('/scheduled/') ? pathname.replace('/scheduled/', '') : undefined
}

function canShareConversation(messages: Message[]) {
  const hasUserMessage = messages.some(
    (message) =>
      message.role === 'user' &&
      (Boolean(message.content.trim()) || Boolean(message.files?.length))
  )
  const hasAssistantReply = messages.some(
    (message) =>
      message.role === 'assistant' &&
      !message.isError &&
      (Boolean(message.content.trim()) || Boolean(message.toolCall))
  )

  return hasUserMessage && hasAssistantReply
}

export function ChatShell() {
  const router = useRouter()
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [setupReady, setSetupReady] = useState<boolean | null>(null)
  const [routeSessionId, setRouteSessionId] = useState<string | null | undefined>(undefined)
  const [scheduledJobId, setScheduledJobId] = useState<string | null | undefined>(undefined)

  useEffect(() => {
    const syncFromLocation = () => {
      setRouteSessionId(getRouteSessionId(window.location.pathname))
      setScheduledJobId(getScheduledJobId(window.location.pathname))
    }

    syncFromLocation()
    window.addEventListener('popstate', syncFromLocation)
    return () => window.removeEventListener('popstate', syncFromLocation)
  }, [])

  useEffect(() => {
    let cancelled = false

    void fetch('/api/setup/status')
      .then((response) => response.json() as Promise<{ ready?: boolean }>)
      .then((data) => {
        if (cancelled) return
        if (!data.ready) {
          setSetupReady(false)
          router.replace('/setup')
          return
        }

        setSetupReady(true)
      })
      .catch(() => {
        if (!cancelled) {
          setSetupReady(true)
        }
      })

    return () => {
      cancelled = true
    }
  }, [router])

  const controller = useChatController({
    routeSessionId,
    pushRoute: (sessionId) => {
      const nextPath = sessionId ? `/c/${sessionId}` : '/c'
      if (window.location.pathname === nextPath) {
        setRouteSessionId(sessionId)
        return
      }

      startTransition(() => {
        window.history.pushState(null, '', nextPath)
        setRouteSessionId(sessionId)
        setScheduledJobId(undefined)
      })
    },
  })

  const navigatePath = (path: string) => {
    if (window.location.pathname === path) return
    startTransition(() => {
      window.history.pushState(null, '', path)
      setRouteSessionId(getRouteSessionId(path))
      setScheduledJobId(getScheduledJobId(path))
    })
  }

  useEffect(() => {
    if (scheduledJobId === undefined) return
    void controller.refreshCronJobs()
    // Refresh only when entering/changing the scheduled route; the controller object is intentionally not a dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scheduledJobId])

  if (setupReady === false) {
    return null
  }

  if (setupReady === null) {
    return (
      <div className="flex h-screen w-screen items-center justify-center bg-background text-sm text-muted-foreground">
        Loading workspace…
      </div>
    )
  }

  if (routeSessionId === undefined) {
    return null
  }

  const showShareButton = Boolean(
    controller.currentSessionId && canShareConversation(controller.currentMessages)
  )

  return (
    <div className="flex h-screen w-screen overflow-hidden bg-background">
      {!sidebarCollapsed ? (
        <Sidebar
          agents={controller.agents}
          currentSessionId={controller.currentSessionId}
          cronJobs={controller.cronJobs}
          currentWorkspaceId={controller.currentWorkspace}
          onAddWorkspace={controller.addWorkspace}
          onCollapse={() => setSidebarCollapsed(true)}
          onCreateSession={async () => {
            await controller.createNewSession()
          }}
          onRemoveSession={controller.removeSession}
          onSelectSession={controller.selectSession}
          onSetWorkspace={controller.setWorkspace}
          onShowScheduled={() => navigatePath('/scheduled')}
          sessions={controller.filteredSessions}
          workspaces={controller.workspaces}
        />
      ) : (
        <div className="fixed left-4 top-4 z-40">
          <Button
            className="h-8 w-8 rounded-md border-border bg-card text-[rgb(var(--color-text-secondary))] hover:bg-accent hover:text-foreground"
            onClick={() => setSidebarCollapsed(false)}
            size="icon"
            title="Show sidebar"
            type="button"
            variant="outline"
          >
            <PanelLeftOpen className="h-4 w-4" />
          </Button>
        </div>
      )}

      <main className="flex min-w-0 flex-1 overflow-hidden bg-background">
        <div className="relative flex min-w-0 flex-1 flex-col overflow-hidden bg-background">
          {scheduledJobId !== undefined ? (
            <ScheduledTasksPage
              agents={controller.agents}
              currentSessionId={controller.currentSessionId}
              currentWorkspace={controller.currentWorkspace}
              jobs={controller.cronJobs}
              onJobsChange={controller.setCronJobs}
              onNavigate={navigatePath}
              onSelectSession={controller.selectSession}
              routeJobId={scheduledJobId}
              sessions={controller.sessions}
              workspaces={controller.workspaces}
            />
          ) : (
            <>
              <div className="absolute right-4 top-4 z-30 flex items-center gap-2">
                {showShareButton && controller.currentSessionId ? (
                  <ShareButton
                    conversationId={controller.currentSessionId}
                    currentWorkspace={controller.currentWorkspace}
                    workspace={controller.currentWorkspaceInfo}
                    onRetryWorkspaceAccess={() => {
                      controller.requestWorkspaceTreeRefresh({ immediate: true })
                      void controller.refreshWorkspaces()
                    }}
                  />
                ) : null}
                <CronClockButton
                  agents={controller.agents}
                  currentSessionId={controller.currentSessionId}
                  currentWorkspace={controller.currentWorkspace}
                  jobs={controller.cronJobs}
                  onNavigate={navigatePath}
                  onSaved={(job) =>
                    controller.setCronJobs([
                      job,
                      ...controller.cronJobs.filter((item) => item.id !== job.id),
                    ])
                  }
                  sessions={controller.sessions}
                  workspaces={controller.workspaces}
                />
                <SettingsButton
                  agents={controller.agents}
                  defaultAgent={controller.defaultAgent}
                  onUpdateAgentEnv={controller.saveAgentEnv}
                  onUpdateAgentMode={controller.saveAgentMode}
                />
              </div>

              <ChatPanel
                agents={controller.agents}
                commands={controller.commands}
                currentAgent={controller.currentAgent}
                currentMessages={controller.currentMessages}
                currentSessionId={controller.currentSessionId}
                currentWorkspace={controller.currentWorkspace}
                currentWorkspaceInfo={controller.currentWorkspaceInfo}
                isSending={controller.isSending}
                onCancel={controller.cancelCurrentChat}
                onConfirmPermission={controller.handlePermissionConfirmed}
                onRetryWorkspaceAccess={() => {
                  controller.requestWorkspaceTreeRefresh({ immediate: true })
                  void controller.refreshWorkspaces()
                }}
                onSend={controller.sendCurrentMessage}
                onWorkspaceFilesChanged={() => {
                  controller.requestWorkspaceTreeRefresh({ immediate: true })
                }}
                pendingPermission={controller.pendingPermission}
                streamItems={controller.streamItems}
              />
            </>
          )}
        </div>

        <WorkspacePreviewPane
          onRefreshWorkspaceStatus={() => {
            void controller.refreshWorkspaces()
          }}
          refreshToken={controller.workspaceTreeRefreshToken}
          workspace={controller.currentWorkspaceInfo}
        />
      </main>
    </div>
  )
}
