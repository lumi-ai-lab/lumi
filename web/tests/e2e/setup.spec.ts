import { expect, test } from '@playwright/test'

import { installMockBackend } from './support/mock-backend'

test('redirects chat traffic to setup when dependencies are not ready', async ({ page }) => {
  await installMockBackend(page, {
    setupReady: false,
    setupStatusSequence: [false],
    setupSubscribeEvents: [
      {
        ready: false,
        environment: [
          {
            name: 'Node.js',
            command: 'node -v',
            status: 'missing',
            message: 'Required',
          },
        ],
        agents: [],
        acpPackages: [],
      },
    ],
  })

  await page.goto('/c')

  await expect(page).toHaveURL(/\/setup$/)
  await expect(page.getByRole('heading', { name: 'Lumi Setup' })).toBeVisible()
  await expect(page.getByText('Node.js')).toBeVisible()
})

test('enters chat when the setup event stream reports ready', async ({ page }) => {
  await installMockBackend(page, {
    setupReady: true,
    setupSubscribeEvents: [
      {
        ready: true,
        environment: [
          {
            name: 'Node.js',
            command: 'node -v',
            status: 'ready',
            message: 'v22.0.0',
          },
        ],
        agents: [
          {
            name: 'Claude Code',
            command: 'npx -y @agentclientprotocol/claude-agent-acp@0.30.0',
            status: 'ready',
            message: 'Installed',
          },
        ],
        acpPackages: [
          {
            name: 'Qwen Code',
            package: '@qwen-code/qwen-code',
            status: 'ready',
            message: 'Cached',
          },
        ],
      },
    ],
  })

  await page.goto('/setup')

  const continueButton = page.getByRole('button', { name: 'Continue to Chat' })
  await expect(continueButton).toBeVisible()
  if (/\/setup$/.test(page.url())) {
    await continueButton.click({ timeout: 2000 }).catch(() => {})
  }
  await expect(page).toHaveURL(/\/c$/)
  await expect(page.getByText('Start chatting!')).toBeVisible()
})

test('shows Qwen package setup status', async ({ page }) => {
  await installMockBackend(page, {
    setupReady: false,
    setupSubscribeEvents: [],
  })

  await page.goto('/setup')
  await expect(page.getByRole('heading', { name: 'Lumi Setup' })).toBeVisible()
  await page.evaluate(() => {
    window.dispatchEvent(new CustomEvent('mock-cron-event', {
      detail: {
        type: 'message',
        payload: {
          ready: false,
          environment: [
            {
              name: 'Node.js',
              command: 'node -v',
              status: 'ready',
              message: 'v22.0.0',
            },
          ],
          agents: [
            {
              name: 'Qwen Code',
              command: 'qwen',
              status: 'missing',
              message: 'Not found',
              install: 'npm install -g @qwen-code/qwen-code',
            },
          ],
          acpPackages: [
            {
              name: 'Qwen Code',
              package: '@qwen-code/qwen-code',
              status: 'not_installed',
              message: 'Not installed',
              install: 'npm install -g @qwen-code/qwen-code',
            },
          ],
        },
      },
    }))
  })

  await expect(page.locator('section', { hasText: 'Agents' }).getByText('Qwen Code')).toBeVisible()
  await expect(page.locator('section', { hasText: 'ACP Packages' }).getByText('@qwen-code/qwen-code')).toBeVisible()
  await expect(page.getByText('npm install -g @qwen-code/qwen-code').first()).toBeVisible()
})
