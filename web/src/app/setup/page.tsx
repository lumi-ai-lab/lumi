'use client'

import { useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'

import { Button } from '@/components/ui/button'
import type { DependencyItem } from '@/lib/types'
import { isUrl } from '@/lib/utils'

interface SetupPayload {
  environment?: DependencyItem[]
  agents?: DependencyItem[]
  acpPackages?: DependencyItem[]
  ready?: boolean
}

const statusIcon: Record<DependencyItem['status'], string> = {
  blocked: '⊘',
  checking: '◐',
  error: '✗',
  installing: '◐',
  missing: '✗',
  not_installed: '○',
  outdated: '✗',
  ready: '✓',
}

const statusClass: Record<DependencyItem['status'], string> = {
  blocked: 'border-border opacity-70',
  checking: 'border-amber-500/30',
  error: 'border-destructive/30',
  installing: 'border-amber-500/30',
  missing: 'border-destructive/30',
  not_installed: 'border-border',
  outdated: 'border-destructive/30',
  ready: 'border-emerald-500/30',
}

const statusIconClass: Record<DependencyItem['status'], string> = {
  blocked: 'text-muted-foreground',
  checking: 'animate-pulse text-amber-400',
  error: 'text-destructive',
  installing: 'animate-pulse text-amber-400',
  missing: 'text-destructive',
  not_installed: 'text-muted-foreground',
  outdated: 'text-destructive',
  ready: 'text-emerald-400',
}

export default function SetupPage() {
  const router = useRouter()
  const [environment, setEnvironment] = useState<DependencyItem[]>([])
  const [agents, setAgents] = useState<DependencyItem[]>([])
  const [acpPackages, setAcpPackages] = useState<DependencyItem[]>([])
  const [isReady, setIsReady] = useState(false)
  const [isInstalling, setIsInstalling] = useState(false)
  const [error, setError] = useState('')

  const canInstall = useMemo(() => {
    const envReady = environment.every((item) => item.status === 'ready')
    const hasAgentMissing = agents.some((item) => item.status === 'missing')
    const hasPackageMissing = acpPackages.some((item) => item.status === 'not_installed')
    return envReady && (hasAgentMissing || hasPackageMissing) && !isInstalling
  }, [acpPackages, agents, environment, isInstalling])

  const hasBlocked = acpPackages.some((item) => item.status === 'blocked')
  const hasMissing =
    environment.some((item) => item.status === 'missing' || item.status === 'outdated') ||
    agents.some((item) => item.status === 'missing')

  useEffect(() => {
    const eventSource = new EventSource('/api/setup/subscribe')

    eventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data) as SetupPayload
        setEnvironment(data.environment || [])
        setAgents(data.agents || [])
        setAcpPackages(data.acpPackages || [])
        setIsReady(Boolean(data.ready))

        if (data.ready) {
          eventSource.close()
          router.replace('/c')
        }
      } catch {
        // Ignore malformed setup frames.
      }
    }

    eventSource.onerror = () => {
      setError('Connection lost, retrying...')
      eventSource.close()
      window.setTimeout(() => {
        router.refresh()
      }, 2000)
    }

    return () => eventSource.close()
  }, [router])

  const startInstall = async () => {
    setIsInstalling(true)
    setError('')

    try {
      const response = await fetch('/api/setup/install', { method: 'POST' })
      const reader = response.body?.getReader()
      if (!reader) throw new Error('No response body')

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          if (!line.startsWith('data: ')) continue

          const data = JSON.parse(line.slice(6)) as
            | { type?: 'agent' | 'acp'; index?: number; status?: DependencyItem['status']; message?: string }
            | { success?: boolean; error?: string }

          if ('type' in data && data.index !== undefined) {
            if (data.type === 'agent') {
              setAgents((current) =>
                current.map((item, index) =>
                  index === data.index
                    ? {
                        ...item,
                        status: data.status || item.status,
                        message: data.message || item.message,
                      }
                    : item
                )
              )
            }

            if (data.type === 'acp') {
              setAcpPackages((current) =>
                current.map((item, index) =>
                  index === data.index
                    ? {
                        ...item,
                        status: data.status || item.status,
                        message: data.message || item.message,
                      }
                    : item
                )
              )
            }
          }

          if ('success' in data && data.success) {
            setIsReady(true)
            router.replace('/c')
          }

          if ('error' in data && data.error) {
            setError(data.error)
          }
        }
      }
    } catch {
      setError('Installation failed')
    } finally {
      setIsInstalling(false)
    }
  }

  const renderSection = (title: string, items: DependencyItem[], detailKey: 'command' | 'package') => {
    if (!items.length) return null

    return (
      <section className="space-y-3">
        <div className="text-xs font-semibold uppercase tracking-[0.05em] text-muted-foreground">
          {title}
        </div>
        <div className="space-y-2">
          {items.map((item) => (
            <div
              className={`flex items-center gap-3 rounded-md border bg-accent px-3.5 py-2.5 ${statusClass[item.status]}`}
              key={item.command || item.package || item.name}
            >
              <div className={`w-5 text-center text-sm ${statusIconClass[item.status]}`}>{statusIcon[item.status]}</div>
              <div className="min-w-0 flex-1">
                <div className="text-[13px] font-semibold text-foreground">{item.name}</div>
                <div className="truncate font-mono text-[11px] text-muted-foreground">
                  {detailKey === 'command' ? item.command : item.package}
                </div>
              </div>
              <div className="max-w-[180px] text-right text-[11px] text-[rgb(var(--color-text-secondary))]">
                <div>{item.message}</div>
                {item.install && (item.status === 'missing' || item.status === 'outdated') ? (
                  isUrl(item.install) ? (
                    <a className="font-medium text-emerald-400" href={item.install} rel="noreferrer" target="_blank">
                      Download →
                    </a>
                  ) : (
                    <div className="truncate text-[10px] text-muted-foreground">{item.install}</div>
                  )
                ) : null}
              </div>
            </div>
          ))}
        </div>
      </section>
    )
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-background px-5 py-10">
      <div className="w-full max-w-[550px] rounded-lg border border-border bg-card px-10 py-10 shadow-panel">
        <div className="mb-6 text-center">
          <h1 className="text-2xl font-bold text-foreground">Lumi Setup</h1>
          <p className="mt-2 text-sm text-[rgb(var(--color-text-secondary))]">Checking dependencies...</p>
        </div>

        <div className="space-y-5">
          {renderSection('Environment', environment, 'command')}
          {renderSection('Agents', agents, 'command')}
          {renderSection('ACP Packages', acpPackages, 'package')}
        </div>

        {error ? (
          <div className="mt-4 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-3 text-sm text-destructive">
            {error}
          </div>
        ) : null}

        <div className="mt-6 flex justify-center">
          {canInstall ? (
            <Button className="rounded-md px-8 py-3" onClick={() => void startInstall()} type="button">
              Install Dependencies
            </Button>
          ) : null}
          {isInstalling ? <Button className="rounded-md px-8 py-3" disabled>Installing...</Button> : null}
          {(hasBlocked || hasMissing) && !canInstall && !isInstalling && !isReady ? (
            <Button className="rounded-md px-8 py-3" disabled>Install Prerequisites First</Button>
          ) : null}
          {isReady ? (
            <Button
              className="rounded-md bg-emerald-400 px-8 py-3 text-[rgb(var(--color-bg-root))] hover:opacity-90"
              onClick={() => {
                router.replace('/c')
              }}
              type="button"
            >
              Continue to Chat
            </Button>
          ) : null}
        </div>
      </div>
    </main>
  )
}
