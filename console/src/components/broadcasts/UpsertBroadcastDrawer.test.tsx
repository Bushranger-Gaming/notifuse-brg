import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { App as AntApp } from 'antd'
import type { ReactElement } from 'react'
import { UpsertBroadcastDrawer } from './UpsertBroadcastDrawer'
import { broadcastApi, Broadcast } from '../../services/api/broadcast'
import type { Workspace } from '../../services/api/types'
import type { List } from '../../services/api/list'

// The template selector pulls in template list queries; replace it with a bare
// input so the required template_id field can be filled without network calls.
vi.mock('../templates/TemplateSelectorInput', () => ({
  default: ({
    value,
    onChange,
    placeholder
  }: {
    value?: string
    onChange?: (value: string) => void
    placeholder?: string
  }) => (
    <input
      data-testid="template-selector"
      placeholder={placeholder}
      value={value ?? ''}
      onChange={(e) => onChange?.(e.target.value)}
    />
  )
}))

vi.mock('../../services/api/broadcast', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../services/api/broadcast')>()
  return {
    ...actual,
    broadcastApi: {
      list: vi.fn(),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      schedule: vi.fn(),
      refreshGlobalFeed: vi.fn(),
      testRecipientFeed: vi.fn()
    }
  }
})

const workspace = {
  id: 'ws1',
  settings: {
    website_url: 'https://example.com',
    email_tracking_enabled: true
  }
} as unknown as Workspace

const lists = [{ id: 'list1', name: 'Newsletter' }] as unknown as List[]

const makeBroadcast = (overrides: Partial<Broadcast> = {}): Broadcast =>
  ({
    id: 'bc1',
    workspace_id: 'ws1',
    name: 'Weekly Newsletter',
    channel_type: 'email',
    status: 'draft',
    audience: { list: 'list1', segments: [], exclude_unsubscribed: true },
    schedule: { is_scheduled: false, use_recipient_timezone: false },
    test_settings: {
      enabled: false,
      sample_percentage: 50,
      auto_send_winner: false,
      variations: [{ variation_name: 'default', template_id: 'tmpl1' }]
    },
    data_feed: {
      global_feed: { enabled: true, url: 'https://api.example.com/global', headers: [] },
      recipient_feed: { enabled: true, url: 'https://api.example.com/recipient', headers: [] }
    },
    test_phase_recipient_count: 0,
    winner_phase_recipient_count: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides
  }) as unknown as Broadcast

function renderDrawer(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <AntApp>{ui}</AntApp>
    </QueryClientProvider>
  )
}

const openEditDrawer = async (broadcast: Broadcast, drawerWorkspace: Workspace = workspace) => {
  renderDrawer(
    <UpsertBroadcastDrawer workspace={drawerWorkspace} broadcast={broadcast} lists={lists} />
  )
  await userEvent.click(screen.getByRole('button', { name: /Edit Broadcast/i }))
}

// The two feed switches carry no accessible name of their own; each sits in the
// heading row next to its title.
const feedSwitch = (title: string) =>
  within(screen.getByText(title).closest('div') as HTMLElement).getByRole('switch')

// Switching forward validates the tab being left first (handleTabChange awaits
// form.validateFields), so the new tab and the footer that goes with it land after the click
// has resolved, not with it. The lookups wait for them.
//
// Ant Design adds screen-reader text ("Tab 4 of 4") in front of the label on the selected,
// focused tab, so its accessible name is no longer exactly the label: match on the end of
// the name. A tab that is already selected is left alone.
const escapeRegExp = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

const goToTab = async (label: string) => {
  const tab = await screen.findByRole('tab', { name: new RegExp(`${escapeRegExp(label)}$`) })
  if (tab.getAttribute('aria-selected') === 'true') return
  await userEvent.click(tab)
}

const save = async () => {
  await goToTab('4. Content')
  await userEvent.click(await screen.findByRole('button', { name: 'Save' }))
}

const updatePayload = () =>
  (broadcastApi.update as Mock).mock.calls[0][0]

