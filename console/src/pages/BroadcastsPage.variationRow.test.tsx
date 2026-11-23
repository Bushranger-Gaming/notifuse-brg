import '../__tests__/resizeObserverMock'
import type { ReactNode } from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { App } from 'antd'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BroadcastCard } from './BroadcastsPage'
import { broadcastApi } from '../services/api/broadcast'
import { transactionalNotificationsApi } from '../services/api/transactional_notifications'
import type { Broadcast } from '../services/api/broadcast'
import type { UserPermissions, Workspace } from '../services/api/types'

// What the card draws around its variation rows is not under test, and each of these runs queries
// of its own
vi.mock('../components/broadcasts/BroadcastStats', () => ({ BroadcastStats: () => null }))
vi.mock('../components/broadcasts/UpsertBroadcastDrawer', () => ({
  UpsertBroadcastDrawer: () => null
}))
vi.mock('../components/broadcasts/BroadcastAnalyticsLinks', () => ({
  BroadcastAnalyticsLinks: () => null
}))
vi.mock('../components/templates/TemplatePreviewDrawer', () => ({
  default: ({ children }: { children: ReactNode }) => <>{children}</>
}))
vi.mock('../services/api/task', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../services/api/task')>()
  return {
    ...actual,
    taskApi: { ...actual.taskApi, findByBroadcastId: vi.fn().mockResolvedValue(null) }
  }
})
// The broadcast's own send stays mocked to prove the test email no longer goes through it
vi.mock('../services/api/broadcast', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../services/api/broadcast')>()
  return {
    ...actual,
    broadcastApi: { ...actual.broadcastApi, sendToIndividual: vi.fn() }
  }
})
vi.mock('../services/api/transactional_notifications', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../services/api/transactional_notifications')>()
  return {
    ...actual,
    transactionalNotificationsApi: {
      ...actual.transactionalNotificationsApi,
      testTemplate: vi.fn()
    }
  }
})

const workspace = {
  id: 'ws-1',
  settings: { marketing_email_provider_id: 'int-marketing' },
  integrations: [
    {
      id: 'int-marketing',
      name: 'Marketing',
      type: 'email',
      email_provider: {
        kind: 'ses',
        senders: [
          {
            id: 'sender-template',
            name: 'Template',
            email: 'template@example.com',
            is_default: true
          }
        ]
      }
    }
  ]
} as unknown as Workspace

// A draft, so the card runs no query for stats or results
const broadcast = {
  id: 'bc-1',
  name: 'Newsletter',
  status: 'draft',
  audience: { exclude_unsubscribed: true },
  schedule: { is_scheduled: false },
  test_settings: {
    enabled: false,
    sample_percentage: 50,
    auto_send_winner: false,
    variations: [
      {
        variation_name: 'A',
        template_id: 'tmpl-1',
        template: {
          id: 'tmpl-1',
          name: 'Welcome',
          category: 'marketing',
          email: { subject: 'Hello', sender_id: 'sender-template' }
        }
      }
    ]
  }
} as unknown as Broadcast

const permissionsOf = (granted: Record<string, { read: boolean; write: boolean }>) =>
  granted as unknown as UserPermissions

const none = { read: false, write: false }

// Broadcast, template and contact access is full, so a disabled button can only be the
// transactional permission the template test path needs
const transactionalPermissions = (transactional: Partial<typeof none>) =>
  permissionsOf({
    templates: { read: true, write: true },
    contacts: { read: true, write: true },
    broadcasts: { read: true, write: true },
    transactional: { ...none, ...transactional }
  })

const renderCard = (permissions: UserPermissions | null) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <App>
        <BroadcastCard
          broadcast={broadcast}
          lists={[]}
          segments={[]}
          workspaceId="ws-1"
          onDelete={vi.fn()}
          onPause={vi.fn()}
          onRetryFailed={vi.fn()}
          onResume={vi.fn()}
          onCancel={vi.fn()}
          onSchedule={vi.fn()}
          onRefresh={vi.fn()}
          currentWorkspace={workspace}
          permissions={permissions}
          isFirst
          currentPage={1}
          pageSize={5}
        />
      </App>
    </QueryClientProvider>
  )

// The button of the variation row, found by its paper plane: it has no label of its own
const testEmailButton = (container: HTMLElement) => {
  const button = container.querySelector('[data-icon="paper-plane"]')?.closest('button')
  if (!button) throw new Error('the variation row has no Send Test Email button')
  return button
}

