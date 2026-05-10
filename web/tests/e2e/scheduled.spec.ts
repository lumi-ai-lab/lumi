import { expect, test } from '@playwright/test'

import { installMockBackend } from './support/mock-backend'

test('creates a cron scheduled task and toggles mute', async ({ page }) => {
  const state = await installMockBackend(page, {
    sessions: [
      {
        id: 'sess-1',
        title: 'Project Chat',
        activeAgent: 'claude',
        workspaceId: 'ws-1',
        messageCount: 0,
        createdAt: Date.UTC(2026, 3, 24, 5, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 5, 0, 0),
      },
    ],
    sessionDetails: {
      'sess-1': {
        id: 'sess-1',
        title: 'Project Chat',
        activeAgent: 'claude',
        workspaceId: 'ws-1',
        createdAt: Date.UTC(2026, 3, 24, 5, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 5, 0, 0),
        messages: [],
      },
    },
  })

  await page.goto('/c')
  await page.getByRole('button', { name: 'Scheduled Tasks', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Scheduled Tasks' })).toBeVisible()
  await page.getByRole('button', { name: 'New Task' }).click()

  await page.getByLabel('Name').fill('Daily project status')
  await page.getByLabel('Description').fill('Daily project status')
  await page.getByLabel('Prompt').fill('Check project status and summarize')
  await page.getByText('Cron expr').click()
  await page.locator('input[value="0 8 * * *"]').fill('0 8 * * *')
  await page.getByLabel('Session Mode').selectOption('new_per_run')
  await page.getByLabel('Timeout Minutes').fill('15')
  await page.getByLabel('Silent').check()
  await page.getByRole('button', { name: 'Save' }).click()

  await expect(page.getByText('Daily project status')).toBeVisible()
  await expect(page.getByText('0 8 * * *')).toBeVisible()
  expect(state.cronCreateRequests[0]).toMatchObject({
    name: 'Daily project status',
    description: 'Daily project status',
    prompt: 'Check project status and summarize',
    conversationId: 'sess-1',
    schedule: { type: 'cron', cronExpr: '0 8 * * *' },
    sessionMode: 'new_per_run',
    timeoutMins: 15,
    silent: true,
  })

  await page.getByRole('button', { name: 'Mute', exact: true }).click()
  await expect.poll(() => state.cronUpdateRequests.length).toBe(1)
  expect(state.cronUpdateRequests[0]).toMatchObject({
    conversationId: 'sess-1',
    mute: true,
  })
  await expect(page.getByRole('button', { name: 'Unmute', exact: true })).toBeVisible()
})

test('keeps cron streamed result visible until persisted session catches up', async ({ page }) => {
  const state = await installMockBackend(page, {
    sessions: [
      {
        id: 'sess-1',
        title: 'Project Chat',
        activeAgent: 'claude',
        workspaceId: 'ws-1',
        messageCount: 0,
        createdAt: Date.UTC(2026, 3, 24, 5, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 5, 0, 0),
      },
    ],
    sessionDetails: {
      'sess-1': {
        id: 'sess-1',
        title: 'Project Chat',
        activeAgent: 'claude',
        workspaceId: 'ws-1',
        createdAt: Date.UTC(2026, 3, 24, 5, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 5, 0, 0),
        messages: [],
      },
    },
  })

  await page.goto('/c/sess-1')
  await expect(page.getByText('Project Chat')).toBeVisible()

  await page.evaluate(() => {
    window.dispatchEvent(new CustomEvent('mock-cron-event', {
      detail: {
        type: 'chat_event',
        payload: {
          conversationId: 'sess-1',
          event: 'update',
          data: {
            update: {
              sessionUpdate: 'agent_message_chunk',
              content: { type: 'text', text: 'Cron finished result' },
            },
          },
        },
      },
    }))
    window.dispatchEvent(new CustomEvent('mock-cron-event', {
      detail: {
        type: 'chat_event',
        payload: {
          conversationId: 'sess-1',
          event: 'done',
          data: { stopReason: 'end_turn' },
        },
      },
    }))
    window.dispatchEvent(new CustomEvent('mock-cron-event', {
      detail: {
        type: 'session_updated',
        payload: { conversationId: 'sess-1' },
      },
    }))
  })

  await expect(page.getByText('Cron finished result')).toBeVisible()
  expect(state.sessionDetails['sess-1'].messages).toHaveLength(0)
})

test('scheduled task card actions stay inside narrow cards', async ({ page }) => {
  await installMockBackend(page, {
    cronJobs: [
      {
        id: 'cron-narrow',
        name: 'Every two minutes greeting',
        enabled: true,
        channel: 'web',
        workspaceId: 'ws-1',
        agentId: 'claude',
        conversationId: 'sess-1',
        schedule: { type: 'cron', cronExpr: '*/2 * * * *' },
        prompt: 'Say hello',
        mute: false,
        state: {
          nextRunAt: Date.UTC(2026, 3, 24, 6, 0, 0),
          runCount: 0,
        },
        createdAt: Date.UTC(2026, 3, 24, 5, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 5, 0, 0),
      },
    ],
    sessions: [
      {
        id: 'sess-1',
        title: 'Project Chat',
        activeAgent: 'claude',
        workspaceId: 'ws-1',
        messageCount: 0,
        createdAt: Date.UTC(2026, 3, 24, 5, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 5, 0, 0),
      },
    ],
  })

  await page.setViewportSize({ width: 390, height: 800 })
  await page.goto('/scheduled')
  const card = page.locator('article').filter({ hasText: 'Every two minutes greeting' })
  await expect(card).toBeVisible()

  for (const label of ['Details', 'Pause', 'Mute', 'Run']) {
    const box = await card.getByRole('button', { name: label, exact: true }).boundingBox()
    const cardBox = await card.boundingBox()
    expect(box).not.toBeNull()
    expect(cardBox).not.toBeNull()
    expect(box!.x + box!.width).toBeLessThanOrEqual(cardBox!.x + cardBox!.width + 1)
  }
})
