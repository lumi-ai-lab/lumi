import test from 'node:test'
import assert from 'node:assert/strict'
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { delimiter, join } from 'node:path'
import { PiRpcProcess } from '../../src/pi-rpc/process.js'

function fakePi(path: string): void {
  writeFileSync(
    path,
    `#!${process.execPath}
import * as readline from 'node:readline'
const rl = readline.createInterface({ input: process.stdin })
rl.on('line', line => {
  const request = JSON.parse(line)
  const data = request.type === 'get_state'
    ? { sessionFile: '/tmp/fake-session.jsonl', model: null, thinkingLevel: 'off', argv: process.argv.slice(2) }
    : {}
  process.stdout.write(JSON.stringify({ type: 'response', id: request.id, command: request.type, success: true, data }) + '\\n')
})
`,
    { mode: 0o755 }
  )
  chmodSync(path, 0o755)
}

test('PiRpcProcess.spawn: forwards explicit project approval without changing the default', async () => {
  const root = mkdtempSync(join(tmpdir(), 'pi-rpc-approval-'))
  const command = join(root, 'pi')
  fakePi(command)
  const previous = process.env.PI_ACP_APPROVE_PROJECT
  try {
    delete process.env.PI_ACP_APPROVE_PROJECT
    const defaultProc = await PiRpcProcess.spawn({ cwd: root, piCommand: command })
    const defaultState = (await defaultProc.getState()) as any
    assert.equal(defaultState.argv.includes('--approve'), false)
    defaultProc.dispose()

    process.env.PI_ACP_APPROVE_PROJECT = 'true'
    const approvedProc = await PiRpcProcess.spawn({ cwd: root, piCommand: command })
    const approvedState = (await approvedProc.getState()) as any
    assert.equal(approvedState.argv.includes('--approve'), true)
    approvedProc.dispose()

    process.env.PI_ACP_APPROVE_PROJECT = 'invalid-sensitive-value'
    await assert.rejects(
      () => PiRpcProcess.spawn({ cwd: root, piCommand: command }),
      (error: any) => {
        const message = String(error?.message ?? '')
        return message.includes('invalid project approval configuration') && !message.includes('sensitive')
      }
    )
  } finally {
    if (previous == null) delete process.env.PI_ACP_APPROVE_PROJECT
    else process.env.PI_ACP_APPROVE_PROJECT = previous
    rmSync(root, { recursive: true, force: true })
  }
})

test('PiRpcProcess.spawn: launches pi from an npm-prefix PATH without a shell', async () => {
  const root = mkdtempSync(join(tmpdir(), 'pi-rpc-path-'))
  const bin = join(root, 'bin')
  mkdirSync(bin)
  fakePi(join(bin, 'pi'))
  const previousPath = process.env.PATH
  process.env.PATH = [bin, previousPath ?? ''].filter(Boolean).join(delimiter)
  try {
    const proc = await PiRpcProcess.spawn({ cwd: root })
    proc.dispose()
    const customProc = await PiRpcProcess.spawn({ cwd: root, piCommand: join(bin, 'pi') })
    customProc.dispose()
  } finally {
    if (previousPath == null) delete process.env.PATH
    else process.env.PATH = previousPath
    rmSync(root, { recursive: true, force: true })
  }
})

test('PiRpcProcess.spawn: preserves permission-denied classification without disclosing paths', async () => {
  const root = mkdtempSync(join(tmpdir(), 'pi-rpc-eacces-'))
  const command = join(root, 'pi-private')
  writeFileSync(command, '#!/bin/sh\nexit 0\n', { mode: 0o600 })
  try {
    await assert.rejects(
      () => PiRpcProcess.spawn({ cwd: root, piCommand: command }),
      (error: any) => {
        const message = String(error?.message ?? '')
        return error?.code === 'EACCES' && message.includes('permission denied') && !message.includes(root)
      }
    )
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('PiRpcProcess.spawn: reports a missing PATH command as executable not found', async () => {
  const root = mkdtempSync(join(tmpdir(), 'pi-rpc-enoent-'))
  const previousPath = process.env.PATH
  process.env.PATH = ''
  try {
    await assert.rejects(
      () => PiRpcProcess.spawn({ cwd: root, piCommand: 'pi-missing-for-test' }),
      (error: any) =>
        error?.code === 'ENOENT' &&
        String(error?.message ?? '').includes('executable not found') &&
        String(error?.message ?? '').includes('PATH entries=0')
    )
  } finally {
    if (previousPath == null) delete process.env.PATH
    else process.env.PATH = previousPath
    rmSync(root, { recursive: true, force: true })
  }
})

test('PiRpcProcess.spawn: does not misreport an invalid cwd as a missing executable', async () => {
  const root = mkdtempSync(join(tmpdir(), 'pi-rpc-cwd-'))
  const command = join(root, 'pi')
  const missingCwd = join(root, 'missing-workspace')
  fakePi(command)
  try {
    await assert.rejects(
      () => PiRpcProcess.spawn({ cwd: missingCwd, piCommand: command }),
      (error: any) => {
        const message = String(error?.message ?? '')
        return (
          error?.code === 'ENOENT' &&
          message.includes('working directory unavailable') &&
          !message.includes('executable not found') &&
          !message.includes(root)
        )
      }
    )
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
