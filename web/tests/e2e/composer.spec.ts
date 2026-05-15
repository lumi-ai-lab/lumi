import { expect, test } from '@playwright/test'

import { installMockBackend } from './support/mock-backend'

test('supports agent and file mentions from the composer', async ({ page }) => {
  const state = await installMockBackend(page, {
    workspaceFiles: [
      {
        path: 'src/features/chat/use-chat.ts',
        name: 'use-chat.ts',
        isDir: false,
      },
    ],
  })

  await page.goto('/c')

  const composer = page.getByRole('textbox', { name: /Message/ })

  await composer.fill('@co')
  await expect(page.getByText('Codex CLI')).toBeVisible()
  await page.getByText('Codex CLI').click()
  await expect(composer).toHaveValue('@codex ')

  await composer.fill('@src/features')
  await expect(page.getByText('src/features/chat/use-chat.ts')).toBeVisible()
  await page.getByText('src/features/chat/use-chat.ts').click()
  await expect(composer).toHaveValue('@src/features/chat/use-chat.ts ')

  await composer.fill('@qw')
  await expect(page.getByText('Qwen Code')).toBeVisible()
  await page.getByText('Qwen Code').click()
  await expect(composer).toHaveValue('@qwen ')

  await composer.fill('@qwen hello')
  await page.getByRole('button', { name: 'Send' }).click()
  await expect.poll(() => state.chatRequests.at(-1)?.message).toBe('@qwen hello')
  expect(state.chatRequests.at(-1)?.agentId).toBe('qwen')
})

test('supports slash command completion from the keyboard', async ({ page }) => {
  await installMockBackend(page)

  await page.goto('/c')

  const composer = page.getByRole('textbox', { name: /Message/ })
  await composer.fill('/pl')

  await expect(page.getByText('Available Commands')).toBeVisible()
  await page.keyboard.press('Enter')

  await expect(composer).toHaveValue('/plan ')
})

test('uploads files and carries them into the outgoing user message', async ({ page }) => {
  const state = await installMockBackend(page, {
    chatResponses: [
      buildUploadChatResponse(),
    ],
    uploadedFiles: [
      {
        name: 'brief.md',
        path: '/tmp/lumi/brief.md',
        size: 1536,
      },
    ],
  })

  await page.goto('/c')

  await page.locator('input[type="file"]').setInputFiles({
    name: 'brief.md',
    mimeType: 'text/markdown',
    buffer: Buffer.from('# Brief\n'),
  })

  await expect(page.getByText('brief.md')).toBeVisible()

  const composer = page.getByRole('textbox', { name: /Message/ })
  await composer.fill('Review the uploaded brief')
  await page.getByRole('button', { name: 'Send' }).click()

  expect(state.uploadRequests).toBe(1)
  await expect(page.getByText('Review the uploaded brief')).toBeVisible()
  await expect(page.getByRole('button', { name: /brief\.md/ })).toBeVisible()
})

function buildUploadChatResponse() {
  return [
    'data: {"conversationId":"sess-1","agent":"claude","sessionId":"agent-session-upload"}',
    '',
    'data: {"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Uploaded file received."}}}',
    '',
    'data: {"stopReason":"end_turn"}',
    '',
  ].join('\n')
}