describe('UpsertBroadcastDrawer data feeds', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(broadcastApi.update as Mock).mockResolvedValue({ broadcast: {} })
    ;(broadcastApi.create as Mock).mockResolvedValue({ broadcast: {} })
  })

  it('turns a stored feed off when the user unchecks both toggles', async () => {
    // An absent data_feed - and an absent sub-feed inside it - means "keep what is
    // stored" on the server, so the payload has to spell out that both feeds are off
    // or the broadcast keeps calling the feed URLs on every send.
    await openEditDrawer(makeBroadcast())

    await goToTab('3. Data Feeds')
    await userEvent.click(feedSwitch('Global Data Feed'))
    await userEvent.click(feedSwitch('Per-Recipient Data Feed'))

    await save()

    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    const payload = updatePayload()
    expect(payload.data_feed).toBeDefined()
    expect(payload.data_feed.global_feed).toBeDefined()
    expect(payload.data_feed.global_feed.enabled).toBe(false)
    expect(payload.data_feed.recipient_feed).toBeDefined()
    expect(payload.data_feed.recipient_feed.enabled).toBe(false)
  })

  it('records both feeds as off on a broadcast created without any feed', async () => {
    renderDrawer(<UpsertBroadcastDrawer workspace={workspace} lists={lists} />)
    await userEvent.click(screen.getByRole('button', { name: /Create Broadcast/i }))

    await userEvent.type(
      screen.getByPlaceholderText('E.g. Weekly Newsletter - May 2023'),
      'Weekly Newsletter'
    )
    await userEvent.click(screen.getAllByRole('combobox')[0])
    await userEvent.click(await screen.findByText('Newsletter'))

    await goToTab('4. Content')
    await userEvent.type(screen.getByTestId('template-selector'), 'tmpl1')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(broadcastApi.create).toHaveBeenCalled())
    const payload = (broadcastApi.create as Mock).mock.calls[0][0]
    expect(payload.data_feed.global_feed.enabled).toBe(false)
    expect(payload.data_feed.recipient_feed.enabled).toBe(false)
  })

  it('keeps the feed the user left on while turning the other one off', async () => {
    await openEditDrawer(makeBroadcast())

    await goToTab('3. Data Feeds')
    await userEvent.click(feedSwitch('Per-Recipient Data Feed'))

    await save()

    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    const payload = updatePayload()
    expect(payload.data_feed.global_feed).toEqual({
      enabled: true,
      url: 'https://api.example.com/global',
      headers: []
    })
    expect(payload.data_feed.recipient_feed.enabled).toBe(false)
  })
})

// The drawer edits the audience, the templates and the UTM tags; the send date is owned by
// the send-or-schedule modal and broadcasts.schedule. The pencil is offered on a scheduled
// broadcast too, so a typo fix must not be able to move the date it is already booked for.
describe('UpsertBroadcastDrawer schedule', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(broadcastApi.update as Mock).mockResolvedValue({ broadcast: {} })
    ;(broadcastApi.create as Mock).mockResolvedValue({ broadcast: {} })
  })

  it('sends no schedule when a scheduled broadcast is edited', async () => {
    await openEditDrawer(
      makeBroadcast({
        status: 'scheduled',
        schedule: {
          is_scheduled: true,
          scheduled_date: '2026-09-01',
          scheduled_time: '09:00',
          timezone: 'Europe/Paris',
          use_recipient_timezone: false
        }
      })
    )

    await save()

    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    // An absent schedule is the server's "leave the stored one alone". Sending the drawer's
    // empty default instead clears is_scheduled, and a later resume reads that as "send now".
    expect('schedule' in updatePayload()).toBe(false)
  })

  it('sends no schedule when a draft broadcast is edited', async () => {
    await openEditDrawer(makeBroadcast())

    await save()

    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    expect('schedule' in updatePayload()).toBe(false)
  })

  it('creates a new broadcast unscheduled', async () => {
    renderDrawer(<UpsertBroadcastDrawer workspace={workspace} lists={lists} />)
    await userEvent.click(screen.getByRole('button', { name: /Create Broadcast/i }))

    await userEvent.type(
      screen.getByPlaceholderText('E.g. Weekly Newsletter - May 2023'),
      'Weekly Newsletter'
    )
    await userEvent.click(screen.getAllByRole('combobox')[0])
    await userEvent.click(await screen.findByText('Newsletter'))

    await goToTab('4. Content')
    await userEvent.type(screen.getByTestId('template-selector'), 'tmpl1')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(broadcastApi.create).toHaveBeenCalled())
    const payload = (broadcastApi.create as Mock).mock.calls[0][0]
    expect(payload.schedule).toEqual({ is_scheduled: false, use_recipient_timezone: false })
  })

  it('creates a new broadcast tracked, with the "Don\'t track" box unchecked', async () => {
    renderDrawer(<UpsertBroadcastDrawer workspace={workspace} lists={lists} />)
    await userEvent.click(screen.getByRole('button', { name: /Create Broadcast/i }))

    await userEvent.type(
      screen.getByPlaceholderText('E.g. Weekly Newsletter - May 2023'),
      'Weekly Newsletter'
    )
    await userEvent.click(screen.getAllByRole('combobox')[0])
    await userEvent.click(await screen.findByText('Newsletter'))

    await goToTab('2. Web Analytics')
    expect(screen.getByRole('checkbox', { name: /Don't track opens and clicks/i })).not.toBeChecked()

    await goToTab('4. Content')
    await userEvent.type(screen.getByTestId('template-selector'), 'tmpl1')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(broadcastApi.create).toHaveBeenCalled())
    expect((broadcastApi.create as Mock).mock.calls[0][0].tracking_mode).toBe('inherit')
  })
})

