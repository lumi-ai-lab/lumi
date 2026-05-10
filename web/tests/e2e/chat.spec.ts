import { expect, test } from '@playwright/test'

import { buildSseStream, installMockBackend } from './support/mock-backend'

test('restores a session from a deep link', async ({ page }) => {
  await installMockBackend(page, {
    sessions: [
      {
        id: 'sess-42',
        title: 'Deep Link Session',
        activeAgent: 'claude',
        workspaceId: 'ws-1',
        messageCount: 2,
        createdAt: Date.UTC(2026, 3, 23, 3, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 3, 30, 0),
      },
    ],
    sessionDetails: {
      'sess-42': {
        id: 'sess-42',
        title: 'Deep Link Session',
        activeAgent: 'claude',
        workspaceId: 'ws-1',
        createdAt: Date.UTC(2026, 3, 23, 3, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 3, 30, 0),
        messages: [
          {
            role: 'user',
            content: 'Review the routing changes',
          },
          {
            role: 'assistant',
            content: 'Loaded from the deep link.',
            agent: 'claude',
          },
        ],
      },
    },
  })

  await page.goto('/c/sess-42')

  await expect(page).toHaveURL(/\/c\/sess-42$/)
  await expect(page.getByText('Review the routing changes')).toBeVisible()
  await expect(page.getByText('Loaded from the deep link.')).toBeVisible()
})

test('creates a session and renders streamed tool activity plus assistant output', async ({ page }) => {
  const state = await installMockBackend(page, {
    chatResponses: [
      buildSseStream([
        {
          data: {
            conversationId: 'sess-1',
            agent: 'claude',
            sessionId: 'agent-session-1',
          },
        },
        {
          event: 'tool_call',
          data: {
            toolCallId: 'tool-1',
            toolName: 'exec_command',
            title: 'npm test',
            description: 'Running the regression suite',
            status: 'pending',
            input: 'npm test -- --runInBand',
          },
        },
        {
          event: 'tool_call',
          data: {
            toolCallId: 'tool-1',
            toolName: 'exec_command',
            title: 'npm test',
            description: 'Running the regression suite',
            status: 'completed',
            input: 'npm test -- --runInBand',
            output: 'All tests passed',
          },
        },
        {
          data: {
            update: {
              sessionUpdate: 'agent_message_chunk',
              content: {
                type: 'text',
                text: 'Regression suite is green.',
              },
            },
          },
        },
        {
          data: {
            stopReason: 'end_turn',
          },
        },
      ]),
    ],
  })

  await page.goto('/c')

  const composer = page.getByRole('textbox', { name: /Message/ })
  await composer.fill('Run the regression suite')
  await page.getByRole('button', { name: 'Send' }).click()

  await expect(page).toHaveURL(/\/c\/sess-1$/)
  await expect(page.getByText('Run the regression suite')).toBeVisible()
  await expect(page.getByText('exec_command')).toBeVisible()
  await expect(page.getByText('All tests passed')).toBeVisible()
  await expect(page.getByText('Regression suite is green.')).toBeVisible()

  expect(state.chatRequests).toHaveLength(1)
  expect(state.chatRequests[0]?.message).toBe('Run the regression suite')
})

