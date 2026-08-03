import test from 'node:test'
import assert from 'node:assert/strict'
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { delimiter, join } from 'node:path'
import {
  defaultPiCommand,
  inspectPiSpawn,
  piProjectApprovalArgs,
  piSpawnEnvironmentSummary,
  shouldUseShellForPiCommand
} from '../../src/pi-rpc/command.js'

test('piProjectApprovalArgs: requires an explicit valid opt-in', () => {
  assert.deepEqual(piProjectApprovalArgs(), [])
  assert.deepEqual(piProjectApprovalArgs(''), [])
  assert.deepEqual(piProjectApprovalArgs(' false '), [])
  assert.deepEqual(piProjectApprovalArgs(' TRUE '), ['--approve'])
  assert.throws(() => piProjectApprovalArgs('yes'), /must be true or false/)
})

test('defaultPiCommand: uses pi.cmd on Windows and pi elsewhere', () => {
  const originalPlatform = process.platform

  try {
    Object.defineProperty(process, 'platform', { value: 'win32' })
    assert.equal(defaultPiCommand(), 'pi.cmd')

    Object.defineProperty(process, 'platform', { value: 'darwin' })
    assert.equal(defaultPiCommand(), 'pi')
  } finally {
    Object.defineProperty(process, 'platform', { value: originalPlatform })
  }
})

test('shouldUseShellForPiCommand: enables shell for Windows cmd launchers only', () => {
  const originalPlatform = process.platform
  Object.defineProperty(process, 'platform', { value: 'win32' })

  try {
    assert.equal(shouldUseShellForPiCommand('pi.cmd'), true)
    assert.equal(shouldUseShellForPiCommand('C:\\Users\\me\\AppData\\Roaming\\npm\\pi.CMD'), true)
    assert.equal(shouldUseShellForPiCommand('pi.bat'), true)
    assert.equal(shouldUseShellForPiCommand('pi'), false)
    assert.equal(shouldUseShellForPiCommand('C:\\tools\\pi.exe'), false)
  } finally {
    Object.defineProperty(process, 'platform', { value: originalPlatform })
  }
})

test('shouldUseShellForPiCommand: keeps shell disabled on non-Windows', () => {
  const originalPlatform = process.platform
  Object.defineProperty(process, 'platform', { value: 'darwin' })

  try {
    assert.equal(shouldUseShellForPiCommand('pi.cmd'), false)
    assert.equal(shouldUseShellForPiCommand('pi'), false)
  } finally {
    Object.defineProperty(process, 'platform', { value: originalPlatform })
  }
})

test('inspectPiSpawn: reports only safe spawn boundary state', () => {
  const originalPlatform = process.platform
  Object.defineProperty(process, 'platform', { value: 'linux' })
  try {
    const prefix = mkdtempSync(join(tmpdir(), 'pi-command-prefix-'))
    const binDir = join(prefix, 'bin')
    const command = join(binDir, 'pi')
    mkdirSync(binDir)
    writeFileSync(command, '#!/bin/sh\nexit 0\n', { mode: 0o755 })
    chmodSync(command, 0o755)

    const inspection = inspectPiSpawn('pi', prefix, {
      PATH: [binDir, '/usr/bin'].join(delimiter),
      HOME: '/sensitive/home'
    })
    assert.deepEqual(inspection, { commandAvailable: true, cwdAvailable: true, pathEntries: 2, homeSet: true })
    const summary = piSpawnEnvironmentSummary(inspection)
    assert.equal(summary, 'command available=true, cwd available=true, PATH entries=2, HOME set=true')
    assert.doesNotMatch(summary, /sensitive|pi-command-prefix/)

    rmSync(prefix, { recursive: true, force: true })
    assert.deepEqual(inspectPiSpawn(command, prefix, { PATH: '', HOME: '' }), {
      commandAvailable: false,
      cwdAvailable: false,
      pathEntries: 0,
      homeSet: false
    })
  } finally {
    Object.defineProperty(process, 'platform', { value: originalPlatform })
  }
})
