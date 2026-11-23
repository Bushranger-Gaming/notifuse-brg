import '../__tests__/resizeObserverMock'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { App } from 'antd'
import { VariationTestEmail } from './BroadcastsPage'
import { transactionalNotificationsApi } from '../services/api/transactional_notifications'
import { ApiError } from '../services/api/errors'
import type { BroadcastVariation } from '../services/api/broadcast'
import type { UserPermissions, Workspace } from '../services/api/types'

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

const testTemplate = vi.mocked(transactionalNotificationsApi.testTemplate)

type SendResult = Awaited<ReturnType<typeof transactionalNotificationsApi.testTemplate>>

// A send that stays pending until the test settles it
const pendingSend = () => {
  let resolve!: (result: SendResult) => void
  const promise = new Promise<SendResult>((res) => {
    resolve = res
  })
  return { promise, resolve }
}

// The marketing provider is the one every broadcast goes through; the transactional one holds a
// sender of its own that no variation of these tests may be sent from
const workspace = {
  id: 'ws-1',
  settings: {
    marketing_email_provider_id: 'int-marketing',
    transactional_email_provider_id: 'int-transactional'
  },
  integrations: [
    {
      id: 'int-transactional',
      name: 'Transactional',
      type: 'email',
      email_provider: {
        kind: 'ses',
        senders: [
          {
            id: 'sender-transactional',
            name: 'Transactional',
            email: 'transactional@example.com',
            is_default: true
          }
        ]
      }
    },
    {
      id: 'int-marketing',
      name: 'Marketing',
      type: 'email',
      email_provider: {
        kind: 'ses',
        senders: [
          { id: 'sender-default', name: 'Default', email: 'default@example.com', is_default: true },
          { id: 'sender-support', name: 'Support', email: 'support@example.com', is_default: false },
          {
            id: 'sender-template',
            name: 'Template',
            email: 'template@example.com',
            is_default: false
          }
        ]
      }
    }
  ]
} as unknown as Workspace

const variation = (senderId?: string): BroadcastVariation =>
  ({
    variation_name: 'B',
    template_id: 'tmpl-2',
    sender_id: senderId,
    template: {
      id: 'tmpl-2',
      name: 'Welcome',
      category: 'marketing',
      email: { subject: 'Hello', sender_id: 'sender-template' }
    }
  }) as unknown as BroadcastVariation

const permissionsOf = (transactionalWrite: boolean, templatesRead: boolean) =>
  ({
    transactional: { read: true, write: transactionalWrite },
    templates: { read: templatesRead, write: true }
  }) as unknown as UserPermissions

// What the template test path asks for: Transactional write, then Templates read to load the template
const permitted = permissionsOf(true, true)

// A null workspace is one the page has not loaded
const renderRow = (
  v: BroadcastVariation,
  permissions: UserPermissions | null = permitted,
  row: Workspace | null = workspace
) =>
  render(
    <App>
      <VariationTestEmail
        workspaceId="ws-1"
        workspace={row ?? undefined}
        variation={v}
        permissions={permissions}
      />
    </App>
  )

// Opens the dialog from the row button, taken before the dialog adds its own buttons to the page
const openDialog = async () => {
  fireEvent.click(screen.getByRole('button'))
  return screen.findByRole('dialog')
}

const typeRecipient = (dialog: HTMLElement, value: string) =>
  fireEvent.change(within(dialog).getByPlaceholderText('recipient@example.com'), {
    target: { value }
  })

const clickSend = (dialog: HTMLElement) =>
  fireEvent.click(within(dialog).getByRole('button', { name: 'Send Test Email' }))