test('shows permission requests and confirms them through the API', async ({ page }) => {
  const state = await installMockBackend(page, {
    chatResponses: [
      buildSseStream([
        {
          data: {
            conversationId: 'sess-1',
            agent: 'claude',
            sessionId: 'agent-session-2',
          },
        },
        {
          data: {
            sessionId: 'sess-1',
            options: [
              {
                optionId: 'allow-once',
                name: 'Allow once',
                kind: 'allow_once',
              },
              {
                optionId: 'reject-once',
                name: 'Reject',
                kind: 'reject_once',
              },
            ],
            toolCall: {
              toolCallId: 'tool-2',
              title: 'Write file',
              kind: 'edit',
              rawInput: {
                file_path: '/tmp/lumi/README.md',
              },
            },
          },
        },
      ]),
    ],
  })

  await page.goto('/c')

  const composer = page.getByRole('textbox', { name: /Message/ })
  await composer.fill('Apply the patch')
  await page.getByRole('button', { name: 'Send' }).click()

  await expect(page.getByText('Permission Required')).toBeVisible()
  await expect(page.getByText('Write file')).toBeVisible()

  await page.getByRole('button', { name: 'Allow once' }).click()

  await expect(page.getByText('Permission Required')).toHaveCount(0)
  expect(state.permissionConfirmations).toEqual([
    {
      agentId: 'claude',
      toolCallId: 'tool-2',
      optionId: 'allow-once',
    },
  ])
})

test('shows codex permission request while a newly created session is still sending', async ({ page }) => {
  const state = await installMockBackend(page, {
    chatResponses: [
      buildSseStream([
        {
          data: {
            conversationId: 'sess-1',
            agent: 'codex',
            sessionId: 'codex-session-1',
          },
        },
        {
          data: {
            sessionId: 'codex-session-1',
            options: [
              {
                optionId: 'approved',
                name: 'Approve',
                kind: 'allow_once',
              },
              {
                optionId: 'denied',
                name: 'Deny',
                kind: 'reject_once',
              },
            ],
            toolCall: {
              toolCallId: 'codex-tool-1',
              title: 'Run shell command',
              kind: 'shell',
              rawInput: {
                command: 'lumi-cli cron add --cron "*/2 * * * *" --prompt "hello" --desc "hello"',
              },
            },
          },
        },
      ]),
    ],
  })

  await page.goto('/c')

  const composer = page.getByRole('textbox', { name: /Message/ })
  await composer.fill('@codex Create a task every two minutes')
  await page.getByRole('button', { name: 'Send' }).click()

  await expect(page.getByText('Permission Required')).toBeVisible()
  await expect(page.getByText('Run shell command')).toBeVisible()

  await page.getByRole('button', { name: 'Approve' }).click()

  expect(state.permissionConfirmations).toEqual([
    {
      agentId: 'codex',
      toolCallId: 'codex-tool-1',
      optionId: 'approved',
    },
  ])
})

test('hides the code preview header for yaml files', async ({ page }) => {
  await installMockBackend(page, {
    workspaceFiles: [
      {
        path: 'config/app.yml',
        name: 'app.yml',
        isDir: false,
      },
    ],
  })

  await page.goto('/c')

  await page.getByRole('button', { name: 'app.yml' }).click()

  await expect(page.getByText('name: preview-test')).toBeVisible()
  await expect(page.getByText(/^YAML$/)).toHaveCount(0)
  await expect(page.getByText(/^[0-9]+ lines$/i)).toHaveCount(0)
})

