import type { SetupDependencyItem } from './setup-types'

export function getStatusIcon(status: SetupDependencyItem['status']): string {
  switch (status) {
    case 'ready':
      return '✓'
    case 'not_installed':
      return '○'
    case 'installing':
    case 'checking':
      return '◐'
    case 'error':
    case 'outdated':
    case 'missing':
      return '✗'
    case 'blocked':
      return '⊘'
    default:
      return '○'
  }
}

export function getStatusTone(status: SetupDependencyItem['status']): string {
  switch (status) {
    case 'ready':
      return 'success'
    case 'error':
    case 'outdated':
    case 'missing':
      return 'error'
    case 'installing':
    case 'checking':
      return 'pending'
    case 'blocked':
      return 'blocked'
    default:
      return ''
  }
}

export function isUrl(value?: string): boolean {
  return Boolean(value && (value.startsWith('http://') || value.startsWith('https://')))
}
