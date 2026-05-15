import { expect, test } from '@playwright/test'

import { installMockBackend } from './support/mock-backend'

test('updates language, theme, and agent mode from settings', async ({ page }) => {
  const state = await installMockBackend(page)

  await page.goto('/c')

  await page.getByRole('button', { name: 'Settings' }).click()
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()
  await expect(page.locator('span', { hasText: 'Qwen Code' }).first()).toBeVisible()

  await page.getByRole('button', { name: '中文' }).click()
  await expect(page.getByRole('heading', { name: '设置', exact: true })).toBeVisible()

  const initialTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
  await page.getByRole('button', { name: /模式/ }).click()
  const nextTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
  expect(nextTheme).not.toBe(initialTheme)

  await page.getByRole('button', { name: 'Full Auto', exact: true }).click()
  expect(state.agentUpdateRequests).toContainEqual({
    agentId: 'codex',
    sessionMode: 'yolo',
  })
})