test('creates a remote workspace from the wizard and sends chat with deviceId', async ({ page }) => {
  const state = await installMockBackend(page, {
    devices: [
      {
        id: 'dev-1',
        name: 'MacBook Pro',
        alias: 'Office Mac',
        displayName: 'Office Mac',
        status: 'setup_required',
        setupReady: false,
        setupStatus: {
          ready: false,
          environment: [
            {
              name: 'npm',
              command: 'npm -v',
              status: 'ready',
              message: '10.0.0',
            },
          ],
          agents: [
            {
              name: 'Claude Code',
              command: 'npx -y @agentclientprotocol/claude-agent-acp@0.30.0',
              status: 'missing',
              install: 'npm install -g @agentclientprotocol/claude-agent-acp@latest',
            },
          ],
          acpPackages: [],
        },
        defaultAgentId: 'claude',
        agents: [{ id: 'claude', name: 'Claude Code' }],
        workspaceId: 'default',
        version: '0.1.0',
        lastHeartbeat: Date.UTC(2026, 3, 24, 4, 0, 0),
        registeredAt: Date.UTC(2026, 3, 24, 3, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 4, 0, 0),
        runningTaskIds: [],
      },
    ],
  })

  await page.goto('/c')

  await page.getByTitle('Add Workspace').click()
  const dialog = page.getByRole('dialog')

  await dialog.getByRole('button', { name: 'Remote Device' }).click()
  await expect(dialog.getByText('device-executor connect --server')).toBeVisible()
  await dialog.getByRole('button', { name: 'Next', exact: true }).click()

  await expect(dialog.getByText('Claude Code')).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Next', exact: true })).toBeDisabled()

  await dialog.getByRole('button', { name: 'Recheck' }).click()
  await expect.poll(() => state.deviceSetupCheckRequests).toEqual(['dev-1'])
  await expect(dialog.getByText('Device setup is ready.')).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Next', exact: true })).toBeEnabled()

  await dialog.getByRole('button', { name: 'Next', exact: true }).click()
  await dialog.getByLabel('Workspace name').fill('Website')
  await dialog.getByLabel('Remote path').fill('/Users/me/site')
  await dialog.getByRole('button', { name: 'Add', exact: true }).click()

  await expect(page.getByRole('button', { name: /Website/ })).toBeVisible()
  await expect(page.getByText('Office Mac · /Users/me/site')).toBeVisible()

  const composer = page.getByRole('textbox', { name: /Message/ })
  await composer.fill('Check remote workspace')
  await page.getByRole('button', { name: 'Send' }).click()

  await expect(page.getByText('Check remote workspace')).toBeVisible()
  await expect(page.getByText('Mock response')).toBeVisible()
  expect(state.workspaceCreates).toEqual([
    {
      name: 'Website',
      path: '/Users/me/site',
      kind: 'remote',
      deviceId: 'dev-1',
      deviceName: 'Office Mac',
      remotePath: '/Users/me/site',
    },
  ])
  expect(state.chatRequests[0]?.deviceId).toBe('dev-1')
})

test('shows categorized setup requirements and copies install hints in the remote wizard', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], {
    origin: 'http://127.0.0.1:4173',
  })

  const codexInstall = 'npm install -g @openai/codex'
  const acpInstall = 'npm install -g @agentclientprotocol/claude-agent-acp@latest'
  const nodeDownload = 'https://nodejs.org/en/download'

  await installMockBackend(page, {
    devices: [
      {
        id: 'dev-setup-required',
        name: 'Remote Mac',
        displayName: 'Remote Mac',
        status: 'setup_required',
        setupReady: false,
        setupStatus: {
          ready: false,
          environment: [
            {
              name: 'npm',
              command: 'npm -v',
              status: 'missing',
              message: 'npm command not found',
              install: nodeDownload,
            },
          ],
          agents: [
            {
              name: 'Codex CLI',
              command: 'codex --version',
              status: 'missing',
              message: 'codex command not found',
              install: codexInstall,
            },
          ],
          acpPackages: [
            {
              name: 'Claude ACP package',
              package: '@agentclientprotocol/claude-agent-acp',
              status: 'not_installed',
              message: 'Package is not cached on the remote device',
              install: acpInstall,
            },
          ],
        },
        defaultAgentId: 'codex',
        agents: [{ id: 'codex', name: 'Codex CLI' }],
        workspaceId: 'default',
        version: '0.1.0',
        lastHeartbeat: Date.UTC(2026, 3, 24, 4, 0, 0),
        registeredAt: Date.UTC(2026, 3, 24, 3, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 4, 0, 0),
        runningTaskIds: [],
      },
    ],
  })

  await page.goto('/c')

  await page.getByTitle('Add Workspace').click()
  const dialog = page.getByRole('dialog')

  await dialog.getByRole('button', { name: 'Remote Device' }).click()
  await expect(dialog.getByText('device-executor connect --server')).toBeVisible()
  await dialog.getByRole('button', { name: 'Next', exact: true }).click()

  await expect(dialog.getByText('Install or fix these items on the remote Mac, then click Recheck.')).toBeVisible()
  await expect(dialog.getByText('Environment')).toBeVisible()
  await expect(dialog.getByText('Agent CLI')).toBeVisible()
  await expect(dialog.getByText('ACP Packages')).toBeVisible()
  await expect(dialog.getByText('npm command not found')).toBeVisible()
  await expect(dialog.getByText('codex command not found')).toBeVisible()
  await expect(dialog.getByText('Package is not cached on the remote device')).toBeVisible()
  await expect(dialog.getByRole('link', { name: nodeDownload })).toHaveAttribute('href', nodeDownload)
  await expect(dialog.getByText(codexInstall)).toBeVisible()
  await expect(dialog.getByText(acpInstall)).toBeVisible()

  await dialog.getByRole('button', { name: 'Copy install hint for Codex CLI' }).click()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(codexInstall)
})

