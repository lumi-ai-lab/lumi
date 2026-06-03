'use client'

import { useEffect, useEffectEvent, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'

import styles from './setup-page.module.css'
import { getStatusIcon, getStatusTone, isUrl } from '@/lib/setup-status'
import type {
  SetupDependencyItem,
  SetupInstallEvent,
  SetupSnapshot,
} from '@/lib/setup-types'

const READY_REDIRECT_DELAY_MS = 1200
const RETRY_DELAY_MS = 2000

function copyItems(items?: SetupDependencyItem[]): SetupDependencyItem[] {
  return (items ?? []).map((item) => ({ ...item }))
}

function SetupSection({
  title,
  items,
  detailKey,
  missingActionLabel,
}: {
  title: string
  items: SetupDependencyItem[]
  detailKey: 'command' | 'package'
  missingActionLabel: string
}) {
  if (items.length === 0) {
    return null
  }

  return (
    <section className={styles.section}>
      <h2 className={styles.sectionTitle}>{title}</h2>
      <div className={styles.list}>
        {items.map((item) => {
          const tone = getStatusTone(item.status)
          const className = [styles.item, tone ? styles[tone] : ''].filter(Boolean).join(' ')
          const detail = detailKey === 'command' ? item.command : item.package

          return (
            <div key={`${item.name}-${detail ?? ''}`} className={className}>
              <span className={styles.statusIcon}>{getStatusIcon(item.status)}</span>
              <div className={styles.itemInfo}>
                <span className={styles.itemName}>{item.name}</span>
                {detail ? <span className={styles.itemDetail}>{detail}</span> : null}
              </div>
              <div className={styles.itemStatus}>
                <span>{item.message}</span>
                {item.install && (item.status === 'missing' || item.status === 'outdated') ? (
                  isUrl(item.install) ? (
                    <a
                      className={styles.installLink}
                      href={item.install}
                      rel="noreferrer"
                      target="_blank"
                    >
                      {missingActionLabel}
                    </a>
                  ) : (
                    <span className={styles.installHint}>{item.install}</span>
                  )
                ) : null}
              </div>
            </div>
          )
        })}
      </div>
    </section>
  )
}

export function SetupPage() {
  const router = useRouter()
  const [environment, setEnvironment] = useState<SetupDependencyItem[]>([])
  const [agents, setAgents] = useState<SetupDependencyItem[]>([])
  const [acpPackages, setAcpPackages] = useState<SetupDependencyItem[]>([])
  const [isReady, setIsReady] = useState(false)
  const [isInstalling, setIsInstalling] = useState(false)
  const [error, setError] = useState('')
  const eventSourceRef = useRef<EventSource | null>(null)
  const retryTimerRef = useRef<number | null>(null)
  const readyTimerRef = useRef<number | null>(null)

  const clearReadyTimer = useEffectEvent(() => {
    if (readyTimerRef.current !== null) {
      window.clearTimeout(readyTimerRef.current)
      readyTimerRef.current = null
    }
  })

  const scheduleReadyRedirect = useEffectEvent(() => {
    clearReadyTimer()
    readyTimerRef.current = window.setTimeout(() => {
      router.replace('/c')
    }, READY_REDIRECT_DELAY_MS)
  })

  const applySnapshot = useEffectEvent((snapshot: SetupSnapshot) => {
    setEnvironment(copyItems(snapshot.environment))
    setAgents(copyItems(snapshot.agents))
    setAcpPackages(copyItems(snapshot.acpPackages))
    setIsReady(snapshot.ready)

    if (snapshot.ready) {
      scheduleReadyRedirect()
    } else {
      clearReadyTimer()
    }
  })

  const subscribeStatus = useEffectEvent(() => {
    eventSourceRef.current?.close()
    const eventSource = new EventSource('/api/setup/subscribe')
    eventSourceRef.current = eventSource

    eventSource.onmessage = (event) => {
      try {
        const snapshot = JSON.parse(event.data) as SetupSnapshot
        setError('')
        applySnapshot(snapshot)
      } catch {
        // Ignore malformed events and keep listening.
      }
    }

    eventSource.onerror = () => {
      setError('Connection lost, retrying...')
      eventSource.close()
      retryTimerRef.current = window.setTimeout(() => {
        subscribeStatus()
      }, RETRY_DELAY_MS)
    }
  })

  useEffect(() => {
    subscribeStatus()

    return () => {
      eventSourceRef.current?.close()
      if (retryTimerRef.current !== null) {
        window.clearTimeout(retryTimerRef.current)
      }
      clearReadyTimer()
    }
  }, [clearReadyTimer, subscribeStatus])

  async function startInstall() {
    setIsInstalling(true)
    setError('')
    setAgents((current) =>
      current.map((item) => (item.status === 'missing' ? { ...item, status: 'installing' } : item))
    )
    setAcpPackages((current) =>
      current.map((item) => (item.status === 'not_installed' ? { ...item, status: 'installing' } : item))
    )

    try {
      const response = await fetch('/api/setup/install', { method: 'POST' })
      const reader = response.body?.getReader()

      if (!reader) {
        throw new Error('No response body')
      }

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) {
          break
        }

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() ?? ''

        for (const line of lines) {
          if (line.startsWith('event: ')) {
            continue
          }

          if (!line.startsWith('data: ')) {
            continue
          }

          const payload = JSON.parse(line.slice(6)) as SetupInstallEvent

          if (payload.type === 'agent' && payload.index !== undefined) {
            setAgents((current) =>
              current.map((item, index) =>
                index === payload.index
                  ? {
                      ...item,
                      status: payload.status ?? item.status,
                      message: payload.message ?? item.message,
                    }
                  : item
              )
            )
          }

          if (payload.type === 'acp' && payload.index !== undefined) {
            setAcpPackages((current) =>
              current.map((item, index) =>
                index === payload.index
                  ? {
                      ...item,
                      status: payload.status ?? item.status,
                      message: payload.message ?? item.message,
                    }
                  : item
              )
            )
          }

          if (payload.success !== undefined) {
            if (payload.success) {
              setIsReady(true)
              scheduleReadyRedirect()
            } else if (payload.error) {
              setError(payload.error)
            }
          }
        }
      }
    } catch {
      setError('Installation failed')
    } finally {
      setIsInstalling(false)
    }
  }

  const canInstall =
    environment.every((item) => item.status === 'ready') &&
    (agents.some((item) => item.status === 'missing') ||
      acpPackages.some((item) => item.status === 'not_installed')) &&
    !isInstalling

  const hasBlocked = acpPackages.some((item) => item.status === 'blocked')
  const hasMissing =
    environment.some((item) => item.status === 'missing' || item.status === 'outdated') ||
    agents.some((item) => item.status === 'missing')

  return (
    <main className={styles.page}>
      <div className={styles.card}>
        <header className={styles.header}>
          <h1 className={styles.title}>Lumi Setup</h1>
          <p className={styles.subtitle}>Checking dependencies...</p>
        </header>

        <SetupSection
          detailKey="command"
          items={environment}
          missingActionLabel="Download Node.js →"
          title="Environment"
        />
        <SetupSection
          detailKey="command"
          items={agents}
          missingActionLabel="Download →"
          title="Agents"
        />
        <SetupSection
          detailKey="package"
          items={acpPackages}
          missingActionLabel="Download →"
          title="ACP Packages"
        />

        {error ? <div className={styles.errorMessage}>{error}</div> : null}

        <div className={styles.actions}>
          {canInstall ? (
            <button
              className={`${styles.button} ${styles.installButton}`}
              onClick={startInstall}
              type="button"
            >
              Install Dependencies
            </button>
          ) : null}

          {isInstalling ? (
            <button className={`${styles.button} ${styles.installButton}`} disabled type="button">
              <span className={styles.spinner} />
              Installing...
            </button>
          ) : null}

          {(hasBlocked || hasMissing) && !canInstall && !isInstalling && !isReady ? (
            <button
              className={`${styles.button} ${styles.blockedButton}`}
              disabled
              type="button"
            >
              Install Prerequisites First
            </button>
          ) : null}

          {isReady ? (
            <button
              className={`${styles.button} ${styles.continueButton}`}
              onClick={() => router.replace('/c')}
              type="button"
            >
              Continue to Chat
            </button>
          ) : null}
        </div>
      </div>
    </main>
  )
}