describe('UpsertBroadcastDrawer tracking', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(broadcastApi.update as Mock).mockResolvedValue({ broadcast: {} })
    ;(broadcastApi.create as Mock).mockResolvedValue({ broadcast: {} })
  })

  const noTrackingCheckbox = () =>
    screen.getByRole('checkbox', { name: /Don't track opens and clicks/i })

  it('follows the workspace and each contact by default', async () => {
    await openEditDrawer(makeBroadcast())

    await save()

    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    expect(updatePayload().tracking_mode).toBe('inherit')
  })

  it('sends no_tracking when the user turns tracking off for the broadcast', async () => {
    await openEditDrawer(makeBroadcast())

    await goToTab('2. Web Analytics')
    await userEvent.click(noTrackingCheckbox())
    await save()

    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    expect(updatePayload().tracking_mode).toBe('no_tracking')
  })

  it('shows the stored no_tracking mode checked and turns it back on', async () => {
    await openEditDrawer(makeBroadcast({ tracking_mode: 'no_tracking' }))

    await goToTab('2. Web Analytics')
    expect(noTrackingCheckbox()).toBeChecked()
    await userEvent.click(noTrackingCheckbox())
    await save()

    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    expect(updatePayload().tracking_mode).toBe('inherit')
  })

  it('turns off and locks the automatic winner when the broadcast is not tracked', async () => {
    await openEditDrawer(
      makeBroadcast({
        test_settings: {
          enabled: true,
          sample_percentage: 50,
          auto_send_winner: true,
          auto_send_winner_metric: 'open_rate',
          test_duration_hours: 2,
          variations: [
            { variation_name: 'A', template_id: 'tmpl1' },
            { variation_name: 'B', template_id: 'tmpl2' }
          ]
        }
      } as Partial<Broadcast>)
    )

    await goToTab('2. Web Analytics')
    await userEvent.click(noTrackingCheckbox())
    await goToTab('4. Content')

    const autoWinner = within(
      screen.getByText('Automatically send winner').closest('.ant-form-item') as HTMLElement
    ).getByRole('switch')
    expect(autoWinner).not.toBeChecked()
    expect(autoWinner).toBeDisabled()

    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    expect(updatePayload().test_settings.auto_send_winner).toBe(false)
    expect(updatePayload().tracking_mode).toBe('no_tracking')
  })

  it('warns that only consenting contacts count when unknown contacts are not tracked', async () => {
    const strictWorkspace = {
      ...workspace,
      settings: {
        ...workspace.settings,
        email_tracking: { opens_enabled: true, clicks_enabled: true, unknown_consent: 'no_track' }
      }
    } as unknown as Workspace
    renderDrawer(
      <UpsertBroadcastDrawer
        workspace={strictWorkspace}
        broadcast={makeBroadcast({
          test_settings: {
            enabled: true,
            sample_percentage: 50,
            auto_send_winner: false,
            variations: [
              { variation_name: 'A', template_id: 'tmpl1' },
              { variation_name: 'B', template_id: 'tmpl2' }
            ]
          }
        } as Partial<Broadcast>)}
        lists={lists}
      />
    )
    await userEvent.click(screen.getByRole('button', { name: /Edit Broadcast/i }))
    await goToTab('4. Content')

    expect(
      screen.getByText(/only recipients who agreed to tracking count in the test results/i)
    ).toBeInTheDocument()
  })

  it('says nothing about consent when unknown contacts are tracked', async () => {
    await openEditDrawer(
      makeBroadcast({
        test_settings: {
          enabled: true,
          sample_percentage: 50,
          auto_send_winner: false,
          variations: [
            { variation_name: 'A', template_id: 'tmpl1' },
            { variation_name: 'B', template_id: 'tmpl2' }
          ]
        }
      } as Partial<Broadcast>)
    )
    await goToTab('4. Content')

    expect(
      screen.queryByText(/only recipients who agreed to tracking count in the test results/i)
    ).not.toBeInTheDocument()
  })
})

