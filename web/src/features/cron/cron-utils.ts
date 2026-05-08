import type { CronJob, CronSchedule } from '@/lib/types'

export function formatCronSchedule(schedule: CronSchedule) {
  if (schedule.type === 'once') {
    return `Once at ${new Date(schedule.runAt).toLocaleString()}`
  }
  return `Every ${Math.round(schedule.everySeconds / 60)} min`
}

export function formatCronTime(value?: number) {
  if (!value) return '-'
  return new Date(value).toLocaleString()
}

export function cronStatus(job: CronJob) {
  if (!job.enabled) return 'paused'
  if (job.state.lastStatus === 'error') return 'error'
  return 'active'
}

export function statusDotClass(status: string) {
  if (status === 'error') return 'bg-destructive'
  if (status === 'paused') return 'bg-amber-500'
  if (status === 'active') return 'bg-emerald-500'
  return 'bg-muted-foreground'
}