describe('variation row Send Test Email', () => {
  beforeEach(() => {
    testTemplate.mockReset()
    testTemplate.mockResolvedValue({ success: true })
  })

  it('sends the template of the variation through the template test path, from the sender of the variation on the marketing provider', async () => {
    renderRow(variation('sender-support'))

    const dialog = await openDialog()
    expect(within(dialog).getByText('Support <support@example.com>')).toBeInTheDocument()
    typeRecipient(dialog, 'to@example.com')
    clickSend(dialog)

    await waitFor(() => expect(testTemplate).toHaveBeenCalledTimes(1))
    // The workspace, the template, the marketing integration, the override sender and the address:
    // a template is tested, so the send is neither a broadcast's nor part of its message history
    expect(testTemplate.mock.calls[0]).toEqual([
      'ws-1',
      'tmpl-2',
      'int-marketing',
      'sender-support',
      'to@example.com'
    ])
  })

  it('sends from the sender of the template when the variation has no override', async () => {
    renderRow(variation())

    const dialog = await openDialog()
    expect(within(dialog).getByText('Template <template@example.com>')).toBeInTheDocument()
    typeRecipient(dialog, 'to@example.com')
    clickSend(dialog)

    await waitFor(() => expect(testTemplate).toHaveBeenCalledTimes(1))
    expect(testTemplate.mock.calls[0]).toEqual([
      'ws-1',
      'tmpl-2',
      'int-marketing',
      'sender-template',
      'to@example.com'
    ])
  })

  it('sends from the default sender of the marketing provider when the variation and its template name none', async () => {
    renderRow({ variation_name: 'B', template_id: 'tmpl-2' } as unknown as BroadcastVariation)

    const dialog = await openDialog()
    expect(within(dialog).getByText('Default <default@example.com>')).toBeInTheDocument()
    typeRecipient(dialog, 'to@example.com')
    clickSend(dialog)

    await waitFor(() => expect(testTemplate).toHaveBeenCalledTimes(1))
    expect(testTemplate.mock.calls[0]).toEqual([
      'ws-1',
      'tmpl-2',
      'int-marketing',
      'sender-default',
      'to@example.com'
    ])
  })

  it('shows that the test email was sent and closes the dialog', async () => {
    renderRow(variation())

    const dialog = await openDialog()
    typeRecipient(dialog, 'to@example.com')
    clickSend(dialog)

    expect(await screen.findByText('Test email sent successfully')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('shows the message of the backend and keeps the dialog open when the send is refused', async () => {
    testTemplate.mockRejectedValue(new ApiError('template does not contain email content', 400))
    renderRow(variation('sender-support'))

    const dialog = await openDialog()
    typeRecipient(dialog, 'to@example.com')
    clickSend(dialog)

    expect(
      await within(dialog).findByText('template does not contain email content')
    ).toBeInTheDocument()
    expect(screen.getByRole('dialog')).toBe(dialog)
    expect(dialog.style.display).not.toBe('none')
    expect(screen.queryByText('Test email sent successfully')).not.toBeInTheDocument()
    // The send can be tried again once what is wrong is fixed
    await waitFor(() =>
      expect(within(dialog).getByRole('button', { name: 'Send Test Email' })).toBeEnabled()
    )
  })

  it('refuses a variation whose sender is no longer on the marketing provider, and sends nothing', async () => {
    renderRow(variation('sender-gone'))

    const dialog = await openDialog()

    expect(
      within(dialog).getByText(
        "This variation's sender is no longer on the marketing email provider; the broadcast would pause"
      )
    ).toBeInTheDocument()
    typeRecipient(dialog, 'to@example.com')
    expect(within(dialog).getByRole('button', { name: 'Send Test Email' })).toBeDisabled()
    clickSend(dialog)
    fireEvent.submit(dialog.querySelector('form') as HTMLFormElement)
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50))
    })

    expect(testTemplate).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBe(dialog)
  })

  it('shows there is no marketing provider when the workspace is not loaded, and sends nothing', async () => {
    renderRow(variation('sender-support'), permitted, null)

    const dialog = await openDialog()

    expect(
      within(dialog).getByText(
        'The workspace has no marketing email provider, so there is nothing to send the test through'
      )
    ).toBeInTheDocument()
    typeRecipient(dialog, 'to@example.com')
    expect(within(dialog).getByRole('button', { name: 'Send Test Email' })).toBeDisabled()
    clickSend(dialog)

    expect(testTemplate).not.toHaveBeenCalled()
  })

  it('sends nothing for an address that is not an email', async () => {
    renderRow(variation())

    const dialog = await openDialog()
    typeRecipient(dialog, 'not-an-email')
    clickSend(dialog)

    expect(await within(dialog).findByText('Please enter a valid email')).toBeInTheDocument()
    expect(testTemplate).not.toHaveBeenCalled()
  })

  it('sends nothing without an address', async () => {
    renderRow(variation())

    const dialog = await openDialog()
    clickSend(dialog)

    expect(await within(dialog).findByText('Email is required')).toBeInTheDocument()
    expect(testTemplate).not.toHaveBeenCalled()
  })

  it('sends once when Enter is pressed twice in the address field', async () => {
    const pending = pendingSend()
    testTemplate.mockReturnValue(pending.promise)
    renderRow(variation('sender-support'))

    const dialog = await openDialog()
    await userEvent.type(
      within(dialog).getByPlaceholderText('recipient@example.com'),
      'to@example.com{Enter}{Enter}'
    )

    await waitFor(() => expect(testTemplate).toHaveBeenCalledTimes(1))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50))
    })
    expect(testTemplate).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('dialog')).toBe(dialog)

    pending.resolve({ success: true })

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(testTemplate).toHaveBeenCalledTimes(1)
  })

  it('renders no dialog while it is closed, and removes it when it is cancelled', async () => {
    renderRow(variation())

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    const dialog = await openDialog()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(testTemplate).not.toHaveBeenCalled()
  })

  // The send of a dialog that was cancelled ends later, when the row has opened another one: the
  // end of the first must not close the second
  it('does not close the dialog opened since when a send of the dialog that was cancelled ends', async () => {
    const first = pendingSend()
    testTemplate.mockReturnValueOnce(first.promise)
    renderRow(variation('sender-support'))

    const trigger = screen.getByRole('button')
    fireEvent.click(trigger)
    const firstDialog = await screen.findByRole('dialog')
    typeRecipient(firstDialog, 'to@example.com')
    clickSend(firstDialog)
    await waitFor(() => expect(testTemplate).toHaveBeenCalledTimes(1))

    fireEvent.click(within(firstDialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    fireEvent.click(trigger)
    const secondDialog = await screen.findByRole('dialog')
    expect(secondDialog).not.toBe(firstDialog)

    await act(async () => {
      first.resolve({ success: true })
    })

    // The email did go, so the person is told, and the dialog they opened since stays open
    expect(await screen.findByText('Test email sent successfully')).toBeInTheDocument()
    expect(screen.getByRole('dialog')).toBe(secondDialog)
    expect(secondDialog.style.display).not.toBe('none')
    expect(testTemplate).toHaveBeenCalledTimes(1)
  })

  it.each([
    { description: 'Transactional write is missing', permissions: permissionsOf(false, true) },
    { description: 'Templates read is missing', permissions: permissionsOf(true, false) },
    { description: 'both are missing', permissions: permissionsOf(false, false) },
    { description: 'the permissions are not loaded', permissions: null }
  ])('does not open the dialog when $description', ({ permissions }) => {
    renderRow(variation('sender-support'), permissions)

    const button = screen.getByRole('button')
    expect(button).toBeDisabled()
    fireEvent.click(button)

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('is enabled, and tells what it does, when the person may send test emails', async () => {
    renderRow(variation('sender-support'))

    const button = screen.getByRole('button')
    expect(button).toBeEnabled()
    await userEvent.hover(button)

    // The tooltip shows after its open delay, so it is waited for
    expect((await screen.findByRole('tooltip')).textContent).toBe('Send Test Email')
  })

  it('is disabled, and names Transactional write, when only that is missing', async () => {
    renderRow(variation('sender-support'), permissionsOf(false, true))

    const button = screen.getByRole('button')
    expect(button).toBeDisabled()
    await userEvent.hover(button)

    expect((await screen.findByRole('tooltip')).textContent).toBe(
      "You don't have write permission for transactional notifications"
    )
  })

  it('is disabled, and names Templates read, when only that is missing', async () => {
    renderRow(variation('sender-support'), permissionsOf(true, false))

    const button = screen.getByRole('button')
    expect(button).toBeDisabled()
    await userEvent.hover(button)

    expect((await screen.findByRole('tooltip')).textContent).toBe(
      "You don't have read permission for templates"
    )
  })

  it('is disabled, and names both permissions, when both are missing', async () => {
    renderRow(variation('sender-support'), permissionsOf(false, false))

    const button = screen.getByRole('button')
    expect(button).toBeDisabled()
    await userEvent.hover(button)

    const tooltip = await screen.findByRole('tooltip')
    expect(tooltip.textContent).toContain(
      "You don't have write permission for transactional notifications"
    )
    expect(tooltip.textContent).toContain("You don't have read permission for templates")
  })

  it('is shown for a variation whose template is not joined to it, since the backend loads it', () => {
    renderRow({ variation_name: 'B', template_id: 'tmpl-2' } as unknown as BroadcastVariation)

    expect(screen.getByRole('button')).toBeEnabled()
  })

  it.each([
    {
      description: 'a template_id that is missing, whatever template is joined to it',
      variation: {
        variation_name: 'B',
        template: { id: 'tmpl-2', name: 'Welcome', category: 'marketing' }
      }
    },
    { description: 'an empty template_id', variation: { variation_name: 'B', template_id: '' } }
  ])('is not shown for a variation with $description', ({ variation: noTemplateId }) => {
    renderRow(noTemplateId as unknown as BroadcastVariation)

    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })
})