describe('variation row Send Test Email permissions', () => {
  beforeEach(() => {
    vi.mocked(broadcastApi.sendToIndividual).mockReset()
    vi.mocked(transactionalNotificationsApi.testTemplate).mockReset()
    vi.mocked(transactionalNotificationsApi.testTemplate).mockResolvedValue({ success: true })
  })

  it('is enabled for a person who writes transactional notifications', () => {
    const { container } = renderCard(transactionalPermissions({ write: true }))

    expect(testEmailButton(container)).toBeEnabled()
  })

  // The template test path asks for Transactional write, then reads the template, so the row asks
  // for Templates read too, and for nothing more
  it('is enabled for a person who writes transactional notifications and reads templates, and nothing else', () => {
    const { container } = renderCard(
      permissionsOf({
        transactional: { read: false, write: true },
        templates: { read: true, write: false }
      })
    )

    expect(testEmailButton(container)).toBeEnabled()
  })

  it.each<[string, UserPermissions | null]>([
    [
      'writes transactional notifications but cannot read templates',
      permissionsOf({
        templates: { read: false, write: true },
        contacts: { read: true, write: true },
        broadcasts: { read: true, write: true },
        transactional: { read: true, write: true }
      })
    ],
    [
      'writes transactional notifications and nothing else',
      permissionsOf({ transactional: { read: false, write: true } })
    ],
    [
      'reads templates but does not write transactional notifications',
      permissionsOf({ templates: { read: true, write: true }, transactional: { read: true, write: false } })
    ],
    ['reads transactional notifications but does not write them', transactionalPermissions({ read: true })],
    ['has no transactional permission', transactionalPermissions({})],
    [
      'writes broadcasts, templates and contacts, which the test email once needed, but not transactional notifications',
      permissionsOf({
        templates: { read: true, write: true },
        contacts: { read: true, write: true },
        broadcasts: { read: true, write: true }
      })
    ],
    ['has a permission set that lists nothing', permissionsOf({})],
    ['has no permission set yet', null]
  ])('is disabled for a person who %s', (_, permissions) => {
    const { container } = renderCard(permissions)

    expect(testEmailButton(container)).toBeDisabled()
  })

  it('tells which permission it needs when it is disabled', async () => {
    const { container } = renderCard(transactionalPermissions({ read: true }))

    await userEvent.hover(testEmailButton(container))

    expect((await screen.findByRole('tooltip')).textContent).toBe(
      "You don't have write permission for transactional notifications"
    )
  })

  it('tells that Templates read is missing when Transactional write is held', async () => {
    const { container } = renderCard(
      permissionsOf({
        templates: { read: false, write: true },
        transactional: { read: true, write: true }
      })
    )

    await userEvent.hover(testEmailButton(container))

    expect((await screen.findByRole('tooltip')).textContent).toBe(
      "You don't have read permission for templates"
    )
  })

  it('renders no test email dialog until the button is clicked', () => {
    renderCard(transactionalPermissions({ write: true }))

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('does not open a dialog from a button that is disabled', () => {
    const { container } = renderCard(transactionalPermissions({ read: true }))

    fireEvent.click(testEmailButton(container))

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('tests the template of its row from the sender its From line shows, through the marketing integration of the card', async () => {
    const { container } = renderCard(transactionalPermissions({ write: true }))

    fireEvent.click(testEmailButton(container))

    const dialog = await screen.findByRole('dialog')
    // The dialog shows the sender the test goes out from: the one of the template, on the marketing provider
    expect(within(dialog).getByText('Template <template@example.com>')).toBeInTheDocument()
    fireEvent.change(within(dialog).getByPlaceholderText('recipient@example.com'), {
      target: { value: 'to@example.com' }
    })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send Test Email' }))

    await waitFor(() => expect(transactionalNotificationsApi.testTemplate).toHaveBeenCalledTimes(1))
    expect(vi.mocked(transactionalNotificationsApi.testTemplate).mock.calls[0]).toEqual([
      'ws-1',
      'tmpl-1',
      'int-marketing',
      'sender-template',
      'to@example.com'
    ])
    // The broadcast's own send would count the test in the A/B results
    expect(broadcastApi.sendToIndividual).not.toHaveBeenCalled()
  })
})