// Switching the exclusion off is how a notice every member must receive is sent (issue #484);
// the switch says what else it changes: the emails carry no List-Unsubscribe header.
describe('UpsertBroadcastDrawer unsubscribed recipients', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('explains that including unsubscribed contacts drops the List-Unsubscribe header', async () => {
    await openEditDrawer(makeBroadcast())

    expect(screen.getByText('Exclude unsubscribed recipients')).toBeInTheDocument()
    expect(
      screen.getByText(/carry no List-Unsubscribe header, so mail clients show no Unsubscribe button/)
    ).toBeInTheDocument()
  })
})

// A variation can send from another sender than the one its template names. The broadcast goes
// out through the workspace's marketing provider, so only that provider's senders are offered.
const senderWorkspace = {
  ...workspace,
  settings: { ...workspace.settings, marketing_email_provider_id: 'int-marketing' },
  integrations: [
    {
      id: 'int-transactional',
      name: 'Transactional',
      type: 'email',
      email_provider: {
        kind: 'smtp',
        rate_limit_per_minute: 25,
        senders: [{ id: 'sender-alerts', email: 'alerts@example.com', name: 'Alerts', is_default: true }]
      }
    },
    {
      id: 'int-marketing',
      name: 'Marketing',
      type: 'email',
      email_provider: {
        kind: 'ses',
        rate_limit_per_minute: 25,
        senders: [
          { id: 'sender-hello', email: 'hello@example.com', name: 'Hello', is_default: true },
          { id: 'sender-support', email: 'support@example.com', name: 'Support', is_default: false }
        ]
      }
    }
  ]
} as unknown as Workspace

const HELLO = 'Hello <hello@example.com>'
const SUPPORT = 'Support <support@example.com>'

const singleVariation = (senderId?: string) =>
  makeBroadcast({
    test_settings: {
      enabled: false,
      sample_percentage: 50,
      auto_send_winner: false,
      variations: [{ variation_name: 'default', template_id: 'tmpl1', sender_id: senderId }]
    }
  } as Partial<Broadcast>)

const abTest = (variations: { template_id: string; sender_id?: string }[]) =>
  makeBroadcast({
    test_settings: {
      enabled: true,
      sample_percentage: 50,
      auto_send_winner: false,
      variations: variations.map((variation, i) => ({
        variation_name: String.fromCharCode(65 + i),
        ...variation
      }))
    }
  } as Partial<Broadcast>)

// The sender select of a variation, found through its label: "Sender", numbered once A/B testing
// lists several variations.
const senderSelect = (index = 0) => {
  const label = screen.getAllByText(/^Sender/, { selector: 'label' })[index]
  return within(label.closest('.ant-form-item') as HTMLElement).getByRole('combobox')
}

const optionContents = () =>
  Array.from(document.querySelectorAll<HTMLElement>('.ant-select-item-option-content'))

// Opens the sender select and returns its options in the order they are listed. Only one select
// is opened per test, so the options in the page are the ones of that select.
const openSenderOptions = async (index = 0) => {
  await userEvent.click(senderSelect(index))
  await waitFor(() => expect(optionContents().length).toBeGreaterThan(0))
  return optionContents()
}

const chooseSender = async (option: string, index = 0) => {
  const options = await openSenderOptions(index)
  await userEvent.click(options.find((o) => o.textContent === option) as HTMLElement)
}

const variationsSent = () => updatePayload().test_settings.variations

