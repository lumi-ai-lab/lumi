'use client'

import { AlarmClock, ChevronLeft, Pencil, Play, Plus, Search, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'

import { Button } from '@/components/ui/button'
import { CronTaskDialog } from '@/features/cron/cron-task-dialog'
import { cronStatus, formatCronSchedule, formatCronTime, statusDotClass } from '@/features/cron/cron-utils'
import * as api from '@/lib/api'
import type { Agent, CronJob, SessionMeta, Workspace } from '@/lib/types'

function cronChannel(job: CronJob) {
  return job.channel || 'web'
}

function cronChannelLabel(job: CronJob) {
  return cronChannelDisplay(cronChannel(job))
}

function cronChannelDisplay(channel: string) {
  switch (channel) {
    case 'web':
      return 'Web'
    case 'wechat':
      return 'WeChat'
    case 'wecom':
      return 'WeCom'
    default:
      return channel
  }
}

function cronTargetLabel(job: CronJob) {
  if (job.channel === 'wechat') {
    return job.target?.wechat?.conversationKey || job.conversationId || 'Legacy unbound task'
  }
  if (job.channel === 'wecom') {
    return job.target?.wecom?.chatId || job.target?.wecom?.userId || job.conversationId || 'Legacy unbound task'
  }
  return job.conversationId || 'Legacy unbound task'
}

function isWebJob(job: CronJob) {
  return cronChannel(job) === 'web'
}

export function ScheduledTasksPage({
  agents,
  currentSessionId,
  currentWorkspace,
  jobs,
  onJobsChange,
  onNavigate,
  onSelectSession,
  routeJobId,
  sessions,
  workspaces,
}: {
  agents: Agent[]
  currentSessionId: string | null
  currentWorkspace: string
  jobs: CronJob[]
  onJobsChange: (jobs: CronJob[]) => void
  onNavigate: (path: string) => void
  onSelectSession: (sessionId: string) => Promise<void>
  routeJobId?: string | null
  sessions: SessionMeta[]
  workspaces: Workspace[]
}) {
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingJob, setEditingJob] = useState<CronJob | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [channelFilter, setChannelFilter] = useState('all')
  const [conversationFilter, setConversationFilter] = useState('all')
  const [search, setSearch] = useState('')
  const job = useMemo(() => jobs.find((item) => item.id === routeJobId) || null, [jobs, routeJobId])

  const conversationOptions = useMemo(() => {
    const seen = new Set<string>()
    return jobs
      .filter((item) => channelFilter === 'all' || cronChannel(item) === channelFilter)
      .map((item) => ({
        key: `${cronChannel(item)}:${cronTargetLabel(item)}`,
        channel: cronChannel(item),
        label: cronTargetLabel(item),
      }))
      .filter((item) => {
        if (seen.has(item.key)) return false
        seen.add(item.key)
        return true
      })
  }, [channelFilter, jobs])

  const filteredJobs = useMemo(() => {
    const query = search.trim().toLowerCase()
    return jobs.filter((item) => {
      if (channelFilter !== 'all' && cronChannel(item) !== channelFilter) return false
      const targetLabel = cronTargetLabel(item)
      if (conversationFilter !== 'all' && `${cronChannel(item)}:${targetLabel}` !== conversationFilter) return false
      if (!query) return true
      return [item.name, item.id, item.prompt, item.agentId, item.workspaceId, cronChannelLabel(item), targetLabel]
        .some((value) => value.toLowerCase().includes(query))
    })
  }, [channelFilter, conversationFilter, jobs, search])

  const upsert = (nextJob: CronJob) => {
    onJobsChange([nextJob, ...jobs.filter((item) => item.id !== nextJob.id)])
  }

  const scopedConversationId = (target: CronJob) => isWebJob(target) ? target.conversationId : ''

  const remove = async (target: CronJob) => {
    const conversationId = scopedConversationId(target)
    if (!conversationId) return
    setBusyId(target.id)
    try {
      await api.deleteCronJob(target.id, conversationId)
      onJobsChange(jobs.filter((item) => item.id !== target.id))
      if (routeJobId === target.id) onNavigate('/scheduled')
    } finally {
      setBusyId(null)
    }
  }

  const toggle = async (target: CronJob) => {
    const conversationId = scopedConversationId(target)
    if (!conversationId) return
    setBusyId(target.id)
    try {
      upsert(await api.updateCronJob(target.id, { conversationId, enabled: !target.enabled }))
    } finally {
      setBusyId(null)
    }
  }

  const runNow = async (target: CronJob) => {
    const conversationId = scopedConversationId(target)
    if (!conversationId) return
    setBusyId(target.id)
    try {
      const result = await api.runCronJobNow(target.id, conversationId)
      if (result.conversationId) {
        await onSelectSession(result.conversationId)
      }
    } finally {
      setBusyId(null)
    }
  }

  if (routeJobId) {
    return (
      <div className="flex h-full min-w-0 flex-1 flex-col overflow-hidden bg-background">
        <div className="border-b border-border px-6 py-4">
          <button className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground" onClick={() => onNavigate('/scheduled')} type="button">
            <ChevronLeft className="h-4 w-4" />
            Back to Scheduled Tasks
          </button>
        </div>
        {!job ? (
          <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Scheduled task not found</div>
        ) : (
          <div className="legacy-hidden-scrollbar flex-1 overflow-y-auto px-6 py-6">
            <div className="mx-auto max-w-[820px]">
              <div className="flex items-start justify-between gap-4">
                <div>
                  <h1 className="text-2xl font-semibold">{job.name}</h1>
                  <div className="mt-2 flex items-center gap-3 text-sm text-muted-foreground">
                    <span className="inline-flex items-center gap-2">
                      <span className={`h-2 w-2 rounded-full ${statusDotClass(cronStatus(job))}`} />
                      {cronStatus(job)}
                    </span>
                    <span>Next run: {formatCronTime(job.state.nextRunAt)}</span>
                  </div>
                </div>
                {isWebJob(job) ? (
                  <div className="flex items-center gap-2">
                    <Button size="icon" variant="outline" onClick={() => { setEditingJob(job); setDialogOpen(true) }}><Pencil className="h-4 w-4" /></Button>
                    <Button size="icon" variant="outline" onClick={() => void remove(job)} disabled={busyId === job.id}><Trash2 className="h-4 w-4" /></Button>
                    <Button size="sm" onClick={() => void runNow(job)} disabled={busyId === job.id}><Play className="h-4 w-4" />Run Now</Button>
                  </div>
                ) : (
                  <div className="rounded-md border border-border px-3 py-2 text-sm text-muted-foreground">Read-only in Web</div>
                )}
              </div>
              <div className="mt-8 grid gap-6 text-sm">
                <section>
                  <h2 className="mb-2 text-sm font-semibold text-muted-foreground">Agent</h2>
                  <div>{agents.find((agent) => agent.id === job.agentId)?.name || job.agentId}</div>
                </section>
                <section>
                  <h2 className="mb-2 text-sm font-semibold text-muted-foreground">Target</h2>
                  <div>Channel: {cronChannelLabel(job)}</div>
                  <div>Workspace: {workspaces.find((workspace) => workspace.id === job.workspaceId)?.name || job.workspaceId}</div>
                  <div className="flex min-w-0 items-center gap-2">
                    <span>Conversation:</span>
                    {isWebJob(job) && job.conversationId ? (
                      <button
                        className="min-w-0 truncate font-mono text-primary hover:underline"
                        onClick={() => void onSelectSession(job.conversationId!)}
                        title={job.conversationId}
                        type="button"
                      >
                        {job.conversationId}
                      </button>
                    ) : (
                      <span className="min-w-0 truncate font-mono" title={cronTargetLabel(job)}>{cronTargetLabel(job)}</span>
                    )}
                  </div>
                </section>
                <section>
                  <h2 className="mb-2 text-sm font-semibold text-muted-foreground">Prompt</h2>
                  <div className="whitespace-pre-wrap rounded-md border border-border bg-card p-4">{job.prompt}</div>
                </section>
                <section>
                  <h2 className="mb-2 text-sm font-semibold text-muted-foreground">Schedule</h2>
                  <div className="flex items-center gap-3">
                    {isWebJob(job) ? (
                      <button className={`h-5 w-9 rounded-full ${job.enabled ? 'bg-primary' : 'bg-muted'} relative`} onClick={() => void toggle(job)} type="button">
                        <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-white transition ${job.enabled ? 'left-4' : 'left-0.5'}`} />
                      </button>
                    ) : null}
                    <span>{formatCronSchedule(job.schedule)}</span>
                  </div>
                </section>
                <section>
                  <h2 className="mb-2 text-sm font-semibold text-muted-foreground">Last Run</h2>
                  <div>
                    {job.state.lastRunAt ? `${job.state.lastStatus || 'unknown'}, ${formatCronTime(job.state.lastRunAt)}` : 'No runs yet'}
                    {job.state.lastError ? <span className="ml-2 text-destructive">{job.state.lastError}</span> : null}
                  </div>
                </section>
              </div>
            </div>
          </div>
        )}
        <CronTaskDialog agents={agents} currentSessionId={job?.conversationId || currentSessionId} currentWorkspace={currentWorkspace} job={editingJob} onClose={() => setDialogOpen(false)} onSaved={upsert} open={dialogOpen} sessions={sessions} workspaces={workspaces} />
      </div>
    )
  }

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col overflow-hidden bg-background">
      <div className="border-b border-border px-6 py-5">
        <div className="flex items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <AlarmClock className="h-5 w-5" />
            <h1 className="text-xl font-semibold">Scheduled Tasks</h1>
          </div>
          <Button size="sm" disabled={sessions.length === 0} onClick={() => { setEditingJob(null); setDialogOpen(true) }}><Plus className="h-4 w-4" />New Task</Button>
        </div>
        <div className="mt-5 grid gap-3 md:grid-cols-[180px_minmax(260px,1fr)_minmax(220px,320px)]">
          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">Channel</span>
            <select
              className="h-9 rounded-sm border border-border bg-background px-3 text-sm"
              value={channelFilter}
              onChange={(event) => { setChannelFilter(event.target.value); setConversationFilter('all') }}
            >
              <option value="all">All channels</option>
              <option value="web">Web</option>
              <option value="wechat">WeChat</option>
              <option value="wecom">WeCom</option>
            </select>
          </label>
          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">Conversation</span>
            <select
              className="h-9 rounded-sm border border-border bg-background px-3 font-mono text-sm"
              value={conversationFilter}
              onChange={(event) => setConversationFilter(event.target.value)}
            >
              <option value="all">All conversations</option>
              {conversationOptions.map((item) => (
                <option key={item.key} value={item.key}>{cronChannelDisplay(item.channel)} / {item.label}</option>
              ))}
            </select>
          </label>
          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">Search</span>
            <span className="flex h-9 items-center gap-2 rounded-sm border border-border bg-background px-3">
              <Search className="h-4 w-4 text-muted-foreground" />
              <input className="min-w-0 flex-1 bg-transparent text-sm outline-none" value={search} onChange={(event) => setSearch(event.target.value)} />
            </span>
          </label>
        </div>
      </div>
      <div className="legacy-hidden-scrollbar flex-1 overflow-y-auto p-6">
        {filteredJobs.length === 0 ? (
          <div className="py-20 text-center text-sm text-muted-foreground">No scheduled tasks match these filters</div>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
            {filteredJobs.map((item) => (
              <button key={item.id} className="rounded-md border border-border bg-card p-4 text-left transition hover:bg-accent" onClick={() => onNavigate(`/scheduled/${item.id}`)} type="button">
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate text-sm font-medium">{item.name}</span>
                  <span className="inline-flex items-center gap-1 text-xs text-muted-foreground"><span className={`h-2 w-2 rounded-full ${statusDotClass(cronStatus(item))}`} />{cronStatus(item)}</span>
                </div>
                <div className="mt-2 grid gap-1 text-xs text-muted-foreground">
                  <span>Channel: {cronChannelLabel(item)}</span>
                  <span className="truncate font-mono" title={cronTargetLabel(item)}>Conversation: {cronTargetLabel(item)}</span>
                  <span>{formatCronSchedule(item.schedule)}</span>
                </div>
                <div className="mt-3 flex items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span>Next: {formatCronTime(item.state.nextRunAt)}</span>
                  {isWebJob(item) ? (
                    <span className="flex gap-1" onClick={(event) => event.stopPropagation()}>
                      <Button size="sm" variant="outline" onClick={() => void toggle(item)} disabled={busyId === item.id}>{item.enabled ? 'Pause' : 'Resume'}</Button>
                      <Button size="sm" variant="outline" onClick={() => void runNow(item)} disabled={busyId === item.id}>Run</Button>
                      <Button size="icon" variant="outline" onClick={() => void remove(item)} disabled={busyId === item.id}><Trash2 className="h-3.5 w-3.5" /></Button>
                    </span>
                  ) : (
                    <span>Read-only</span>
                  )}
                </div>
              </button>
            ))}
          </div>
        )}
      </div>
      <CronTaskDialog agents={agents} currentSessionId={currentSessionId} currentWorkspace={currentWorkspace} job={editingJob} onClose={() => setDialogOpen(false)} onSaved={upsert} open={dialogOpen} sessions={sessions} workspaces={workspaces} />
    </div>
  )
}
