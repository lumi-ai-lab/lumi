import { chmodSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'
import { getPiAcpSessionMapPath } from './paths.js'

export type StoredSession = {
  sessionId: string
  cwd: string
  sessionFile: string
  updatedAt: string
  instructionProfileDigest?: string
  requiresInstructionProfile?: boolean
}

type SessionMapFile = {
  version: 1
  sessions: Record<string, StoredSession>
}

function ensureParentDir(path: string) {
  const parent = dirname(path)
  mkdirSync(parent, { recursive: true, mode: 0o700 })
  chmodSync(parent, 0o700)
}

function loadFile(path: string): SessionMapFile {
  try {
    const raw = readFileSync(path, 'utf-8')
    const parsed = JSON.parse(raw) as SessionMapFile
    if (parsed?.version !== 1 || typeof parsed.sessions !== 'object' || !parsed.sessions) {
      return { version: 1, sessions: {} }
    }
    return parsed
  } catch {
    return { version: 1, sessions: {} }
  }
}

function saveFile(path: string, data: SessionMapFile): void {
  ensureParentDir(path)
  writeFileSync(path, JSON.stringify(data, null, 2) + '\n', { encoding: 'utf-8', mode: 0o600 })
  chmodSync(path, 0o600)
}

export class SessionStore {
  private readonly path: string

  constructor(path = getPiAcpSessionMapPath()) {
    this.path = path
  }

  get(sessionId: string): StoredSession | null {
    const db = loadFile(this.path)
    return db.sessions[sessionId] ?? null
  }

  upsert(entry: {
    sessionId: string
    cwd: string
    sessionFile: string
    instructionProfileDigest?: string
    requiresInstructionProfile?: boolean
  }): void {
    const db = loadFile(this.path)
    const current = db.sessions[entry.sessionId]
    db.sessions[entry.sessionId] = {
      sessionId: entry.sessionId,
      cwd: entry.cwd,
      sessionFile: entry.sessionFile,
      updatedAt: new Date().toISOString(),
      ...(entry.instructionProfileDigest !== undefined
        ? { instructionProfileDigest: entry.instructionProfileDigest }
        : current?.instructionProfileDigest
          ? { instructionProfileDigest: current.instructionProfileDigest }
          : {}),
      ...(entry.requiresInstructionProfile !== undefined
        ? { requiresInstructionProfile: entry.requiresInstructionProfile }
        : current?.requiresInstructionProfile
          ? { requiresInstructionProfile: true }
          : {})
    }
    saveFile(this.path, db)
  }

  delete(sessionId: string): void {
    const db = loadFile(this.path)
    if (!db.sessions[sessionId]) return
    delete db.sessions[sessionId]
    saveFile(this.path, db)
  }
}
