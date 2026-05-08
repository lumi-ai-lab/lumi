'use client'

import { AlarmClock } from 'lucide-react'
import { useMemo, useState } from 'react'

import { Button } from '@/components/ui/button'
import { CronTaskDialog } from '@/features/cron/cron-task-dialog'
import { cronStatus, statusDotClass } from '@/features/cron/cron-utils'
import type { Agent, CronJob, SessionMeta, Workspace } from '@/lib/types'

export function CronClockButton({
  agents,
  currentSessionId,
  currentWorkspace,
  jobs,
  onNavigate,
  onSaved,
  sessions,
  workspaces,
}: {
  agents: Agent[]
  currentSessionId: string | null
  currentWorkspace: string
  jobs: CronJob[]
  onNavigate: (path: string) => void
  onSaved: (job: CronJob) => void
  sessions: SessionMeta[]
  workspaces: Workspace[]
}) {
  const [open, setOpen] = useState(false)
  const job = useMemo(
    () => jobs.find((item) => (item.channel || 'web') === 'web' && item.conversationId && item.conversationId === currentSessionId) || null,
    [currentSessionId, jobs],
  )
  const status = job ? cronStatus(job) : 'none'

  return (
    <>
      <Button
        aria-label="Scheduled tasks"
        className="relative h-8 w-8 rounded-md border-border bg-card text-[rgb(var(--color-text-secondary))] hover:bg-accent hover:text-foreground"
        onClick={() => {
          if (job) {
            onNavigate(`/scheduled/${job.id}`)
          } else {
            setOpen(true)
          }
        }}
        size="icon"
        title={job ? job.name : 'Create scheduled task'}
        type="button"
        variant="outline"
      >
        <AlarmClock className="h-4 w-4" />
        <span className={`absolute right-1 top-1 h-2 w-2 rounded-full ${statusDotClass(status)}`} />
      </Button>
      <CronTaskDialog
        agents={agents}
        currentSessionId={currentSessionId}
        currentWorkspace={currentWorkspace}
        onClose={() => setOpen(false)}
        onSaved={onSaved}
        open={open}
        sessions={sessions}
        workspaces={workspaces}
      />
    </>
  )
}