test('shows a waiting setup result state when the remote device has not reported setupStatus', async ({ page }) => {
  await installMockBackend(page, {
    devices: [
      {
        id: 'dev-waiting',
        name: 'Remote Mac',
        displayName: 'Remote Mac',
        status: 'setup_required',
        setupReady: false,
        setupStatus: null,
        defaultAgentId: 'claude',
        agents: [{ id: 'claude', name: 'Claude Code' }],
        workspaceId: 'default',
        version: '0.1.0',
        lastHeartbeat: Date.UTC(2026, 3, 24, 4, 0, 0),
        registeredAt: Date.UTC(2026, 3, 24, 3, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 4, 0, 0),
        runningTaskIds: [],
      },
    ],
  })

  await page.goto('/c')

  await page.getByTitle('Add Workspace').click()
  const dialog = page.getByRole('dialog')

  await dialog.getByRole('button', { name: 'Remote Device' }).click()
  await expect(dialog.getByText('device-executor connect --server')).toBeVisible()
  await dialog.getByRole('button', { name: 'Next', exact: true }).click()

  await expect(dialog.getByText('Waiting for setup check results from the remote device.')).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Refresh' })).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Recheck' })).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Next', exact: true })).toBeDisabled()
})

test('renders remote device SSE errors for unavailable workspaces', async ({ page }) => {
  const state = await installMockBackend(page, {
    workspaces: [
      {
        id: 'ws-remote',
        name: 'Remote Docs',
        path: '/Users/dev/docs',
        kind: 'remote',
        deviceId: 'dev-offline',
        deviceName: 'Office Mac',
        remotePath: '/Users/dev/docs',
        deviceStatus: 'offline',
        setupReady: false,
      },
    ],
    defaultWorkspace: 'ws-remote',
    devices: [
      {
        id: 'dev-offline',
        name: 'MacBook Pro',
        alias: 'Office Mac',
        displayName: 'Office Mac',
        status: 'offline',
        setupReady: false,
        setupStatus: {
          ready: false,
          environment: [],
          agents: [],
          acpPackages: [],
        },
        defaultAgentId: 'claude',
        agents: [{ id: 'claude', name: 'Claude Code' }],
        workspaceId: 'default',
        version: '0.1.0',
        lastHeartbeat: Date.UTC(2026, 3, 24, 4, 0, 0),
        registeredAt: Date.UTC(2026, 3, 24, 3, 0, 0),
        updatedAt: Date.UTC(2026, 3, 24, 4, 0, 0),
        runningTaskIds: [],
      },
    ],
  })

  await page.goto('/c')

  await expect(page.getByText('Office Mac · /Users/dev/docs')).toBeVisible()

  const composer = page.getByRole('textbox', { name: /Message/ })
  await composer.fill('Can you inspect the docs?')
  await page.getByRole('button', { name: 'Send' }).click()

  await expect(page.getByText('Device is offline')).toBeVisible()
  expect(state.chatRequests[0]?.deviceId).toBe('dev-offline')
})
