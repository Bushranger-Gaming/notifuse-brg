import '../../__tests__/resizeObserverMock'
import { StrictMode, useState } from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { App } from 'antd'
import { SendVariationTestModal } from './SendVariationTestModal'
import { transactionalNotificationsApi } from '../../services/api/transactional_notifications'
import type { BroadcastVariation } from '../../services/api/broadcast'
import type { Workspace } from '../../services/api/types'

vi.mock('../../services/api/transactional_notifications', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../services/api/transactional_notifications')>()
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

const defaultSender = {
  id: 'sender-default',
  name: 'Default',
  email: 'default@example.com',
  is_default: true
}
const supportSender = {
  id: 'sender-support',
  name: 'Support',
  email: 'support@example.com',
  is_default: false
}
const templateSender = {
  id: 'sender-template',
  name: 'Template',
  email: 'template@example.com',
  is_default: false
}

// The transactional provider holds a sender the marketing one lacks, and the marketing one is the
// provider every broadcast goes through
const workspaceWith = (
  marketingSenders: object[] = [defaultSender, supportSender, templateSender],
  patch: Record<string, unknown> = {}
) =>
  ({
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
        email_provider: { kind: 'ses', senders: marketingSenders }
      }
    ],
    ...patch
  }) as unknown as Workspace

const variationOf = ({
  senderId,
  templateSenderId
}: { senderId?: string; templateSenderId?: string } = {}) =>
  ({
    variation_name: 'B',
    template_id: 'tmpl-2',
    sender_id: senderId,
    template: {
      id: 'tmpl-2',
      name: 'Welcome',
      category: 'marketing',
      email: { subject: 'Hello', sender_id: templateSenderId }
    }
  }) as unknown as BroadcastVariation

// The dialog renders only while it is open, so it is open as soon as it is rendered. A null
// workspace is one the page has not loaded: a default parameter would swallow an undefined one
const renderModal = (
  variation: BroadcastVariation = variationOf({ senderId: supportSender.id }),
  workspace: Workspace | null = workspaceWith()
) => {
  const onClose = vi.fn()
  render(
    <App>
      <SendVariationTestModal
        workspaceId="ws-1"
        workspace={workspace ?? undefined}
        variation={variation}
        onClose={onClose}
      />
    </App>
  )
  return { onClose }
}

// A page that can take the dialog away while a send is in flight, as the Cancel button of a row does
function ModalHost({ onClose }: { onClose: () => void }) {
  const [open, setOpen] = useState(true)
  return (
    <>
      <button onClick={() => setOpen(false)}>Take the dialog away</button>
      {open && (
        <SendVariationTestModal
          workspaceId="ws-1"
          workspace={workspaceWith()}
          variation={variationOf({ senderId: supportSender.id })}
          onClose={onClose}
        />
      )}
    </>
  )
}

const typeRecipient = (dialog: HTMLElement, value: string) =>
  fireEvent.change(within(dialog).getByPlaceholderText('recipient@example.com'), {
    target: { value }
  })

// Taken before the send starts: while it loads, the button's name carries the loading icon
const sendButton = (dialog: HTMLElement) =>
  within(dialog).getByRole('button', { name: 'Send Test Email' })

const errorAlert = (dialog: HTMLElement) => dialog.querySelector<HTMLElement>('.ant-alert-error')

// What a send is called with: the workspace, the template of the variation, the marketing
// integration, the sender and the address
const calledWith = (senderId: string) => [
  ['ws-1', 'tmpl-2', 'int-marketing', senderId, 'to@example.com']
]

const defaultSenderNote =
  'This variation names no sender on the marketing email provider, so the test is sent from the default sender'

// Lets a send that should not start have the time to
const settle = () =>
  act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50))
  })

