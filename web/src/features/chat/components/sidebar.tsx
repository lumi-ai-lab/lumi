'use client'

import { AlarmClock, Plus } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { WorkspaceSelector } from '@/features/chat/components/workspace-selector'
import type { CreateWorkspaceOptions } from '@/lib/api'
import type { Agent, CronJob, SessionMeta, Workspace } from '@/lib/types'
import { useI18n } from '@/features/i18n/i18n-provider'
import { formatTime } from '@/utils/format'

export function Sidebar({
  agents,
  currentSessionId,
  cronJobs,
  currentWorkspaceId,
  onAddWorkspace,
  onCollapse,
  onCreateSession,
  onRemoveSession,
  onSetWorkspace,
  onShowScheduled,
  onSelectSession,
  sessions,
  workspaces,
}: {
  agents: Agent[]
  currentSessionId: string | null
  cronJobs: CronJob[]
  currentWorkspaceId: string
  onAddWorkspace: (
    name: string,
    path: string,
    options?: CreateWorkspaceOptions,
  ) => Promise<string | null>
  onCollapse: () => void
  onCreateSession: () => Promise<void>
  onRemoveSession: (sessionId: string) => Promise<void>
  onSetWorkspace: (workspaceId: string) => void
  onShowScheduled?: () => void
  onSelectSession: (sessionId: string) => Promise<void>
  sessions: SessionMeta[]
  workspaces: Workspace[]
}) {
  const { t } = useI18n()
  const [deleteTarget, setDeleteTarget] = useState<SessionMeta | null>(null)
  const deleteTaskCount = deleteTarget
    ? cronJobs.filter((job) => (job.channel || 'web') === 'web' && job.conversationId === deleteTarget.id).length
    : 0

  return (
    <aside className="flex h-full w-[260px] flex-shrink-0 flex-col border-r border-border bg-card">
      <WorkspaceSelector
        agents={agents}
        currentWorkspaceId={currentWorkspaceId}
        onAddWorkspace={onAddWorkspace}
        onCollapse={onCollapse}
        onSelectWorkspace={onSetWorkspace}
        workspaces={workspaces}
      />
      <div className="flex items-center justify-between px-4 pb-3 pt-5">
        <h2 className="text-[11px] font-bold uppercase tracking-[0.1em] text-muted-foreground">Chats</h2>
        <button
          className="flex h-6 w-6 items-center justify-center rounded-sm border border-border bg-transparent text-[rgb(var(--color-text-secondary))] transition hover:bg-accent hover:text-foreground"
          onClick={() => void onCreateSession()}
          title={t('sidebar.new_chat')}
          type="button"
        >
          <Plus className="h-3.5 w-3.5" />
        </button>
      </div>
      <div className="legacy-hidden-scrollbar flex-1 overflow-y-auto px-2 pb-2">
        {sessions.length ? (
          sessions.map((session) => (
            <div
              className={`group relative mb-0.5 rounded-md border border-transparent px-3 py-[10px] transition ${
                session.id === currentSessionId
                  ? 'bg-muted'
                  : 'bg-transparent hover:bg-accent'
              }`}
              key={session.id}
            >
              {session.id === currentSessionId ? (
                <span className="absolute left-0 top-1/2 h-3 w-0.5 -translate-y-1/2 rounded-r-sm bg-primary" />
              ) : null}
              <button
                className="w-full pr-5 text-left"
                onClick={() => void onSelectSession(session.id)}
                type="button"
              >
                <div className="flex items-center gap-2">
                  <span
                    className={`min-w-0 flex-1 truncate text-[13px] font-medium transition-colors ${
                      session.id === currentSessionId
                        ? 'text-foreground'
                        : 'text-[rgb(var(--color-text-secondary))] group-hover:text-foreground'
                    }`}
                  >
                    {session.title}
                  </span>
                  <span className="flex-shrink-0 text-[10px] text-muted-foreground">
                    {formatTime(session.updatedAt)}
                  </span>
                </div>
              </button>
              <button
                className="absolute right-2 top-1/2 flex h-5 w-5 -translate-y-1/2 items-center justify-center rounded-sm bg-background text-[rgb(var(--color-text-tertiary))] opacity-0 transition hover:bg-destructive hover:text-destructive-foreground group-hover:opacity-100"
                onClick={() => setDeleteTarget(session)}
                title="Delete"
                type="button"
              >
                &times;
              </button>
            </div>
          ))
        ) : (
          <div className="px-4 py-10 text-center text-xs text-muted-foreground">
            No chats in this workspace
          </div>
        )}
      </div>

      <div className="border-t border-border p-2">
        <button
          className="flex h-9 w-full items-center gap-2 rounded-md px-3 text-left text-[13px] text-[rgb(var(--color-text-secondary))] transition hover:bg-accent hover:text-foreground"
          onClick={onShowScheduled}
          type="button"
        >
          <AlarmClock className="h-4 w-4" />
          <span>Scheduled Tasks</span>
        </button>
      </div>

      <Dialog onOpenChange={(open) => !open && setDeleteTarget(null)} open={Boolean(deleteTarget)}>
        <DialogContent className="border-border bg-[rgb(var(--color-bg-muted))] p-6">
          <DialogHeader className="space-y-2">
            <DialogTitle>Delete Chat</DialogTitle>
            <DialogDescription>
              {deleteTaskCount > 0
                ? `This chat has ${deleteTaskCount} scheduled ${deleteTaskCount === 1 ? 'task' : 'tasks'}. Deleting this chat will also delete ${deleteTaskCount === 1 ? 'that task' : 'those tasks'}.`
                : 'Are you sure you want to delete this chat?'}
            </DialogDescription>
          </DialogHeader>
          <div className="mt-5 flex justify-end gap-3">
            <Button className="rounded-md" onClick={() => setDeleteTarget(null)} type="button" variant="outline">
              Cancel
            </Button>
            <Button
              className="rounded-md"
              onClick={async () => {
                if (deleteTarget) {
                  await onRemoveSession(deleteTarget.id)
                }
                setDeleteTarget(null)
              }}
              type="button"
              variant="destructive"
            >
              {deleteTaskCount > 0 ? 'Delete Chat & Tasks' : 'Delete'}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </aside>
  )
}
