import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { EmailConfigForm } from './EmailConfigForm'
import type { EmailNodeConfig } from '../../../services/api/automation'
import type { Workspace } from '../../../services/api/types'

// The template selector pulls in template list queries; the sender does not depend on it.
vi.mock('../../templates/TemplateSelectorInput', () => ({
  default: () => <input />
}))

const integration = (
  id: string,
  name: string,
  senders: { id: string; name: string; email: string }[]
) => ({
  id,
  name,
  type: 'email',
  email_provider: {
    kind: 'ses',
    rate_limit_per_minute: 25,
    senders: senders.map((sender, i) => ({ ...sender, is_default: i === 0 }))
  }
})

const hello = { id: 'sender-hello', name: 'Hello', email: 'hello@example.com' }
const news = { id: 'sender-news', name: 'News', email: 'news@example.com' }
const ops = { id: 'sender-ops', name: 'Ops', email: 'ops@example.com' }

// The workspace default is the marketing provider. "Other" lists a sender the marketing provider
// does not have; "Mirror" lists one it has too.
const workspace = {
  id: 'ws1',
  settings: { marketing_email_provider_id: 'int-marketing' },
  integrations: [
    integration('int-marketing', 'Marketing', [hello, news]),
    integration('int-other', 'Other', [ops]),
    integration('int-mirror', 'Mirror', [news])
  ]
} as unknown as Workspace

const renderForm = (config: EmailNodeConfig, formWorkspace: Workspace = workspace) => {
  const onChange = vi.fn()
  render(
    <I18nProvider i18n={i18n}>
      <EmailConfigForm
        config={config}
        onChange={onChange}
        workspaceId="ws1"
        workspace={formWorkspace}
      />
    </I18nProvider>
  )
  return onChange
}

// The workspace above with its integrations, or its settings, replaced
const workspaceWith = (patch: Record<string, unknown>) =>
  ({ ...workspace, ...patch }) as unknown as Workspace

// The select of the form item labelled `label`.
const selectOf = (label: string) =>
  within(screen.getByText(label).closest('.ant-form-item') as HTMLElement).getByRole('combobox')

const optionContents = () =>
  Array.from(document.querySelectorAll<HTMLElement>('.ant-select-item-option-content'))

// Opens a select and returns its options in the order they are listed. Each test opens one
// select, so the options in the page are the ones of that select.
const openOptions = async (label: string) => {
  await userEvent.click(selectOf(label))
  await waitFor(() => expect(optionContents().length).toBeGreaterThan(0))
  return optionContents()
}

const choose = async (label: string, option: string) => {
  const options = await openOptions(label)
  await userEvent.click(options.find((o) => o.textContent === option) as HTMLElement)
}

const lastConfig = (onChange: { mock: { calls: unknown[][] } }) =>
  onChange.mock.calls[onChange.mock.calls.length - 1][0] as EmailNodeConfig

describe('EmailConfigForm sender', () => {
  it('lists the template sender first, then the senders of the workspace default provider', async () => {
    renderForm({ template_id: 'tmpl1' })

    const options = await openOptions('Sender')

    expect(options.map((option) => option.textContent)).toEqual([
      'Template sender',
      'Hello <hello@example.com>',
      'News <news@example.com>'
    ])
  })

  it('lists the senders of the integration chosen on the node instead', async () => {
    renderForm({ template_id: 'tmpl1', integration_id: 'int-other' })

    const options = await openOptions('Sender')

    expect(options.map((option) => option.textContent)).toEqual([
      'Template sender',
      'Ops <ops@example.com>'
    ])
  })

  it('shows "Template sender" while the node has no sender of its own', () => {
    renderForm({ template_id: 'tmpl1' })

    const select = selectOf('Sender').closest('.ant-select') as HTMLElement
    expect(within(select).getByText('Template sender')).toBeInTheDocument()
  })

  it('shows the sender stored on the node', () => {
    renderForm({ template_id: 'tmpl1', sender_id: 'sender-news' })

    const select = selectOf('Sender').closest('.ant-select') as HTMLElement
    expect(within(select).getByText('News <news@example.com>')).toBeInTheDocument()
  })

  it('saves the chosen sender in the node config', async () => {
    const onChange = renderForm({ template_id: 'tmpl1' })

    await choose('Sender', 'News <news@example.com>')

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(lastConfig(onChange)).toEqual({ template_id: 'tmpl1', sender_id: 'sender-news' })
  })

  it('removes the sender from the node config when "Template sender" is chosen', async () => {
    const onChange = renderForm({ template_id: 'tmpl1', sender_id: 'sender-news' })

    await choose('Sender', 'Template sender')

    expect(onChange).toHaveBeenCalledTimes(1)
    expect('sender_id' in lastConfig(onChange)).toBe(false)
    expect(lastConfig(onChange)).toEqual({ template_id: 'tmpl1' })
  })

  it('clears a sender the new integration does not list when the integration changes', async () => {
    const onChange = renderForm({
      template_id: 'tmpl1',
      integration_id: 'int-marketing',
      sender_id: 'sender-news'
    })

    await choose('Email Integration', 'Other')

    expect(onChange).toHaveBeenCalledTimes(1)
    expect('sender_id' in lastConfig(onChange)).toBe(false)
    expect(lastConfig(onChange)).toEqual({ template_id: 'tmpl1', integration_id: 'int-other' })
  })

  it('keeps the sender when the new integration lists it too', async () => {
    const onChange = renderForm({
      template_id: 'tmpl1',
      integration_id: 'int-marketing',
      sender_id: 'sender-news'
    })

    await choose('Email Integration', 'Mirror')

    expect(lastConfig(onChange)).toEqual({
      template_id: 'tmpl1',
      integration_id: 'int-mirror',
      sender_id: 'sender-news'
    })
  })

  it('clears a sender the workspace default provider does not list when going back to the default', async () => {
    const onChange = renderForm({
      template_id: 'tmpl1',
      integration_id: 'int-other',
      sender_id: 'sender-ops'
    })

    await choose('Email Integration', 'Use workspace default')

    expect('integration_id' in lastConfig(onChange)).toBe(false)
    expect('sender_id' in lastConfig(onChange)).toBe(false)
    expect(lastConfig(onChange)).toEqual({ template_id: 'tmpl1' })
  })

  it('keeps a sender the workspace default provider lists when going back to the default', async () => {
    const onChange = renderForm({
      template_id: 'tmpl1',
      integration_id: 'int-mirror',
      sender_id: 'sender-news'
    })

    await choose('Email Integration', 'Use workspace default')

    expect('integration_id' in lastConfig(onChange)).toBe(false)
    expect(lastConfig(onChange)).toEqual({ template_id: 'tmpl1', sender_id: 'sender-news' })
  })
})

