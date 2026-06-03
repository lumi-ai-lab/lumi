export interface SetupDependencyItem {
  name: string
  command?: string
  package?: string
  status:
    | 'checking'
    | 'ready'
    | 'missing'
    | 'outdated'
    | 'not_installed'
    | 'installing'
    | 'error'
    | 'blocked'
  message?: string
  install?: string
}

export interface SetupSnapshot {
  ready: boolean
  environment?: SetupDependencyItem[]
  agents?: SetupDependencyItem[]
  acpPackages?: SetupDependencyItem[]
}

export interface SetupInstallEvent {
  type?: 'agent' | 'acp'
  index?: number
  status?: SetupDependencyItem['status']
  message?: string
  success?: boolean
  error?: string
}
