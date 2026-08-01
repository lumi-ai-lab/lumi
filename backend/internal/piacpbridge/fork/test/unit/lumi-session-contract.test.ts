import assert from 'node:assert/strict'
import { existsSync, mkdtempSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { PiAcpAgent } from '../../src/acp/agent.js'
import { SessionManager } from '../../src/acp/session.js'
import { SessionStore } from '../../src/acp/session-store.js'
import { instructionProfileDigest } from '../../src/lumi/session-instructions.js'
import { PiRpcProcess } from '../../src/pi-rpc/process.js'
import { FakeAgentSideConnection, asAgentConn } from '../helpers/fakes.js'

function profileMeta(baseInstructions = 'stable protocol', sessionContext = 'session context') {
  return {
    lumi: {
      sessionInstructions: {
        schemaVersion: 1,
        baseInstructions,
        sessionContext,
        profileDigest: instructionProfileDigest(baseInstructions, sessionContext)
      }
    }
  }
}

function fakeRpcProcess(overrides: Record<string, unknown> = {}) {
  return {
    onEvent: () => () => {},
    getState: async () => ({
      sessionId: 'logical-session',
      sessionFile: '/tmp/lumi-pi-session.jsonl',
      thinkingLevel: 'medium',
      model: { provider: 'test', id: 'model' }
    }),
    getAvailableModels: async () => ({ models: [{ provider: 'test', id: 'model', name: 'Model' }] }),
    getMessages: async () => ({ messages: [] }),
    getCommands: async () => ({ commands: [] }),
    ...overrides
  } as any
}

test('initialize advertises the complete Lumi instruction transport capability', async () => {
  const agent = new PiAcpAgent(asAgentConn(new FakeAgentSideConnection()))
  const response = await agent.initialize({ protocolVersion: 1 } as any)

  assert.deepEqual((response as any)._meta?.lumi?.sessionInstructions, {
    transportVersion: 1,
    systemPromptAppend: true,
    rehydrateOnRestore: true,
    turnContext: true
  })
})

test('session/new passes the validated profile to PI and persists only its digest', async () => {
  const spawnCalls: any[] = []
  const storeUpserts: any[] = []
  const originalSpawn = PiRpcProcess.spawn
  ;(PiRpcProcess as any).spawn = async (params: any) => {
    spawnCalls.push(params)
    return fakeRpcProcess()
  }

  try {
    const manager = new SessionManager()
    ;(manager as any).store = { upsert: (entry: any) => storeUpserts.push(entry) }

    const profile = profileMeta().lumi.sessionInstructions
    await manager.create({
      cwd: '/tmp/lumi-project',
      mcpServers: [],
      conn: asAgentConn(new FakeAgentSideConnection()),
      instructionProfile: profile
    } as any)

    assert.equal(spawnCalls.length, 1)
    assert.equal(spawnCalls[0].systemPromptAppend, 'stable protocol\n\nsession context')
    assert.equal(spawnCalls[0].systemPromptAppend.includes(profile.profileDigest), false)
    assert.deepEqual(storeUpserts, [
      {
        sessionId: 'logical-session',
        cwd: '/tmp/lumi-project',
        sessionFile: '/tmp/lumi-pi-session.jsonl',
        instructionProfileDigest: profile.profileDigest,
        requiresInstructionProfile: true
      }
    ])
  } finally {
    PiRpcProcess.spawn = originalSpawn
  }
})

test('explicit load and implicit restore both rehydrate PI with the current profile', async () => {
  const spawnCalls: any[] = []
  const originalSpawn = PiRpcProcess.spawn
  ;(PiRpcProcess as any).spawn = async (params: any) => {
    spawnCalls.push(params)
    return fakeRpcProcess()
  }

  const stored = {
    sessionId: 'logical-session',
    cwd: '/tmp/lumi-project',
    sessionFile: '/tmp/lumi-pi-session.jsonl',
    updatedAt: new Date().toISOString(),
    instructionProfileDigest: profileMeta().lumi.sessionInstructions.profileDigest,
    requiresInstructionProfile: true
  }
  const entries: any[] = []
  const store = {
    get: (sessionId: string) => (sessionId === stored.sessionId ? stored : null),
    upsert: (entry: any) => entries.push(entry)
  }

  try {
    const explicit = new PiAcpAgent(asAgentConn(new FakeAgentSideConnection()))
    ;(explicit as any).store = store
    await explicit.loadSession({
      sessionId: stored.sessionId,
      cwd: stored.cwd,
      mcpServers: [],
      _meta: profileMeta()
    } as any)

    const implicit = new PiAcpAgent(asAgentConn(new FakeAgentSideConnection()))
    ;(implicit as any).store = store
    ;(implicit as any).sessions = {
      maybeGet: () => undefined,
      getOrCreate: (sessionId: string, params: any) => ({
        sessionId,
        cwd: params.cwd,
        proc: params.proc,
        prompt: async () => 'end_turn'
      })
    }
    const response = await implicit.prompt({
      sessionId: stored.sessionId,
      prompt: [{ type: 'text', text: 'actual question' }],
      _meta: profileMeta()
    } as any)

    assert.equal(response.stopReason, 'end_turn')
    assert.equal(spawnCalls.length, 2)
    for (const call of spawnCalls) {
      assert.equal(call.sessionPath, stored.sessionFile)
      assert.equal(call.systemPromptAppend, 'stable protocol\n\nsession context')
    }
    assert.ok(entries.every(entry => !JSON.stringify(entry).includes('stable protocol')))
  } finally {
    PiRpcProcess.spawn = originalSpawn
  }
})

test('a changed digest rebuilds the live logical session before prompting', async () => {
  const spawnCalls: any[] = []
  const closeCalls: string[] = []
  const originalSpawn = PiRpcProcess.spawn
  ;(PiRpcProcess as any).spawn = async (params: any) => {
    spawnCalls.push(params)
    return fakeRpcProcess()
  }

  const current = profileMeta('new stable protocol', 'session context')
  const stored = {
    sessionId: 'logical-session',
    cwd: '/tmp/lumi-project',
    sessionFile: '/tmp/lumi-pi-session.jsonl',
    updatedAt: new Date().toISOString(),
    instructionProfileDigest: instructionProfileDigest('old stable protocol', 'session context'),
    requiresInstructionProfile: true
  }
  const live = { sessionId: stored.sessionId }

  try {
    const agent = new PiAcpAgent(asAgentConn(new FakeAgentSideConnection()))
    ;(agent as any).store = {
      get: () => stored,
      upsert: () => {}
    }
    ;(agent as any).sessions = {
      maybeGet: () => live,
      close: (sessionId: string) => closeCalls.push(sessionId),
      getOrCreate: (sessionId: string, params: any) => ({
        sessionId,
        cwd: params.cwd,
        proc: params.proc,
        prompt: async () => 'end_turn'
      })
    }

    await agent.prompt({
      sessionId: stored.sessionId,
      prompt: [{ type: 'text', text: 'actual question' }],
      _meta: current
    } as any)

    assert.deepEqual(closeCalls, [stored.sessionId])
    assert.equal(spawnCalls.length, 1)
    assert.equal(spawnCalls[0].systemPromptAppend, 'new stable protocol\n\nsession context')
  } finally {
    PiRpcProcess.spawn = originalSpawn
  }
})

test('restore fails closed when a protected session omits its profile', async () => {
  const agent = new PiAcpAgent(asAgentConn(new FakeAgentSideConnection()))
  ;(agent as any).store = {
    get: () => ({
      sessionId: 'logical-session',
      cwd: '/tmp/lumi-project',
      sessionFile: '/tmp/lumi-pi-session.jsonl',
      updatedAt: new Date().toISOString(),
      requiresInstructionProfile: true
    })
  }
  ;(agent as any).sessions = { maybeGet: () => undefined }

  await assert.rejects(
    () =>
      agent.prompt({
        sessionId: 'logical-session',
        prompt: [{ type: 'text', text: 'actual question' }]
      } as any),
    (error: any) => error?.code === -32602 && /instruction profile is required/i.test(String(error?.data ?? ''))
  )
})

test('SessionStore never serializes instruction bodies or turn context', () => {
  const root = mkdtempSync(join(tmpdir(), 'lumi-session-store-'))
  const path = join(root, 'session-map.json')
  const store = new SessionStore(path)
  const digest = instructionProfileDigest('sensitive stable text', 'sensitive session text')

  store.upsert({
    sessionId: 'logical-session',
    cwd: '/redacted/workspace',
    sessionFile: '/redacted/session.jsonl',
    instructionProfileDigest: digest,
    requiresInstructionProfile: true,
    baseInstructions: 'sensitive stable text',
    sessionContext: 'sensitive session text',
    turnContext: 'sensitive turn text'
  } as any)

  const serialized = readFileSync(path, 'utf-8')
  assert.ok(serialized.includes(digest))
  assert.equal(serialized.includes('sensitive'), false)
  if (process.platform !== 'win32') {
    assert.equal(statSync(path).mode & 0o777, 0o600)
    assert.equal(statSync(dirname(path)).mode & 0o777, 0o700)
  }
})

test('PI native transport uses a 0600 file, keeps its body out of argv, and removes it after handshake', async () => {
  const root = mkdtempSync(join(tmpdir(), 'lumi-pi-transport-'))
  const executable = join(root, 'fake-pi.cjs')
  const observation = join(root, 'observation.json')
  const sentinel = 'PRIVATE-SYSTEM-INSTRUCTION-SENTINEL'

  writeFileSync(
    executable,
    `#!/usr/bin/env node
const fs = require('node:fs')
const readline = require('node:readline')
const index = process.argv.indexOf('--append-system-prompt')
const instructionPath = index >= 0 ? process.argv[index + 1] : ''
const stat = fs.statSync(instructionPath)
fs.writeFileSync(process.env.LUMI_PI_TEST_OBSERVATION, JSON.stringify({
  instructionPath,
  mode: stat.mode & 0o777,
  argv: process.argv.slice(2)
}))
const input = readline.createInterface({ input: process.stdin })
input.on('line', line => {
  const request = JSON.parse(line)
  if (request.type === 'get_state') {
    process.stdout.write(JSON.stringify({ type: 'response', id: request.id, command: 'get_state', success: true, data: {} }) + '\\n')
  }
})
setInterval(() => {}, 1000)
`,
    { encoding: 'utf-8', mode: 0o700 }
  )

  const previousObservation = process.env.LUMI_PI_TEST_OBSERVATION
  process.env.LUMI_PI_TEST_OBSERVATION = observation
  let proc: PiRpcProcess | null = null
  try {
    proc = await PiRpcProcess.spawn({
      cwd: root,
      piCommand: executable,
      systemPromptAppend: sentinel
    })

    const recorded = JSON.parse(readFileSync(observation, 'utf-8')) as {
      instructionPath: string
      mode: number
      argv: string[]
    }
    assert.equal(recorded.mode, 0o600)
    assert.equal(recorded.argv.includes(sentinel), false)
    assert.equal(existsSync(recorded.instructionPath), false)
    assert.equal(existsSync(dirname(recorded.instructionPath)), false)
  } finally {
    proc?.dispose()
    if (previousObservation === undefined) delete process.env.LUMI_PI_TEST_OBSERVATION
    else process.env.LUMI_PI_TEST_OBSERVATION = previousObservation
  }
})

test('PI native transport removes its temporary file when the RPC handshake times out', async () => {
  const root = mkdtempSync(join(tmpdir(), 'lumi-pi-timeout-'))
  const executable = join(root, 'fake-pi-timeout.cjs')
  const observation = join(root, 'instruction-path.txt')
  const sentinel = 'PRIVATE-TIMEOUT-SYSTEM-INSTRUCTION'

  writeFileSync(
    executable,
    `#!/usr/bin/env node
const fs = require('node:fs')
const index = process.argv.indexOf('--append-system-prompt')
fs.writeFileSync(process.env.LUMI_PI_TEST_OBSERVATION, process.argv[index + 1])
setInterval(() => {}, 1000)
`,
    { encoding: 'utf-8', mode: 0o700 }
  )

  const previousObservation = process.env.LUMI_PI_TEST_OBSERVATION
  process.env.LUMI_PI_TEST_OBSERVATION = observation
  try {
    await assert.rejects(
      () =>
        PiRpcProcess.spawn({
          cwd: root,
          piCommand: executable,
          systemPromptAppend: sentinel,
          handshakeTimeoutMs: 500
        }),
      (error: any) =>
        /Could not confirm that pi loaded the system instructions/.test(String(error?.message ?? error)) &&
        !String(error?.message ?? error).includes(sentinel)
    )
    const instructionPath = readFileSync(observation, 'utf-8')
    assert.equal(existsSync(instructionPath), false)
    assert.equal(existsSync(dirname(instructionPath)), false)
  } finally {
    if (previousObservation === undefined) delete process.env.LUMI_PI_TEST_OBSERVATION
    else process.env.LUMI_PI_TEST_OBSERVATION = previousObservation
  }
})

test('PI RPC rejects writes after disposal without an uncaught EPIPE', async () => {
  const root = mkdtempSync(join(tmpdir(), 'lumi-pi-dispose-'))
  const executable = join(root, 'fake-pi-dispose.cjs')

  writeFileSync(
    executable,
    `#!/usr/bin/env node
const readline = require('node:readline')
const input = readline.createInterface({ input: process.stdin })
input.on('line', line => {
  const request = JSON.parse(line)
  if (request.type === 'get_state') {
    process.stdout.write(JSON.stringify({ type: 'response', id: request.id, command: 'get_state', success: true, data: {} }) + '\\n')
  }
})
setInterval(() => {}, 1000)
`,
    { encoding: 'utf-8', mode: 0o700 }
  )

  const proc = await PiRpcProcess.spawn({ cwd: root, piCommand: executable })
  proc.dispose()

  await assert.rejects(() => proc.getCommands(), /pi process disposed/)
})