describe('UpsertBroadcastDrawer sender', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(broadcastApi.update as Mock).mockResolvedValue({ broadcast: {} })
  })

  it('lists the template sender first, then the marketing provider senders as Name <email>', async () => {
    await openEditDrawer(singleVariation(), senderWorkspace)
    await goToTab('4. Content')

    const options = await openSenderOptions()

    // The transactional provider's sender is left out: the broadcast does not go through it.
    expect(options.map((option) => option.textContent)).toEqual(['Template sender', HELLO, SUPPORT])
  })

  it('offers only the template sender when the workspace has no marketing provider senders', async () => {
    await openEditDrawer(singleVariation())
    await goToTab('4. Content')

    const options = await openSenderOptions()

    expect(options.map((option) => option.textContent)).toEqual(['Template sender'])
  })

  it('shows "Template sender" while the variation has no sender of its own', async () => {
    await openEditDrawer(singleVariation(), senderWorkspace)
    await goToTab('4. Content')

    const select = senderSelect().closest('.ant-select') as HTMLElement
    expect(within(select).getByText('Template sender')).toBeInTheDocument()
  })

  it('saves the sender chosen for the variation', async () => {
    await openEditDrawer(singleVariation(), senderWorkspace)
    await goToTab('4. Content')

    await chooseSender(SUPPORT)
    await save()

    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    expect(variationsSent()[0].sender_id).toBe('sender-support')
  })

  it('shows the sender stored on a broadcast and sends it back when the broadcast is saved', async () => {
    await openEditDrawer(singleVariation('sender-hello'), senderWorkspace)
    await goToTab('4. Content')

    const select = senderSelect().closest('.ant-select') as HTMLElement
    expect(within(select).getByText(HELLO)).toBeInTheDocument()

    await save()

    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    expect(variationsSent()[0].sender_id).toBe('sender-hello')
  })

  it('sends no sender once the user goes back to "Template sender"', async () => {
    await openEditDrawer(singleVariation('sender-hello'), senderWorkspace)
    await goToTab('4. Content')

    await chooseSender('Template sender')
    await save()

    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    expect(variationsSent()[0].sender_id).toBeUndefined()
  })

  it('offers a sender select on every A/B variation and saves the choice of each', async () => {
    await openEditDrawer(
      abTest([{ template_id: 'tmpl1' }, { template_id: 'tmpl2' }]),
      senderWorkspace
    )
    await goToTab('4. Content')

    expect(screen.getAllByText(/^Sender/, { selector: 'label' })).toHaveLength(2)

    await chooseSender(SUPPORT, 1)
    await save()

    await waitFor(() => expect(broadcastApi.update).toHaveBeenCalled())
    expect(variationsSent()[0].sender_id).toBeUndefined()
    expect(variationsSent()[1].sender_id).toBe('sender-support')
  })

  it('numbers the senders of the A/B variations from 1', async () => {
    await openEditDrawer(
      abTest([{ template_id: 'tmpl1' }, { template_id: 'tmpl2' }]),
      senderWorkspace
    )
    await goToTab('4. Content')

    expect(screen.getByText('Sender 1', { selector: 'label' })).toBeInTheDocument()
    expect(screen.getByText('Sender 2', { selector: 'label' })).toBeInTheDocument()
  })

  // A row keeps the key it was given when an earlier one is removed, so numbering by the key
  // left "Sender 2" and "Template 2" on the first row of what remained.
  it('numbers the senders and templates by their row once a variation is removed', async () => {
    await openEditDrawer(
      abTest([{ template_id: 'tmpl1' }, { template_id: 'tmpl2' }, { template_id: 'tmpl3' }]),
      senderWorkspace
    )
    await goToTab('4. Content')

    await userEvent.click(screen.getAllByRole('button', { name: /delete/i })[0])
    await userEvent.click(await screen.findByRole('button', { name: 'Yes' }))

    await waitFor(() =>
      expect(screen.getAllByText(/^Sender/, { selector: 'label' })).toHaveLength(2)
    )
    // The first variation is the one that went
    expect(
      screen.getAllByTestId('template-selector').map((input) => (input as HTMLInputElement).value)
    ).toEqual(['tmpl2', 'tmpl3'])
    expect(screen.getByText('Sender 1', { selector: 'label' })).toBeInTheDocument()
    expect(screen.getByText('Sender 2', { selector: 'label' })).toBeInTheDocument()
    expect(screen.queryByText('Sender 3', { selector: 'label' })).not.toBeInTheDocument()
    expect(screen.getByText('Template 1', { selector: 'label' })).toBeInTheDocument()
    expect(screen.getByText('Template 2', { selector: 'label' })).toBeInTheDocument()
    expect(screen.queryByText('Template 3', { selector: 'label' })).not.toBeInTheDocument()
  })

  // The marketing provider is the one the broadcast goes through; when it cannot be found, or has
  // no senders to list, the select holds only "Template sender". The workspace keeps other
  // integrations with senders, so a lookup that ignored the provider id would offer one.
  it.each([
    [
      'is not set',
      {
        settings: { ...senderWorkspace.settings, marketing_email_provider_id: undefined },
        integrations: senderWorkspace.integrations
      }
    ],
    [
      'is not among the integrations of the workspace',
      {
        settings: { ...senderWorkspace.settings, marketing_email_provider_id: 'int-gone' },
        integrations: senderWorkspace.integrations
      }
    ],
    [
      'has no email provider',
      {
        settings: senderWorkspace.settings,
        integrations: [
          ...(senderWorkspace.integrations ?? []).slice(0, 1),
          { id: 'int-marketing', name: 'Marketing', type: 'supabase' }
        ]
      }
    ],
    [
      'lists no senders',
      {
        settings: senderWorkspace.settings,
        integrations: [
          ...(senderWorkspace.integrations ?? []).slice(0, 1),
          {
            id: 'int-marketing',
            name: 'Marketing',
            type: 'email',
            email_provider: { kind: 'ses', rate_limit_per_minute: 25 }
          }
        ]
      }
    ]
  ])('offers only the template sender when the marketing provider %s', async (_, patch) => {
    const drawerWorkspace = { ...senderWorkspace, ...patch } as unknown as Workspace
    await openEditDrawer(singleVariation(), drawerWorkspace)
    await goToTab('4. Content')

    const options = await openSenderOptions()

    expect(options.map((option) => option.textContent)).toEqual(['Template sender'])
  })

  it('offers the senders of a marketing provider the workspace gained after the drawer first rendered', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const broadcast = singleVariation()
    const drawerFor = (drawerWorkspace: Workspace) => (
      <QueryClientProvider client={queryClient}>
        <AntApp>
          <UpsertBroadcastDrawer workspace={drawerWorkspace} broadcast={broadcast} lists={lists} />
        </AntApp>
      </QueryClientProvider>
    )
    const { rerender } = render(drawerFor(workspace))
    rerender(drawerFor(senderWorkspace))
    await userEvent.click(screen.getByRole('button', { name: /Edit Broadcast/i }))
    await goToTab('4. Content')

    const options = await openSenderOptions()

    expect(options.map((option) => option.textContent)).toEqual(['Template sender', HELLO, SUPPORT])
  })
})

