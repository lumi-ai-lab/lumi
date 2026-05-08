'use client'

import { useEffect, useMemo, useState } from 'react'

import { cn } from '@/lib/utils'
import type { ToolCall } from '@/lib/types'

type ToolStep = {
  description: string
  details?: string
  key: string
  name: string
  output?: string
  status: ToolCall['status']
}

function formatRawInput(value?: string) {
  if (!value || value === '{}') return ''

  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch {
    return value
  }
}

function parseRawInput(value?: string): Record<string, unknown> | null {
  if (!value || value === '{}') return null
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : null
  } catch {
    return null
  }
}

function asString(value: unknown) {
  return typeof value === 'string' && value.trim() ? value.trim() : ''
}

function summarizeTool(tool: ToolCall) {
  const rawInput = parseRawInput(tool.rawInput)

  if (rawInput) {
    const command = asString(rawInput.command)
    if (command) return command

    const filePath = asString(rawInput.file_path) || asString(rawInput.path) || asString(rawInput.fileName)
    if (filePath) return filePath

    const pattern = asString(rawInput.pattern)
    const query = asString(rawInput.query)
    const url = asString(rawInput.url)
    if (pattern) return rawInput.path ? `"${pattern}" in ${String(rawInput.path)}` : `"${pattern}"`
    if (query) return query
    if (url) return url
  }

  return tool.input || tool.description || (tool.title !== tool.toolCallId ? tool.title : '') || tool.kind || ''
}

function toStep(tool: ToolCall): ToolStep {
  const details = formatRawInput(tool.rawInput) || tool.input
  return {
    description: summarizeTool(tool),
    details,
    key: tool.toolCallId,
    name: tool.toolName || tool.kind || 'Tool',
    output: tool.error || tool.output,
    status: tool.status,
  }
}

function statusClass(status: ToolCall['status']) {
  if (status === 'completed') return 'bg-[#16a34a]'
  if (status === 'error') return 'bg-[#dc2626]'
  return 'animate-pulse bg-muted-foreground'
}

function ToolStepRow({ step }: { step: ToolStep }) {
  const [expanded, setExpanded] = useState(step.status === 'error')
  const hasDetails = Boolean(step.details || step.output)

  return (
    <div className="flex flex-col">
      <div className="flex min-w-0 items-center gap-3 text-muted-foreground">
        <span className={cn('h-2.5 w-2.5 shrink-0 rounded-full', statusClass(step.status))} />
        <button
          className={cn(
            'flex min-w-0 flex-1 items-baseline gap-2 text-left transition',
            hasDetails && 'hover:text-foreground'
          )}
          disabled={!hasDetails}
          onClick={() => setExpanded((current) => !current)}
          type="button"
        >
          <span className="shrink-0 text-[13px] font-medium text-foreground">{step.name}</span>
          {step.description ? (
            <span className={cn('min-w-0 text-[13px] opacity-80', expanded ? 'break-all' : 'truncate')}>
              {step.description}
            </span>
          ) : null}
        </button>
        {hasDetails ? (
          <button
            className="shrink-0 text-muted-foreground transition hover:text-foreground"
            onClick={() => setExpanded((current) => !current)}
            type="button"
          >
            {expanded ? '▼' : '▶'}
          </button>
        ) : null}
      </div>

      {expanded && hasDetails ? (
        <div className="ml-5 mt-1 border-l border-border pl-3">
          {step.details ? (
            <div className="mb-1.5">
              <div className="mb-1 text-[11px] font-medium text-muted-foreground">Input</div>
              <pre className="legacy-hidden-scrollbar max-h-[200px] overflow-auto whitespace-pre-wrap break-all rounded bg-muted/40 px-2 py-1.5 text-xs leading-5 text-muted-foreground">
                {step.details}
              </pre>
            </div>
          ) : null}
          {step.output ? (
            <div>
              <div className="mb-1 text-[11px] font-medium text-muted-foreground">
                {step.status === 'error' ? 'Error' : 'Output'}
              </div>
              <pre className="legacy-hidden-scrollbar max-h-[220px] overflow-auto whitespace-pre-wrap break-all rounded bg-muted/40 px-2 py-1.5 text-xs leading-5 text-muted-foreground">
                {step.output}
              </pre>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

export function ToolSteps({ tools }: { tools: ToolCall[] }) {
  const hasRunningTools = tools.some((tool) => tool.status === 'pending')
  const [expanded, setExpanded] = useState(hasRunningTools)
  const steps = useMemo(() => tools.map(toStep), [tools])

  useEffect(() => {
    if (hasRunningTools) {
      setExpanded(true)
    }
  }, [hasRunningTools])

  if (!steps.length) return null

  return (
    <div className="my-2">
      <button
        className="flex items-center gap-2 py-1 text-left text-sm text-muted-foreground transition hover:text-foreground"
        onClick={() => setExpanded((current) => !current)}
        type="button"
      >
        <span className="relative flex h-2.5 w-2.5 items-center justify-center">
          <span className="h-2.5 w-2.5 rounded-full bg-muted-foreground/30" />
        </span>
        <span>View Steps</span>
        <span className="text-xs">{expanded ? '▼' : '▶'}</span>
      </button>
      {expanded ? (
        <div className="flex flex-col gap-2 pl-5 pt-2">
          {steps.map((step) => (
            <ToolStepRow key={step.key} step={step} />
          ))}
        </div>
      ) : null}
    </div>
  )
}
