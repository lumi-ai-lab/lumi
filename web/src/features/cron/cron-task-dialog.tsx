'use client'

import { useEffect, useMemo, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import * as api from '@/lib/api'
import type { Agent, CronJob, CronSchedule, SessionMeta, Workspace } from '@/lib/types'

function toDatetimeLocal(value?: number) {
  const date = value ? new Date(value) : new Date(Date.now() + 60 * 60 * 1000)
  const offset = date.getTimezoneOffset() * 60000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}

export function CronTaskDialog({
  agents,
  currentSessionId,
  currentWorkspace,
  job,
  onClose,
  onSaved,
  open,
  sessions = [],
  workspaces,
}: {
  agents: Agent[]
  currentSessionId: string | null
  currentWorkspace: string
  job?: CronJob | null
  onClose: () => void
  onSaved: (job: CronJob) => void
  open: boolean
  sessions?: SessionMeta[]
  workspaces: Workspace[]
}) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [prompt, setPrompt] = useState('')
  const [execCmd, setExecCmd] = useState('')
  const [agentId, setAgentId] = useState('')
  const [workspaceId, setWorkspaceId] = useState('')
  const [mode, setMode] = useState<'once' | 'interval' | 'cron'>('cron')
  const [runAt, setRunAt] = useState(toDatetimeLocal())
  const [minutes, setMinutes] = useState('60')
  const [cronExpr, setCronExpr] = useState('0 8 * * *')
  const [conversationId, setConversationId] = useState('')
  const [mute, setMute] = useState(false)
  const [silent, setSilent] = useState(false)
  const [sessionMode, setSessionMode] = useState('reuse')
  const [workDir, setWorkDir] = useState('')
  const [agentMode, setAgentMode] = useState('')
  const [timeoutMins, setTimeoutMins] = useState('30')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open) return
    setName(job?.name || '')
    setDescription(job?.description || job?.name || '')
    setPrompt(job?.prompt || '')
    setExecCmd(job?.exec || '')
    setAgentId(job?.agentId || agents[0]?.id || '')
    setWorkspaceId(job?.workspaceId || currentWorkspace || workspaces[0]?.id || '')
    setMode(job?.schedule.type || 'cron')
    setRunAt(toDatetimeLocal(job?.schedule.type === 'once' ? job.schedule.runAt : undefined))
    setMinutes(job?.schedule.type === 'interval' ? String(Math.round(job.schedule.everySeconds / 60)) : '60')
    setCronExpr(job?.schedule.type === 'cron' ? job.schedule.cronExpr : '0 8 * * *')
    setConversationId(job?.conversationId || currentSessionId || sessions[0]?.id || '')
    setMute(Boolean(job?.mute))
    setSilent(Boolean(job?.silent))
    setSessionMode(job?.sessionMode || 'reuse')
    setWorkDir(job?.workDir || '')
    setAgentMode(job?.mode || '')
    setTimeoutMins(job?.timeoutMins === undefined ? '30' : String(job.timeoutMins))
  }, [agents, currentSessionId, currentWorkspace, job, open, sessions, workspaces])

  const schedule = useMemo<CronSchedule>(() => {
    if (mode === 'once') {
      return { type: 'once', runAt: new Date(runAt).getTime() }
    }
    if (mode === 'cron') {
      return { type: 'cron', cronExpr }
    }
    return { type: 'interval', everySeconds: Math.max(1, Number(minutes) || 1) * 60 }
  }, [cronExpr, minutes, mode, runAt])

  const save = async () => {
    if (!conversationId) return
    setSaving(true)
    try {
      const input = {
        name,
        description,
        prompt,
        exec: execCmd,
        agentId,
        workspaceId,
        conversationId,
        schedule,
        mute,
        silent,
        sessionMode,
        workDir,
        mode: agentMode,
        timeoutMins: Math.max(0, Number(timeoutMins) || 0),
      }
      const saved = job ? await api.updateCronJob(job.id, input) : await api.createCronJob(input)
      onSaved(saved)
      onClose()
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog onOpenChange={(next) => !next && onClose()} open={open}>
      <DialogContent className="max-h-[88vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="normal-case tracking-normal">
            {job ? 'Edit Scheduled Task' : 'Create Scheduled Task'}
          </DialogTitle>
        </DialogHeader>
        <div className="mt-4 grid gap-4">
          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">Name</span>
            <Input value={name} onChange={(event) => setName(event.target.value)} />
          </label>
          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">Description</span>
            <Input value={description} onChange={(event) => setDescription(event.target.value)} />
          </label>
          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">Prompt</span>
            <textarea
              className="min-h-[120px] rounded-sm border border-border bg-background px-3 py-2 text-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
            />
          </label>
          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">Shell Exec</span>
            <Input value={execCmd} onChange={(event) => setExecCmd(event.target.value)} placeholder="df -h" />
          </label>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="grid gap-1.5 text-sm">
              <span className="text-muted-foreground">Agent</span>
              <select className="h-9 rounded-sm border border-border bg-background px-3 text-sm" value={agentId} onChange={(event) => setAgentId(event.target.value)}>
                {agents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
              </select>
            </label>
            <label className="grid gap-1.5 text-sm">
              <span className="text-muted-foreground">Workspace</span>
              <select className="h-9 rounded-sm border border-border bg-background px-3 text-sm" value={workspaceId} onChange={(event) => setWorkspaceId(event.target.value)}>
                {workspaces.map((workspace) => <option key={workspace.id} value={workspace.id}>{workspace.name}</option>)}
              </select>
            </label>
          </div>
          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">Web Conversation</span>
            <select
              className="h-9 rounded-sm border border-border bg-background px-3 font-mono text-sm"
              disabled={Boolean(job)}
              value={conversationId}
              onChange={(event) => setConversationId(event.target.value)}
            >
              {!conversationId ? <option value="">Select a conversation</option> : null}
              {sessions.map((session) => (
                <option key={session.id} value={session.id}>{session.id}</option>
              ))}
              {conversationId && !sessions.some((session) => session.id === conversationId) ? (
                <option value={conversationId}>{conversationId}</option>
              ) : null}
            </select>
          </label>
          <div className="grid gap-3 text-sm">
            <span className="text-muted-foreground">Schedule</span>
            <label className="flex items-center gap-2">
              <input checked={mode === 'once'} onChange={() => setMode('once')} type="radio" />
              <span>Once at</span>
              <Input className="max-w-[220px]" type="datetime-local" value={runAt} onChange={(event) => setRunAt(event.target.value)} />
            </label>
            <label className="flex items-center gap-2">
              <input checked={mode === 'interval'} onChange={() => setMode('interval')} type="radio" />
              <span>Every</span>
              <Input className="w-24" min={1} type="number" value={minutes} onChange={(event) => setMinutes(event.target.value)} />
              <span>minutes</span>
            </label>
            <label className="flex items-center gap-2">
              <input checked={mode === 'cron'} onChange={() => setMode('cron')} type="radio" />
              <span>Cron expr</span>
              <Input className="max-w-[220px] font-mono" value={cronExpr} onChange={(event) => setCronExpr(event.target.value)} />
            </label>
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="grid gap-1.5 text-sm">
              <span className="text-muted-foreground">Session Mode</span>
              <select className="h-9 rounded-sm border border-border bg-background px-3 text-sm" value={sessionMode} onChange={(event) => setSessionMode(event.target.value)}>
                <option value="reuse">Reuse</option>
                <option value="new_per_run">New per run</option>
              </select>
            </label>
            <label className="grid gap-1.5 text-sm">
              <span className="text-muted-foreground">Timeout Minutes</span>
              <Input min={0} type="number" value={timeoutMins} onChange={(event) => setTimeoutMins(event.target.value)} />
            </label>
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="grid gap-1.5 text-sm">
              <span className="text-muted-foreground">Work Dir</span>
              <Input value={workDir} onChange={(event) => setWorkDir(event.target.value)} />
            </label>
            <label className="grid gap-1.5 text-sm">
              <span className="text-muted-foreground">Mode</span>
              <Input value={agentMode} onChange={(event) => setAgentMode(event.target.value)} />
            </label>
          </div>
          <div className="flex flex-wrap gap-4 text-sm">
            <label className="flex items-center gap-2">
              <input checked={mute} onChange={(event) => setMute(event.target.checked)} type="checkbox" />
              <span>Mute</span>
            </label>
            <label className="flex items-center gap-2">
              <input checked={silent} onChange={(event) => setSilent(event.target.checked)} type="checkbox" />
              <span>Silent</span>
            </label>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="outline" onClick={onClose} type="button">Cancel</Button>
            <Button disabled={saving || !name || (!prompt && !execCmd) || Boolean(prompt && execCmd) || !agentId || !workspaceId || !conversationId} onClick={() => void save()} type="button">
              {saving ? 'Saving...' : 'Save'}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
