import { expect, test } from '@playwright/test'

import { installMockBackend } from './support/mock-backend'

const baseUrl = 'https://ilinkai.weixin.qq.com'

test('manages WeChat settings inside the settings modal', async ({ page }) => {
  const state = await installMockBackend(page, {
    workspaces: [
      {
        id: 'ws-local-1',
        name: 'Local Alpha',
        path: '/tmp/local-alpha',
        kind: 'local',
      },
      {
        id: 'ws-remote-1',
        name: 'Remote Hidden',
        path: '/tmp/remote-hidden',
        kind: 'remote',
      },
      {
        id: 'ws-local-2',
        name: 'Local Beta',
        path: '/tmp/local-beta',
        kind: 'local',
      },
    ],
    defaultWorkspace: 'ws-local-1',
    wechatBotToken: 'wechat-secret-token',
    wechatConfig: {
      enabled: false,
      loginMode: 'manual',
      accountId: 'wx_manual_old',
      baseUrl,
      workspaceId: 'ws-local-1',
      agentId: 'claude',
      hasToken: true,
      maskedToken: 'wech********oken',
    },
    wechatStatus: {
      running: false,
      configured: true,
      configError: '',
      lastError: '',
      lastSyncAt: Date.UTC(2026, 3, 28, 6, 30, 0),
      lastLoginAt: Date.UTC(2026, 3, 28, 6, 20, 0),
      lastMessageAt: 0,
    },
    wechatLoginEvents: [
      {
        event: 'qr',
        data: {
          ticket: 'wxlogin_ticket_42',
          imageUrl: 'data:text/html,<html><body>Mock WeChat QR Page</body></html>',
        },
      },
      {
        event: 'scanned',
        data: {},
      },
      {
        event: 'confirmed',
        data: {
          accountId: 'wx_qr_bound',
          baseUrl,
          hasToken: true,
        },
      },
      {
        event: 'done',
        data: {},
      },
    ],
  })

  state.wechatTestResult = {
    success: false,
    error: 'wechat probe failed',
  }

  await page.goto('/c')

  await page.getByRole('button', { name: 'Settings' }).click()
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()
  await expect(page.getByText('WeChat', { exact: true })).toBeVisible()

  const wechatSection = page.locator('section').filter({
    has: page.getByRole('heading', { name: 'WeChat', exact: true }),
  })
  const workspaceSelect = wechatSection.getByLabel('Workspace')
  await expect(workspaceSelect.locator('option')).toHaveCount(3)
  await expect(workspaceSelect.locator('option')).toHaveText([
    'Select a local workspace',
    'Local Alpha',
    'Local Beta',
  ])

  await wechatSection.getByRole('button', { name: 'Show' }).click()
  await wechatSection.getByLabel('Account ID').fill('wx_manual_updated')
  await wechatSection.getByLabel('Base URL').fill('https://wechat.example.com')
  await wechatSection.getByLabel('Login Mode').selectOption('manual')
  await workspaceSelect.selectOption('ws-local-2')
  await wechatSection.getByLabel('Agent').selectOption('codex')
  await wechatSection.getByRole('button', { name: 'Save', exact: true }).click()

  await expect.poll(() => state.wechatSaveRequests.length).toBe(1)
  expect(state.wechatSaveRequests[0]).toMatchObject({
    enabled: false,
    loginMode: 'manual',
    accountId: 'wx_manual_updated',
    baseUrl: 'https://wechat.example.com',
    workspaceId: 'ws-local-2',
    agentId: 'codex',
  })
  expect(state.wechatSaveRequests[0]).not.toHaveProperty('botToken')

  await wechatSection.getByRole('button', { name: 'Test connection', exact: true }).click()
  await expect(page.getByText('wechat probe failed')).toBeVisible()
  expect(state.wechatTestRequests).toBe(1)

  state.wechatTestResult = {
    success: true,
    message: 'connection ok',
  }

  await wechatSection.getByRole('button', { name: 'Start QR Login' }).click()
  await expect(page.getByTestId('wechat-qr-code').locator('svg')).toBeVisible()
  await expect(page.getByText('Scan this QR code with WeChat. It encodes the official WeChat login page.')).toBeVisible()
  await expect(page.getByText('wxlogin_ticket_42')).toBeVisible()
  await expect.poll(async () => wechatSection.getByLabel('Account ID').inputValue()).toBe('wx_qr_bound')

  await wechatSection.getByLabel('Agent').selectOption('claude')
  await wechatSection.getByRole('button', { name: 'Enable', exact: true }).click()
  await expect.poll(() => state.wechatSaveRequests.length).toBe(2)
  expect(state.wechatSaveRequests[1]).toMatchObject({
    agentId: 'claude',
  })
  await expect.poll(() => state.wechatEnableRequests).toBe(1)
  await expect(page.locator('span').filter({ hasText: 'Running' }).first()).toBeVisible()

  await wechatSection.getByRole('button', { name: 'Disable', exact: true }).click()
  await expect.poll(() => state.wechatDisableRequests).toBe(1)
  await expect(page.locator('span').filter({ hasText: 'Stopped' }).first()).toBeVisible()
})
