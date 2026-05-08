export function generateStaticParams() {
  return [{ jobId: 'placeholder' }]
}

import { ChatShell } from '@/features/chat/chat-shell'

export default function ScheduledJobPage() {
  return <ChatShell />
}