describe('SendVariationTestModal', () => {
  beforeEach(() => {
    testTemplate.mockReset()
    testTemplate.mockResolvedValue({ success: true })
  })

  it('titles the dialog Send Test Email and labels the email field Recipient Email', async () => {
    renderModal()

    const dialog = await screen.findByRole('dialog')

    expect(
      within(dialog).getByText('Send Test Email', { selector: '.ant-modal-title' })
    ).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Recipient Email')).toBe(
      within(dialog).getByPlaceholderText('recipient@example.com')
    )
  })

  describe('the sender of the test', () => {
    it.each([
      {
        description: 'the override of the variation',
        variation: variationOf({ senderId: 'sender-support', templateSenderId: 'sender-template' }),
        from: 'Support <support@example.com>',
        senderId: 'sender-support',
        defaultNote: false
      },
      {
        description: 'the sender of the template when the variation has no override',
        variation: variationOf({ templateSenderId: 'sender-template' }),
        from: 'Template <template@example.com>',
        senderId: 'sender-template',
        defaultNote: false
      },
      {
        description: 'the default sender of the marketing provider when neither names a sender',
        variation: variationOf(),
        from: 'Default <default@example.com>',
        senderId: 'sender-default',
        defaultNote: true
      },
      // The broadcast falls back to the default sender for a template sender the provider lacks
      {
        description: 'the default sender when the sender of the template is not on the marketing provider',
        variation: variationOf({ templateSenderId: 'sender-transactional' }),
        from: 'Default <default@example.com>',
        senderId: 'sender-default',
        defaultNote: true
      }
    ])('sends the test from $description, through the marketing provider, and shows that From', async ({
      variation,
      from,
      senderId,
      defaultNote
    }) => {
      const { onClose } = renderModal(variation)

      const dialog = await screen.findByRole('dialog')
      expect(within(dialog).getByText('From')).toBeInTheDocument()
      expect(within(dialog).getByText(from)).toBeInTheDocument()
      if (defaultNote) {
        expect(within(dialog).getByText(defaultSenderNote)).toBeInTheDocument()
      } else {
        expect(within(dialog).queryByText(defaultSenderNote)).not.toBeInTheDocument()
      }

      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))

      await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
      // The sender shown is the one sent, and the template path renders the subject itself: it is
      // handed the template, never a broadcast, so nothing is recorded in a broadcast's history
      expect(testTemplate.mock.calls).toEqual(calledWith(senderId))
    })

    // The backend falls back to the sender flagged as default only, and fails when none is flagged:
    // a test sent from the first sender would succeed where the broadcast would not
    it('refuses to send from the first sender when neither names one and the marketing provider flags none as default', async () => {
      const first = { id: 'sender-first', name: 'First', email: 'first@example.com', is_default: false }
      const second = { id: 'sender-second', name: 'Second', email: 'second@example.com', is_default: false }
      const { onClose } = renderModal(variationOf(), workspaceWith([first, second]))

      const dialog = await screen.findByRole('dialog')
      expect(
        within(dialog).getByText(
          'The marketing email provider has no default sender; choose a sender on the variation or its template'
        )
      ).toBeInTheDocument()
      expect(errorAlert(dialog)).not.toBeNull()
      expect(within(dialog).queryByText('From')).not.toBeInTheDocument()
      expect(within(dialog).queryByText('First <first@example.com>')).not.toBeInTheDocument()
      expect(sendButton(dialog)).toBeDisabled()

      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))
      // Enter in the address field submits the form without the button
      fireEvent.submit(dialog.querySelector('form') as HTMLFormElement)
      await settle()

      expect(testTemplate).not.toHaveBeenCalled()
      expect(onClose).not.toHaveBeenCalled()
    })

    // A sender that is named needs no default
    it('sends from the sender of the template when the marketing provider flags none as default', async () => {
      const first = { id: 'sender-first', name: 'First', email: 'first@example.com', is_default: false }
      const second = { id: 'sender-second', name: 'Second', email: 'second@example.com', is_default: false }
      const { onClose } = renderModal(
        variationOf({ templateSenderId: 'sender-second' }),
        workspaceWith([first, second])
      )

      const dialog = await screen.findByRole('dialog')
      expect(errorAlert(dialog)).toBeNull()
      expect(within(dialog).getByText('Second <second@example.com>')).toBeInTheDocument()
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))

      await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
      expect(testTemplate.mock.calls).toEqual(calledWith('sender-second'))
    })

    // The flag picks the default, not the position
    it('sends from the sender the marketing provider flags as default when it is not the first', async () => {
      const first = { id: 'sender-first', name: 'First', email: 'first@example.com', is_default: false }
      const flagged = { id: 'sender-flagged', name: 'Flagged', email: 'flagged@example.com', is_default: true }
      const { onClose } = renderModal(variationOf(), workspaceWith([first, flagged]))

      const dialog = await screen.findByRole('dialog')
      expect(within(dialog).getByText('Flagged <flagged@example.com>')).toBeInTheDocument()
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))

      await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
      expect(testTemplate.mock.calls).toEqual(calledWith('sender-flagged'))
    })

    // The test endpoint falls back to the default sender for an id it does not know, so the dialog
    // refuses before sending: the broadcast would pause on such a variation
    it.each([
      { description: 'is on no provider', senderId: 'sender-gone' },
      {
        description: 'is on the transactional provider only',
        senderId: 'sender-transactional'
      }
    ])('refuses an override that $description, and sends nothing', async ({ senderId }) => {
      const { onClose } = renderModal(variationOf({ senderId, templateSenderId: 'sender-template' }))

      const dialog = await screen.findByRole('dialog')
      expect(
        within(dialog).getByText(
          "This variation's sender is no longer on the marketing email provider; the broadcast would pause"
        )
      ).toBeInTheDocument()
      expect(errorAlert(dialog)).not.toBeNull()
      expect(within(dialog).queryByText('From')).not.toBeInTheDocument()
      expect(sendButton(dialog)).toBeDisabled()

      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))
      // Enter in the address field submits the form without the button
      fireEvent.submit(dialog.querySelector('form') as HTMLFormElement)
      await settle()

      expect(testTemplate).not.toHaveBeenCalled()
      expect(onClose).not.toHaveBeenCalled()
    })

    it.each([
      {
        description: 'sets no marketing provider',
        workspace: workspaceWith(undefined, {
          settings: { transactional_email_provider_id: 'int-transactional' }
        })
      },
      {
        description: 'sets a marketing provider it has no integration for',
        workspace: workspaceWith(undefined, {
          settings: { marketing_email_provider_id: 'int-gone' }
        })
      },
      {
        description: 'has no integrations',
        workspace: workspaceWith(undefined, { integrations: undefined })
      },
      { description: 'is not loaded', workspace: null }
    ])('shows there is no marketing provider when the workspace $description, and sends nothing', async ({
      workspace
    }) => {
      const { onClose } = renderModal(variationOf({ senderId: 'sender-support' }), workspace)

      const dialog = await screen.findByRole('dialog')
      expect(
        within(dialog).getByText(
          'The workspace has no marketing email provider, so there is nothing to send the test through'
        )
      ).toBeInTheDocument()
      expect(errorAlert(dialog)).not.toBeNull()
      expect(sendButton(dialog)).toBeDisabled()

      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))
      fireEvent.submit(dialog.querySelector('form') as HTMLFormElement)
      await settle()

      expect(testTemplate).not.toHaveBeenCalled()
      expect(onClose).not.toHaveBeenCalled()
    })

    it('shows there is no sender when the marketing provider has none, and sends nothing', async () => {
      renderModal(variationOf(), workspaceWith([]))

      const dialog = await screen.findByRole('dialog')
      expect(
        within(dialog).getByText('The marketing email provider has no sender to send the test from')
      ).toBeInTheDocument()
      expect(sendButton(dialog)).toBeDisabled()

      typeRecipient(dialog, 'to@example.com')
      fireEvent.submit(dialog.querySelector('form') as HTMLFormElement)
      await settle()

      expect(testTemplate).not.toHaveBeenCalled()
    })

    it('shows no error and enables Send when the sender is found', async () => {
      renderModal()

      const dialog = await screen.findByRole('dialog')

      expect(errorAlert(dialog)).toBeNull()
      expect(sendButton(dialog)).toBeEnabled()
    })
  })

  describe('while a send is in flight', () => {
    it('shows the loading state on the Send button, and a second click does not send again', async () => {
      const pending = pendingSend()
      testTemplate.mockReturnValue(pending.promise)
      const { onClose } = renderModal()

      const dialog = await screen.findByRole('dialog')
      const send = sendButton(dialog)
      expect(send).not.toHaveClass('ant-btn-loading')

      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(send)

      await waitFor(() => expect(send).toHaveClass('ant-btn-loading'))
      expect(testTemplate).toHaveBeenCalledTimes(1)

      fireEvent.click(send)
      // A second send would start once the form has validated again, so the real time it takes is let pass
      await settle()
      expect(testTemplate).toHaveBeenCalledTimes(1)
      expect(onClose).not.toHaveBeenCalled()

      pending.resolve({ success: true })

      await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
    })

    it('sends once when Enter is pressed twice, and keeps the address from being edited meanwhile', async () => {
      const pending = pendingSend()
      testTemplate.mockReturnValue(pending.promise)
      const { onClose } = renderModal()

      const dialog = await screen.findByRole('dialog')
      const input = within(dialog).getByPlaceholderText('recipient@example.com')

      await userEvent.type(input, 'to@example.com{Enter}{Enter}')

      await waitFor(() => expect(testTemplate).toHaveBeenCalledTimes(1))
      await waitFor(() => expect(input).toBeDisabled())
      await settle()
      expect(testTemplate).toHaveBeenCalledTimes(1)
      expect(onClose).not.toHaveBeenCalled()

      pending.resolve({ success: true })

      await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
      expect(testTemplate).toHaveBeenCalledTimes(1)
    })

    // The form drops a submit when a newer one starts before it has validated, so only a submit that
    // comes after the first send began reaches the handler a second time. The form fires submit
    // whatever the state of its fields, and the address is disabled by then: what stops this send
    // is the guard of the handler, and the gap between the first send starting and the dialog
    // rendering it is where a second Enter lands
    it('sends once when the form is submitted again while the first send is pending', async () => {
      const pending = pendingSend()
      testTemplate.mockReturnValue(pending.promise)
      const { onClose } = renderModal()

      const dialog = await screen.findByRole('dialog')
      const input = within(dialog).getByPlaceholderText('recipient@example.com')
      const form = dialog.querySelector('form') as HTMLFormElement
      typeRecipient(dialog, 'to@example.com')

      fireEvent.submit(form)
      await waitFor(() => expect(testTemplate).toHaveBeenCalledTimes(1))
      await waitFor(() => expect(input).toBeDisabled())

      fireEvent.submit(form)
      fireEvent.submit(form)
      // A second send would start once the form has validated again, so the real time it takes is let pass
      await settle()
      expect(testTemplate).toHaveBeenCalledTimes(1)
      expect(onClose).not.toHaveBeenCalled()

      pending.resolve({ success: true })

      await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
      expect(testTemplate).toHaveBeenCalledTimes(1)
    })

    // StrictMode, and a hot reload in development, run the effect of a mounted dialog again after
    // its cleanup: it must count as mounted again, or it never closes itself
    it('still closes itself when its effect has run again after a cleanup, as in StrictMode', async () => {
      const onClose = vi.fn()
      render(
        <StrictMode>
          <App>
            <SendVariationTestModal
              workspaceId="ws-1"
              workspace={workspaceWith()}
              variation={variationOf({ senderId: supportSender.id })}
              onClose={onClose}
            />
          </App>
        </StrictMode>
      )

      const dialog = await screen.findByRole('dialog')
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))

      await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
      expect(testTemplate).toHaveBeenCalledTimes(1)
    })

    it('shows the error of a failed send in the dialog when its effect has run again after a cleanup', async () => {
      testTemplate.mockRejectedValue(new Error('boom'))
      render(
        <StrictMode>
          <App>
            <SendVariationTestModal
              workspaceId="ws-1"
              workspace={workspaceWith()}
              variation={variationOf({ senderId: supportSender.id })}
              onClose={vi.fn()}
            />
          </App>
        </StrictMode>
      )

      const dialog = await screen.findByRole('dialog')
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))

      expect(await within(dialog).findByText('boom')).toBeInTheDocument()
      expect(errorAlert(dialog)).toHaveTextContent('boom')
    })

    it('sends again once a failed send is over, with the address editable again', async () => {
      testTemplate.mockRejectedValueOnce(new Error('boom'))
      renderModal()

      const dialog = await screen.findByRole('dialog')
      const input = within(dialog).getByPlaceholderText('recipient@example.com')
      const send = sendButton(dialog)
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(send)

      expect(await within(dialog).findByText('boom')).toBeInTheDocument()
      await waitFor(() => expect(input).toBeEnabled())
      await waitFor(() => expect(send).not.toHaveClass('ant-btn-loading'))

      fireEvent.click(send)

      await waitFor(() => expect(testTemplate).toHaveBeenCalledTimes(2))
    })

    // The dialog is unmounted when it is closed, and a row opens a new one the next time: what
    // onClose closes then is that new dialog
    it('does not call onClose when the send ends after the dialog is gone, and still says it was sent', async () => {
      const pending = pendingSend()
      testTemplate.mockReturnValue(pending.promise)
      const onClose = vi.fn()
      render(
        <App>
          <ModalHost onClose={onClose} />
        </App>
      )

      const dialog = await screen.findByRole('dialog')
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))
      await waitFor(() => expect(testTemplate).toHaveBeenCalledTimes(1))

      fireEvent.click(screen.getByText('Take the dialog away'))
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
      await act(async () => {
        pending.resolve({ success: true })
      })

      expect(await screen.findByText('Test email sent successfully')).toBeInTheDocument()
      expect(onClose).not.toHaveBeenCalled()
    })

    it('shows the message of a send that fails after the dialog is gone, as a message of the page', async () => {
      const pending = pendingSend()
      testTemplate.mockReturnValue(pending.promise)
      const onClose = vi.fn()
      render(
        <App>
          <ModalHost onClose={onClose} />
        </App>
      )

      const dialog = await screen.findByRole('dialog')
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))
      await waitFor(() => expect(testTemplate).toHaveBeenCalledTimes(1))

      fireEvent.click(screen.getByText('Take the dialog away'))
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
      await act(async () => {
        pending.resolve({ success: false, error: 'the provider refused the message' })
      })

      expect(await screen.findByText('the provider refused the message')).toBeInTheDocument()
      expect(onClose).not.toHaveBeenCalled()
    })

    // React 18 no longer warns about a state update on a component that is gone, so the end of the
    // send has nothing to guard there: the state is set whether the dialog is still there or not
    it('logs no unmounted component warning when the send ends after the dialog is gone', async () => {
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
      try {
        const pending = pendingSend()
        testTemplate.mockReturnValue(pending.promise)
        const onClose = vi.fn()
        render(
          <App>
            <ModalHost onClose={onClose} />
          </App>
        )

        const dialog = await screen.findByRole('dialog')
        typeRecipient(dialog, 'to@example.com')
        fireEvent.click(sendButton(dialog))
        await waitFor(() => expect(testTemplate).toHaveBeenCalledTimes(1))

        fireEvent.click(screen.getByText('Take the dialog away'))
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
        await act(async () => {
          pending.resolve({ success: true })
        })
        expect(await screen.findByText('Test email sent successfully')).toBeInTheDocument()

        expect(onClose).not.toHaveBeenCalled()
        const warned = consoleError.mock.calls.filter(([message]) =>
          /unmounted component/i.test(String(message))
        )
        expect(warned).toEqual([])
      } finally {
        consoleError.mockRestore()
      }
    })
  })

  describe('the outcome of a send', () => {
    it('shows that the test email was sent and closes the dialog', async () => {
      const { onClose } = renderModal()

      const dialog = await screen.findByRole('dialog')
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))

      expect(await screen.findByText('Test email sent successfully')).toBeInTheDocument()
      await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
    })

    it.each([
      { description: 'a string', reason: 'boom' },
      { description: 'an object without a message', reason: { code: 500 } },
      {
        description: 'an object with a message that is not an Error',
        reason: { message: 'not an Error' }
      },
      { description: 'an Error without a message', reason: new Error('') }
    ])('shows Something went wrong when the send is rejected with $description', async ({ reason }) => {
      testTemplate.mockRejectedValue(reason)
      const { onClose } = renderModal()

      const dialog = await screen.findByRole('dialog')
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))

      expect(await within(dialog).findByText('Something went wrong')).toBeInTheDocument()
      expect(errorAlert(dialog)).toHaveTextContent('Something went wrong')
      expect(within(dialog).queryByText('not an Error')).not.toBeInTheDocument()
      expect(onClose).not.toHaveBeenCalled()
    })

    it('shows the message of an Error instead of Something went wrong', async () => {
      testTemplate.mockRejectedValue(new Error('template does not contain email content'))
      renderModal()

      const dialog = await screen.findByRole('dialog')
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))

      expect(
        await within(dialog).findByText('template does not contain email content')
      ).toBeInTheDocument()
      expect(errorAlert(dialog)).toHaveTextContent('template does not contain email content')
      expect(within(dialog).queryByText('Something went wrong')).not.toBeInTheDocument()
    })

    it('shows the error of a response that says the send failed, and keeps the dialog open', async () => {
      testTemplate.mockResolvedValue({ success: false, error: 'the provider refused the message' })
      const { onClose } = renderModal()

      const dialog = await screen.findByRole('dialog')
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))

      expect(await within(dialog).findByText('the provider refused the message')).toBeInTheDocument()
      expect(screen.queryByText('Test email sent successfully')).not.toBeInTheDocument()
      expect(onClose).not.toHaveBeenCalled()
    })

    it('shows Something went wrong for a response that says the send failed without saying why', async () => {
      testTemplate.mockResolvedValue({ success: false })
      const { onClose } = renderModal()

      const dialog = await screen.findByRole('dialog')
      typeRecipient(dialog, 'to@example.com')
      fireEvent.click(sendButton(dialog))

      expect(await within(dialog).findByText('Something went wrong')).toBeInTheDocument()
      expect(onClose).not.toHaveBeenCalled()
    })

    it('shows no error alert before a failed send, shows it after one, and clears it when the send is tried again', async () => {
      const retry = pendingSend()
      testTemplate
        .mockRejectedValueOnce(new Error('template does not contain email content'))
        .mockReturnValueOnce(retry.promise)
      const { onClose } = renderModal()

      const dialog = await screen.findByRole('dialog')
      const send = sendButton(dialog)
      typeRecipient(dialog, 'to@example.com')
      expect(errorAlert(dialog)).toBeNull()

      fireEvent.click(send)

      expect(
        await within(dialog).findByText('template does not contain email content')
      ).toBeInTheDocument()
      expect(errorAlert(dialog)).not.toBeNull()
      // The send can be tried again once what is wrong is fixed
      await waitFor(() => expect(send).not.toHaveClass('ant-btn-loading'))
      expect(onClose).not.toHaveBeenCalled()

      fireEvent.click(send)

      await waitFor(() => expect(send).toHaveClass('ant-btn-loading'))
      expect(testTemplate).toHaveBeenCalledTimes(2)
      expect(errorAlert(dialog)).toBeNull()

      retry.resolve({ success: true })

      await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
      expect(errorAlert(dialog)).toBeNull()
    })
  })
})
