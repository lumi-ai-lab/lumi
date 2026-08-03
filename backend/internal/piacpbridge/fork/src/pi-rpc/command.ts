import { constants, accessSync, statSync } from 'node:fs'
import { platform } from 'node:os'
import { delimiter, isAbsolute, resolve, sep } from 'node:path'

export function defaultPiCommand(): string {
  return platform() === 'win32' ? 'pi.cmd' : 'pi'
}

export function getPiCommand(override?: string): string {
  return override ?? defaultPiCommand()
}

export function piProjectApprovalArgs(value?: string): string[] {
  const normalized = value?.trim().toLowerCase()
  if (!normalized || normalized === 'false') return []
  if (normalized === 'true') return ['--approve']
  throw new Error('PI_ACP_APPROVE_PROJECT must be true or false')
}

export type PiSpawnInspection = {
  commandAvailable: boolean
  cwdAvailable: boolean
  pathEntries: number
  homeSet: boolean
}

function isAccessibleDirectory(path: string): boolean {
  try {
    if (!statSync(path).isDirectory()) return false
    accessSync(path, constants.X_OK)
    return true
  } catch {
    return false
  }
}

function isExecutable(path: string): boolean {
  try {
    if (!statSync(path).isFile()) return false
    accessSync(path, platform() === 'win32' ? constants.F_OK : constants.X_OK)
    return true
  } catch {
    return false
  }
}

export function inspectPiSpawn(command: string, cwd: string, env: NodeJS.ProcessEnv = process.env): PiSpawnInspection {
  const pathEntries = env.PATH ? env.PATH.split(delimiter).filter(Boolean).length : 0
  const cwdAvailable = isAccessibleDirectory(cwd)
  let commandAvailable = false

  if (isAbsolute(command) || command.includes(sep) || command.includes('/')) {
    commandAvailable = isExecutable(isAbsolute(command) ? command : resolve(cwd, command))
  } else if (env.PATH) {
    commandAvailable = env.PATH.split(delimiter).some(entry => {
      const base = !entry ? cwd : isAbsolute(entry) ? entry : resolve(cwd, entry)
      return isExecutable(resolve(base, command))
    })
  }

  return {
    commandAvailable,
    cwdAvailable,
    pathEntries,
    homeSet: Boolean(env.HOME?.trim())
  }
}

export function piSpawnEnvironmentSummary(inspection: PiSpawnInspection): string {
  return `command available=${inspection.commandAvailable}, cwd available=${inspection.cwdAvailable}, PATH entries=${inspection.pathEntries}, HOME set=${inspection.homeSet}`
}

export function shouldUseShellForPiCommand(cmd: string): boolean {
  if (platform() !== 'win32') return false

  const normalized = cmd.trim().toLowerCase()
  return normalized.endsWith('.cmd') || normalized.endsWith('.bat')
}
