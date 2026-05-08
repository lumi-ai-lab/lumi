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
  const [prompt, setPrompt] = useState('')
  const [agentId, setAgentId] = useState('')
  const [workspaceId, setWorkspaceId] = useState('')
  const [mode, setMode] = useState<'once' | 'interval'>('once')
  const [runAt, setRunAt] = useState(toDatetimeLocal())
  const [minutes, setMinutes] = useState('60')
  const [conversationId, setConversationId] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open) return
    setName(job?.name || '')
    setPrompt(job?.prompt || '')
    setAgentId(job?.agentId || agents[0]?.id || '')
    setWorkspaceId(job?.workspaceId || currentWorkspace || workspaces[0]?.id || '')
    setMode(job?.schedule.type || 'once')
    setRunAt(toDatetimeLocal(job?.schedule.type === 'once' ? job.schedule.runAt : undefined))
    setMinutes(job?.schedule.type === 'interval' ? String(Math.round(job.schedule.everySeconds / 60)) : '60')
    setConversationId(job?.conversationId || currentSessionId || sessions[0]?.id || '')
  }, [agents, currentSessionId, currentWorkspace, job, open, sessions, workspaces])

  const schedule = useMemo<CronSchedule>(() => {
    if (mode === 'once') {
      return { type: 'once', runAt: new Date(runAt).getTime() }
    }
    return { type: 'interval', everySeconds: Math.max(1, Number(minutes) || 1) * 60 }
  }, [minutes, mode, runAt])

  const save = async () => {
    if (!conversationId) return
    setSaving(true)
    try {
      const input = {
        name,
        prompt,
        agentId,
        workspaceId,
        conversationId,
        schedule,
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
            <span className="text-muted-foreground">Prompt</span>
            <textarea
              className="min-h-[120px] rounded-sm border border-border bg-background px-3 py-2 text-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
            />
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
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="outline" onClick={onClose} type="button">Cancel</Button>
            <Button disabled={saving || !name || !prompt || !agentId || !workspaceId || !conversationId} onClick={() => void save()} type="button">
              {saving ? 'Saving...' : 'Save'}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