describe('UpsertBroadcastDrawer single template', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(broadcastApi.update as Mock).mockResolvedValue({ broadcast: {} })
  })

  const withoutTemplate = () =>
    makeBroadcast({
      test_settings: {
        enabled: false,
        sample_percentage: 50,
        auto_send_winner: false,
        variations: [{ variation_name: 'default', template_id: '' }]
      }
    } as Partial<Broadcast>)

  it('labels the template field and prompts to select a template', async () => {
    await openEditDrawer(singleVariation(), senderWorkspace)
    await goToTab('4. Content')

    expect(screen.getByText('Template', { selector: 'label' })).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Select template')).toBeInTheDocument()
  })

  it('refuses to save without a template and marks the template field as required', async () => {
    await openEditDrawer(withoutTemplate(), senderWorkspace)

    await save()

    const field = screen
      .getByText('Template', { selector: 'label' })
      .closest('.ant-form-item') as HTMLElement
    // antd renders the message itself through a motion wrapper that jsdom never runs (the
    // ant-form-item-additional box stays empty), so the field's error state is the assertion.
    await waitFor(() => expect(field).toHaveClass('ant-form-item-has-error'))
    expect(broadcastApi.update).not.toHaveBeenCalled()
  })
})

describe('UpsertBroadcastDrawer duplicate template', () => {
  const duplicateMessage = 'This template is already used in another variation'

  beforeEach(() => {
    vi.clearAllMocks()
    ;(broadcastApi.update as Mock).mockResolvedValue({ broadcast: {} })
  })

  it('rejects the same template in two variations, whatever their senders', async () => {
    await openEditDrawer(
      abTest([
        { template_id: 'tmpl1', sender_id: 'sender-hello' },
        { template_id: 'tmpl1', sender_id: 'sender-support' }
      ]),
      senderWorkspace
    )

    await save()

    expect((await screen.findAllByText(duplicateMessage)).length).toBeGreaterThan(0)
    expect(broadcastApi.update).not.toHaveBeenCalled()
  })
})
