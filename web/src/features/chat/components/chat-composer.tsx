'use client'

import { Paperclip, X } from 'lucide-react'
import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'

import { fetchWorkspaceFiles, uploadFiles } from '@/lib/api'
import { cn, formatFileSize } from '@/lib/utils'
import type { Agent, FileInfo, MessageFile, SlashCommand, UploadedFile } from '@/lib/types'
import { useI18n } from '@/features/i18n/i18n-provider'

interface ChatComposerProps {
  agents: Agent[]
  commands: SlashCommand[]
  currentAgent: string
  currentWorkspace: string
  disabled: boolean
  isSending: boolean
  onCancel: () => void
  onSend: (message: string, files: MessageFile[]) => Promise<void>
  onSlashCommandsOpen?: () => void
  onWorkspaceFilesChanged?: () => void
}

export function ChatComposer({
  agents,
  commands,
  currentAgent,
  currentWorkspace,
  disabled,
  isSending,
  onCancel,
  onSend,
  onSlashCommandsOpen,
  onWorkspaceFilesChanged,
}: ChatComposerProps) {
  const { t } = useI18n()
  const [message, setMessage] = useState('')
  const [showMentions, setShowMentions] = useState(false)
  const [showCommands, setShowCommands] = useState(false)
  const [mentionQuery, setMentionQuery] = useState('')
  const [commandQuery, setCommandQuery] = useState('')
  const [selectedIndex, setSelectedIndex] = useState(0)
  const [workspaceFiles, setWorkspaceFiles] = useState<FileInfo[]>([])
  const [isLoadingFiles, setIsLoadingFiles] = useState(false)
  const [uploadedFiles, setUploadedFiles] = useState<UploadedFile[]>([])
  const [isDragging, setIsDragging] = useState(false)
  const [isUploading, setIsUploading] = useState(false)
  const textareaRef = useRef<HTMLTextAreaElement | null>(null)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const searchTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const filteredAgents = agents.filter((agent) => {
    const query = mentionQuery.toLowerCase()
    return agent.id.toLowerCase().includes(query) || agent.name.toLowerCase().includes(query)
  })
  const filteredFiles = workspaceFiles.slice(0, 15)
  const filteredCommands = commands.filter((command) => {
    const query = commandQuery.toLowerCase()
    return (
      command.name.toLowerCase().includes(query) ||
      command.description.toLowerCase().includes(query)
    )
  })
  const totalMentionItems = filteredAgents.length + filteredFiles.length

  useEffect(() => {
    if (!showMentions || !currentWorkspace) {
      setWorkspaceFiles([])
      return
    }

    if (searchTimeoutRef.current) {
      clearTimeout(searchTimeoutRef.current)
    }

    searchTimeoutRef.current = setTimeout(async () => {
      setIsLoadingFiles(true)
      try {
        const files = await fetchWorkspaceFiles(currentWorkspace, mentionQuery, 20)
        setWorkspaceFiles(files)
      } catch {
        setWorkspaceFiles([])
      } finally {
        setIsLoadingFiles(false)
      }
    }, 150)

    return () => {
      if (searchTimeoutRef.current) {
        clearTimeout(searchTimeoutRef.current)
      }
    }
  }, [currentWorkspace, mentionQuery, showMentions])

  useEffect(() => {
    const handleEscape = (event: globalThis.KeyboardEvent) => {
      if (event.key !== 'Escape') return

      if (showCommands) {
        event.preventDefault()
        removeTrigger('/')
        setShowCommands(false)
      }

      if (showMentions) {
        event.preventDefault()
        removeTrigger('@')
        setShowMentions(false)
      }
    }

    window.addEventListener('keydown', handleEscape, true)
    return () => window.removeEventListener('keydown', handleEscape, true)
  }, [showCommands, showMentions])

  const resetDropdowns = () => {
    setShowMentions(false)
    setShowCommands(false)
    setMentionQuery('')
    setCommandQuery('')
    setSelectedIndex(0)
  }

  const removeTrigger = (trigger: '@' | '/') => {
    const textarea = textareaRef.current
    if (!textarea) return

    const cursorPosition = textarea.selectionStart
    const before = message.slice(0, cursorPosition)
    const after = message.slice(cursorPosition)
    const pattern = trigger === '/' ? /(?:^|\n)\/[\w\-:]*$/ : /@[\w\-/\.]*$/
    const nextBefore = before.replace(pattern, '')

    setMessage(nextBefore + after)
    setMentionQuery('')
    setCommandQuery('')

    window.setTimeout(() => {
      textarea.setSelectionRange(nextBefore.length, nextBefore.length)
      textarea.focus()
    }, 0)
  }

  const selectAgent = (agent: Agent) => {
    const textarea = textareaRef.current
    if (!textarea) return

    const cursorPosition = textarea.selectionStart
    const before = message.slice(0, cursorPosition)
    const after = message.slice(cursorPosition)
    const nextBefore = before.replace(/@[\w\-/\.]*$/, `@${agent.id} `)

    setMessage(nextBefore + after)
    resetDropdowns()

    window.setTimeout(() => {
      textarea.focus()
      textarea.setSelectionRange(nextBefore.length, nextBefore.length)
    }, 0)
  }

  const selectCommand = (command: SlashCommand) => {
    const textarea = textareaRef.current
    if (!textarea) return

    const cursorPosition = textarea.selectionStart
    const before = message.slice(0, cursorPosition)
    const after = message.slice(cursorPosition)
    const nextBefore = before.replace(/(?:^|\n)\/[\w\-:]*$/, `/${command.name} `)

    setMessage(nextBefore + after)
    resetDropdowns()

    window.setTimeout(() => {
      textarea.focus()
      textarea.setSelectionRange(nextBefore.length, nextBefore.length)
    }, 0)
  }

  const selectFile = (file: FileInfo) => {
    const textarea = textareaRef.current
    if (!textarea) return

    const cursorPosition = textarea.selectionStart
    const before = message.slice(0, cursorPosition)
    const after = message.slice(cursorPosition)
    const nextBefore = before.replace(/@[\w\-/\.]*$/, `@${file.path} `)

    setMessage(nextBefore + after)
    resetDropdowns()
    setWorkspaceFiles([])

    window.setTimeout(() => {
      textarea.focus()
      textarea.setSelectionRange(nextBefore.length, nextBefore.length)
    }, 0)
  }

  const submit = async () => {
    const trimmed = message.trim()
    if (!trimmed && uploadedFiles.length === 0) return

    await onSend(
      trimmed,
      uploadedFiles.map((file) => ({
        name: file.name,
        path: file.path,
        size: file.size,
      }))
    )

    setMessage('')
    setUploadedFiles([])
    resetDropdowns()
  }

  const handleInput = (value: string, cursorPosition: number) => {
    setMessage(value)
    const before = value.slice(0, cursorPosition)

    const slashMatch = before.match(/(?:^|\n)\/([\w\-:]*)$/)
    if (slashMatch) {
      setShowCommands(true)
      setShowMentions(false)
      setCommandQuery(slashMatch[1] || '')
      setSelectedIndex(0)
      onSlashCommandsOpen?.()
      return
    }

    const mentionMatch = before.match(/@([\w\-/\.]*)$/)
    if (mentionMatch) {
      setShowMentions(true)
      setShowCommands(false)
      setMentionQuery(mentionMatch[1] || '')
      setSelectedIndex(0)
      return
    }

    resetDropdowns()
  }

  const handleKeyDown = async (event: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Escape') {
      if (showCommands) {
        event.preventDefault()
        removeTrigger('/')
        setShowCommands(false)
      }

      if (showMentions) {
        event.preventDefault()
        removeTrigger('@')
        setShowMentions(false)
      }

      return
    }

    if (showCommands && filteredCommands.length > 0) {
      if (event.key === 'ArrowDown') {
        event.preventDefault()
        setSelectedIndex((current) => (current + 1) % filteredCommands.length)
        return
      }

      if (event.key === 'ArrowUp') {
        event.preventDefault()
        setSelectedIndex((current) => (current - 1 + filteredCommands.length) % filteredCommands.length)
        return
      }

      if (event.key === 'Enter' || event.key === 'Tab') {
        event.preventDefault()
        const selected = filteredCommands[selectedIndex]
        if (selected) {
          selectCommand(selected)
        }
        return
      }
    }

    if (showMentions && totalMentionItems > 0) {
      if (event.key === 'ArrowDown') {
        event.preventDefault()
        setSelectedIndex((current) => (current + 1) % totalMentionItems)
        return
      }

      if (event.key === 'ArrowUp') {
        event.preventDefault()
        setSelectedIndex((current) => (current - 1 + totalMentionItems) % totalMentionItems)
        return
      }

      if (event.key === 'Enter' || event.key === 'Tab') {
        event.preventDefault()

        if (selectedIndex < filteredAgents.length) {
          const selected = filteredAgents[selectedIndex]
          if (selected) selectAgent(selected)
          return
        }

        const fileIndex = selectedIndex - filteredAgents.length
        const selected = filteredFiles[fileIndex]
        if (selected) {
          selectFile(selected)
        }
        return
      }
    }

    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
      if (showCommands || showMentions) {
        event.preventDefault()
        resetDropdowns()
        return
      }

      if (message.trim() || uploadedFiles.length > 0) {
        event.preventDefault()
        await submit()
      }
    }
  }

  const handleFileUpload = async (files: FileList | File[]) => {
    if (!currentWorkspace || isUploading) return
    const fileList = Array.from(files)
    if (fileList.length === 0) return

    setIsUploading(true)
    try {
      const result = await uploadFiles(fileList, currentWorkspace)
      if (result.success && result.files) {
        const nextFiles = result.files || []
        setUploadedFiles((current) => [...current, ...nextFiles])
        onWorkspaceFilesChanged?.()
      }
    } finally {
      setIsUploading(false)
    }
  }

  return (
    <form
      className="w-full"
      onDragLeave={(event) => {
        event.preventDefault()
        setIsDragging(false)
      }}
      onDragOver={(event) => {
        event.preventDefault()
        setIsDragging(true)
      }}
      onDrop={async (event) => {
        event.preventDefault()
        setIsDragging(false)
        if (event.dataTransfer?.files) {
          await handleFileUpload(event.dataTransfer.files)
        }
      }}
      onSubmit={(event) => {
        event.preventDefault()
        void submit()
      }}
    >
      <div className="group/input relative flex flex-col rounded-lg border border-border bg-card shadow-panel transition-[border-color,box-shadow] duration-200 focus-within:border-[rgb(var(--color-text-secondary))] focus-within:shadow-floating">
        <input
          className="hidden"
          multiple
          onChange={(event) => {
            if (event.target.files) {
              void handleFileUpload(event.target.files)
              event.target.value = ''
            }
          }}
          ref={fileInputRef}
          type="file"
        />

        {uploadedFiles.length ? (
          <div className="flex flex-wrap gap-1.5 border-b border-border px-4 py-3">
            {uploadedFiles.map((file) => (
              <div
                className="flex items-center gap-1.5 rounded-sm border border-border bg-background px-2 py-1 text-xs"
                key={file.path}
              >
                <span className="text-foreground">{file.name}</span>
                <span className="text-muted-foreground">{formatFileSize(file.size)}</span>
                <button
                  className="rounded-sm p-0.5 text-muted-foreground transition hover:bg-accent hover:text-foreground"
                  onClick={() => {
                    setUploadedFiles((current) => current.filter((item) => item.path !== file.path))
                  }}
                  type="button"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
          </div>
        ) : null}

        {isDragging ? (
          <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border bg-background/95 text-sm text-foreground">
            <Paperclip className="h-5 w-5" />
            <span>{t('input.dropFiles')}</span>
          </div>
        ) : null}

        <textarea
          aria-label={t('input.placeholder')}
          className="min-h-[56px] w-full resize-none bg-transparent px-4 py-4 text-[15px] leading-6 text-foreground outline-none placeholder:text-muted-foreground"
          disabled={disabled}
          onChange={(event) => handleInput(event.target.value, event.target.selectionStart)}
          onKeyDown={(event) => void handleKeyDown(event)}
          placeholder={t('input.placeholder')}
          ref={textareaRef}
          rows={1}
          value={message}
        />

        <div className="flex items-center px-3 pb-3">
          <button
            className="flex h-8 w-8 items-center justify-center rounded-sm bg-transparent text-muted-foreground transition hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
            disabled={disabled || isUploading}
            onClick={() => fileInputRef.current?.click()}
            title="Attach files"
            type="button"
          >
            <Paperclip className="h-4 w-4" />
          </button>
          <div className="flex-1" />
          {isSending ? (
            <button
              className="rounded-full bg-muted px-4 py-1.5 text-[13px] font-semibold text-[rgb(var(--color-text-secondary))] transition hover:bg-destructive hover:text-destructive-foreground"
              onClick={onCancel}
              type="button"
            >
              Cancel
            </button>
          ) : (
            <button
              className={cn(
                'rounded-full px-4 py-1.5 text-[13px] font-semibold transition-all duration-150',
                disabled || (!message.trim() && uploadedFiles.length === 0)
                  ? 'bg-muted text-muted-foreground'
                  : 'bg-primary text-primary-foreground hover:opacity-95',
                'translate-y-0 opacity-100',
                disabled || (!message.trim() && uploadedFiles.length === 0)
                  ? 'pointer-events-none'
                  : 'pointer-events-auto'
              )}
              disabled={disabled || (!message.trim() && uploadedFiles.length === 0)}
              type="submit"
            >
              Send
            </button>
          )}
        </div>

        {showCommands && filteredCommands.length ? (
          <div className="absolute inset-x-3 bottom-full mb-2 rounded-md border border-border bg-card p-1 shadow-floating">
            <div className="mb-1 flex items-center gap-2 border-b border-border px-2 pb-2 text-xs text-muted-foreground">
              <span className="rounded-full bg-muted px-2 py-0.5 font-semibold uppercase tracking-[0.1em] text-foreground">
                {currentAgent}
              </span>
              <span>Available Commands</span>
            </div>
            {filteredCommands.map((command, index) => (
              <button
                className={`flex w-full items-center gap-3 rounded-sm px-3 py-2 text-left transition ${
                  selectedIndex === index ? 'bg-accent' : 'hover:bg-accent'
                }`}
                key={command.name}
                onClick={() => selectCommand(command)}
                onMouseEnter={() => setSelectedIndex(index)}
                type="button"
              >
                <span className="font-mono text-sm font-semibold text-foreground">/{command.name}</span>
                <span className="text-sm text-muted-foreground">{command.description}</span>
              </button>
            ))}
          </div>
        ) : null}

        {showMentions && totalMentionItems ? (
          <div className="absolute inset-x-3 bottom-full mb-2 rounded-md border border-border bg-card p-1 shadow-floating">
            {filteredAgents.length ? (
              <div className="px-2 pb-1 text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">
                Agents
              </div>
            ) : null}
            {filteredAgents.map((agent, index) => (
              <button
                className={`flex w-full items-center gap-3 rounded-sm px-3 py-2 text-left transition ${
                  selectedIndex === index ? 'bg-accent' : 'hover:bg-accent'
                }`}
                key={agent.id}
                onClick={() => selectAgent(agent)}
                onMouseEnter={() => setSelectedIndex(index)}
                type="button"
              >
                <span className="text-lg font-semibold">@</span>
                <span className="font-mono text-sm font-semibold text-foreground">{agent.id}</span>
                <span className="text-sm text-muted-foreground">{agent.name}</span>
              </button>
            ))}

            {filteredFiles.length ? (
              <div className="px-2 pb-1 pt-2 text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">
                Files
              </div>
            ) : null}
            {filteredFiles.map((file, index) => (
              <button
                className={`flex w-full items-center gap-3 rounded-sm px-3 py-2 text-left transition ${
                  selectedIndex === index + filteredAgents.length ? 'bg-accent' : 'hover:bg-accent'
                }`}
                key={file.path}
                onClick={() => selectFile(file)}
                onMouseEnter={() => setSelectedIndex(index + filteredAgents.length)}
                type="button"
              >
                <Paperclip className="h-4 w-4 text-muted-foreground" />
                <span className="font-mono text-sm font-medium text-foreground">{file.path}</span>
              </button>
            ))}

            {isLoadingFiles ? (
              <div className="px-3 py-2 text-xs text-muted-foreground">Loading files...</div>
            ) : null}
          </div>
        ) : null}
      </div>
    </form>
  )
}