describe('EmailConfigForm integration field', () => {
  const integrationSelect = () => selectOf('Email Integration').closest('.ant-select') as HTMLElement

  it('says it overrides the workspace default provider, and the sender field says what it overrides', () => {
    renderForm({ template_id: 'tmpl1' })

    expect(
      screen.getByText('Override the workspace default email provider for this node')
    ).toBeInTheDocument()
    expect(
      screen.getByText('Override the sender of the template for this node')
    ).toBeInTheDocument()
  })

  it('shows "Use workspace default" while the node has no integration of its own', () => {
    renderForm({ template_id: 'tmpl1' })

    expect(within(integrationSelect()).getByText('Use workspace default')).toBeInTheDocument()
  })

  it('shows the name of the integration stored on the node', () => {
    renderForm({ template_id: 'tmpl1', integration_id: 'int-other' })

    expect(within(integrationSelect()).getByText('Other')).toBeInTheDocument()
    expect(within(integrationSelect()).queryByText('Use workspace default')).not.toBeInTheDocument()
  })

  it('has no integration or sender field when the workspace has no email integration', () => {
    renderForm({ template_id: 'tmpl1' }, workspaceWith({ integrations: undefined }))

    expect(screen.queryByText('Email Integration')).not.toBeInTheDocument()
    expect(screen.queryByText('Sender')).not.toBeInTheDocument()
  })
})

// The integration chosen on the node, or the marketing provider when it has none, may leave no
// senders to offer: the sender select then holds only "Template sender".
const bareProvider = {
  id: 'int-bare',
  name: 'Bare',
  type: 'email',
  email_provider: { kind: 'ses', rate_limit_per_minute: 25 }
}
const supabase = { id: 'int-supabase', name: 'Supabase', type: 'supabase' }
const withIntegration = (extra: object) =>
  workspaceWith({ integrations: [...(workspace.integrations ?? []), extra] })

describe('EmailConfigForm sender when the provider has none to offer', () => {
  it.each([
    ['the integration of the node is not in the workspace', { integration_id: 'int-gone' }, workspace],
    [
      'the integration of the node lists no senders',
      { integration_id: 'int-bare' },
      withIntegration(bareProvider)
    ],
    [
      'the integration of the node has no email provider',
      { integration_id: 'int-supabase' },
      withIntegration(supabase)
    ],
    ['the workspace sets no marketing provider', {}, workspaceWith({ settings: {} })]
  ])('offers only "Template sender" when %s', async (_, nodeConfig, formWorkspace) => {
    renderForm({ template_id: 'tmpl1', ...nodeConfig }, formWorkspace)

    const options = await openOptions('Sender')

    expect(options.map((option) => option.textContent)).toEqual(['Template sender'])
  })
})

// The workspace is read again while the form is on screen: the integrations it lists are those
// the workspace has now
describe('EmailConfigForm when the integrations of the workspace change', () => {
  const form = (formWorkspace: Workspace) => (
    <I18nProvider i18n={i18n}>
      <EmailConfigForm
        config={{ template_id: 'tmpl1' }}
        onChange={vi.fn()}
        workspaceId="ws1"
        workspace={formWorkspace}
      />
    </I18nProvider>
  )

  it('lists an integration the workspace gains', async () => {
    const { rerender } = render(form(workspace))

    rerender(form(withIntegration(integration('int-new', 'Newly added', [ops]))))

    const options = await openOptions('Email Integration')
    expect(options.map((option) => option.textContent)).toEqual([
      'Use workspace default',
      'Marketing',
      'Other',
      'Mirror',
      'Newly added'
    ])
  })

  it('shows the integration and sender fields once the workspace has an email integration', () => {
    const { rerender } = render(form(workspaceWith({ integrations: undefined })))
    expect(screen.queryByText('Email Integration')).not.toBeInTheDocument()

    rerender(form(workspace))

    expect(screen.getByText('Email Integration')).toBeInTheDocument()
    expect(screen.getByText('Sender')).toBeInTheDocument()
  })
})
