'use client'

import { confirmPermission } from '@/lib/api'
import type { PermissionRequest } from '@/lib/types'

function getOptionClass(kind: string) {
  return kind.includes('allow')
    ? 'bg-[#10b981] text-white hover:bg-[#059669]'
    : 'border border-border bg-transparent text-[rgb(var(--color-text-secondary))] hover:border-destructive hover:bg-accent hover:text-destructive'
}

function formatInput(rawInput?: Record<string, unknown>) {
  if (!rawInput) return ''
  if (rawInput.command) return String(rawInput.command)
  if (rawInput.file_path) return String(rawInput.file_path)
  return JSON.stringify(rawInput, null, 2)
}

export function PermissionRequestCard({
  request,
  agentId,
  onConfirmed,
}: {
  request: PermissionRequest
  agentId: string
  onConfirmed: () => void
}) {
  const targetAgentId = request.agentId || agentId

  return (
    <div className="my-3 rounded-md border border-border bg-card px-5 py-5 shadow-panel">
      <div className="mb-4 flex items-center gap-2">
        <span className="text-lg text-amber-400">&#9888;</span>
        <span className="text-[15px] font-semibold text-foreground">Permission Required</span>
      </div>

      <div className="mb-5">
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <span className="text-sm font-semibold text-foreground">{request.toolCall.title || 'Tool Call'}</span>
          {request.toolCall.kind ? (
            <span className="rounded-full border border-border bg-muted px-2.5 py-1 text-xs text-[rgb(var(--color-text-secondary))]">
              {request.toolCall.kind}
            </span>
          ) : null}
        </div>
        {request.toolCall.rawInput ? (
          <div className="rounded-md border border-border bg-muted px-4 py-3">
            <code className="whitespace-pre-wrap break-all text-[13px] leading-6 text-[rgb(var(--color-text-secondary))]">
              {formatInput(request.toolCall.rawInput)}
            </code>
          </div>
        ) : null}
      </div>

      <div className="flex flex-wrap justify-end gap-3">
        {request.options.map((option) => (
          <button
            className={`rounded-md px-4 py-2 text-[13px] font-medium transition ${getOptionClass(option.kind)}`}
            key={option.optionId}
            onClick={async () => {
              await confirmPermission(targetAgentId, request.toolCall.toolCallId, option.optionId)
              onConfirmed()
            }}
            type="button"
          >
            {option.name}
          </button>
        ))}
      </div>
    </div>
  )
}
