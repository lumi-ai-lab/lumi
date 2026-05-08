'use client'

import { useEffect, useRef, useState } from 'react'

import type { ThinkingData } from '@/lib/types'

function formatDuration(ms = 0) {
  const seconds = Math.floor(ms / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  return `${minutes}m ${seconds % 60}s`
}

function getFirstLine(content: string) {
  const firstLine = content.split('\n')[0]?.trim() || ''
  return firstLine.length > 96 ? `${firstLine.slice(0, 96)}...` : firstLine
}

export function ThinkingBlock({ thinking }: { thinking: ThinkingData }) {
  const [isOpen, setIsOpen] = useState(thinking.status === 'thinking')
  const isActive = thinking.status === 'thinking'
  const [elapsedMs, setElapsedMs] = useState(0)
  const startedAtRef = useRef(Date.now())
  const bodyRef = useRef<HTMLPreElement | null>(null)

  useEffect(() => {
    if (isActive) return
    setIsOpen(false)
  }, [isActive])

  useEffect(() => {
    if (!isActive) return
    startedAtRef.current = Date.now()
    const timer = window.setInterval(() => {
      setElapsedMs(Date.now() - startedAtRef.current)
    }, 1000)
    return () => window.clearInterval(timer)
  }, [isActive])

  useEffect(() => {
    if (!isActive || !isOpen || !bodyRef.current) return
    bodyRef.current.scrollTop = bodyRef.current.scrollHeight
  }, [thinking.content, isActive, isOpen])

  const summary = isActive
    ? `Thinking... (${formatDuration(elapsedMs)})`
    : `Thought complete (${formatDuration(thinking.duration || 0)})${
        thinking.content ? ` - ${getFirstLine(thinking.content)}` : ''
      }`

  return (
    <div className="my-2 w-full text-sm">
      <div className="border-t border-border" />
      <button
        className="flex w-full items-center gap-2 py-2 text-left text-sm italic text-muted-foreground transition hover:text-foreground"
        onClick={() => setIsOpen((current) => !current)}
        type="button"
      >
        <span className={`text-[10px] transition-transform ${isOpen ? 'rotate-90' : ''}`}>▶</span>
        <span className="min-w-0 flex-1 truncate">{summary}</span>
      </button>
      {isOpen ? (
        <pre
          className="legacy-hidden-scrollbar max-h-[240px] overflow-auto whitespace-pre-wrap break-words pb-2 text-sm italic leading-7 text-muted-foreground"
          ref={bodyRef}
        >
          {thinking.content}
        </pre>
      ) : null}
      <div className="border-t border-border" />
    </div>
  )
}
