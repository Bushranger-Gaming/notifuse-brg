import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import type { NodeProps } from '@xyflow/react'
import { EmailNode } from './EmailNode'
import type { AutomationFlowNode, AutomationNodeData } from '../utils/flowConverter'
import type { Template, Workspace } from '../../../services/api/types'

// EmailNode only needs Handle/Position/useConnection from @xyflow/react; render
// them as inert so the node can mount without a ReactFlow provider.
vi.mock('@xyflow/react', () => ({
  Handle: () => null,
  Position: { Top: 'top', Bottom: 'bottom' },
  useConnection: () => ({ inProgress: false })
}))

// The reference list of templates comes from the automation context. It is
// populated by AutomationsPage's templates query; EmailNode resolves the
// selected template_id against it to display the template name on the canvas.
// The workspace comes from the same context: its integrations hold the senders.
let mockTemplates: Template[] = []
let mockWorkspace: Workspace
vi.mock('../context', () => ({
  useAutomation: () => ({ templates: mockTemplates, workspace: mockWorkspace })
}))

const makeIntegration = (id: string, sender: { id: string; email: string }) => ({
  id,
  name: id,
  type: 'email',
  email_provider: { kind: 'ses', senders: [{ ...sender, name: 'Sender', is_default: true }] }
})

// The workspace default is the marketing provider; "int-other" is an integration a node can pick.
const senderWorkspace = {
  id: 'ws1',
  settings: { marketing_email_provider_id: 'int-marketing' },
  integrations: [
    makeIntegration('int-marketing', { id: 'sender-hello', email: 'hello@example.com' }),
    makeIntegration('int-other', { id: 'sender-ops', email: 'ops@example.com' })
  ]
} as unknown as Workspace

beforeEach(() => {
  mockWorkspace = senderWorkspace
})

const workspaceWith = (patch: Record<string, unknown>) =>
  ({ ...senderWorkspace, ...patch }) as unknown as Workspace

const makeTemplate = (id: string, name: string, category: string): Template =>
  ({ id, name, category, channel: 'email' } as Template)

const renderNode = (config: Record<string, unknown>) => {
  const data: AutomationNodeData = { nodeType: 'email', config, label: 'Email' }
  // EmailNode only reads `data` and `selected`; the rest of NodeProps is inert here.
  const props = { data, selected: false } as NodeProps<AutomationFlowNode>
  return render(
    <I18nProvider i18n={i18n}>
      <EmailNode {...props} />
    </I18nProvider>
  )
}

describe('EmailNode', () => {
  it('shows the template name for a selected template of any category', () => {
    // Regression guard for the "Template set" bug: a welcome-category template is
    // a valid selection for an automation email node (welcome/unsubscribe emails
    // are sent via automations since migration v20), so as long as the reference
    // list contains it, the canvas must show its name — not the "Template set"
    // fallback. This only holds because AutomationsPage no longer restricts the
    // reference list to category:'marketing'.
    mockTemplates = [makeTemplate('welcome-pdf', 'Send Checklist PDF', 'welcome')]
    renderNode({ template_id: 'welcome-pdf' })

    expect(screen.getByText('Send Checklist PDF')).toBeInTheDocument()
    expect(screen.queryByText('Template set')).not.toBeInTheDocument()
  })

  it('falls back to "Template set" when a template is selected but absent from the list', () => {
    mockTemplates = []
    renderNode({ template_id: 'missing-id' })

    expect(screen.getByText('Template set')).toBeInTheDocument()
  })

  it('prompts to select when no template is configured', () => {
    mockTemplates = []
    renderNode({})

    expect(screen.getByText('Select')).toBeInTheDocument()
  })

  it('shows the author description alongside the template name', () => {
    // Every node type carries an optional description; this covers the wiring from the config bag
    // through to BaseNode, which the other eight node components repeat verbatim.
    mockTemplates = [makeTemplate('welcome-pdf', 'Send Checklist PDF', 'welcome')]
    renderNode({ template_id: 'welcome-pdf', description: 'Welcome — day 1' })

    expect(screen.getByText('Welcome — day 1')).toBeInTheDocument()
    expect(screen.getByText('Send Checklist PDF')).toBeInTheDocument()
  })

  it('shows the email of the sender chosen on the node, from the workspace default provider', () => {
    mockTemplates = [makeTemplate('welcome-pdf', 'Send Checklist PDF', 'welcome')]
    mockWorkspace = senderWorkspace
    renderNode({ template_id: 'welcome-pdf', sender_id: 'sender-hello' })

    expect(screen.getByText('Send Checklist PDF')).toBeInTheDocument()
    expect(screen.getByText('hello@example.com')).toBeInTheDocument()
  })

  it('looks the sender up among the senders of the integration chosen on the node', () => {
    mockTemplates = [makeTemplate('welcome-pdf', 'Send Checklist PDF', 'welcome')]
    mockWorkspace = senderWorkspace
    renderNode({ template_id: 'welcome-pdf', integration_id: 'int-other', sender_id: 'sender-ops' })

    expect(screen.getByText('ops@example.com')).toBeInTheDocument()
  })

  it('shows no sender when the node keeps the template sender', () => {
    mockTemplates = [makeTemplate('welcome-pdf', 'Send Checklist PDF', 'welcome')]
    mockWorkspace = senderWorkspace
    renderNode({ template_id: 'welcome-pdf' })

    expect(screen.queryByText('hello@example.com')).not.toBeInTheDocument()
    expect(screen.queryByText('ops@example.com')).not.toBeInTheDocument()
    // the template name is the only line under the label: no empty sender line either
    expect(screen.getByText('Send Checklist PDF').parentElement?.children).toHaveLength(1)
  })

  it('shows the sender in a line of its own under the template name, with its email as the tooltip', () => {
    mockTemplates = [makeTemplate('welcome-pdf', 'Send Checklist PDF', 'welcome')]
    renderNode({ template_id: 'welcome-pdf', sender_id: 'sender-hello' })

    expect(screen.getByText('Send Checklist PDF').parentElement?.children).toHaveLength(2)
    expect(screen.getByText('hello@example.com')).toHaveAttribute('title', 'hello@example.com')
  })

  it('shows the id of a sender the provider no longer has, not the email of another sender', () => {
    // The send fails on a sender the provider does not have, so the card says which one it is
    // asked for, as the broadcasts list does for a deleted override.
    mockTemplates = [makeTemplate('welcome-pdf', 'Send Checklist PDF', 'welcome')]
    renderNode({ template_id: 'welcome-pdf', sender_id: 'sender-gone' })

    expect(screen.getByText('sender-gone')).toBeInTheDocument()
    expect(screen.queryByText('hello@example.com')).not.toBeInTheDocument()
  })

  it('looks the sender up on the integration chosen on the node only', () => {
    // sender-hello belongs to the marketing provider, not to the integration the node picked
    mockTemplates = [makeTemplate('welcome-pdf', 'Send Checklist PDF', 'welcome')]
    renderNode({ template_id: 'welcome-pdf', integration_id: 'int-other', sender_id: 'sender-hello' })

    expect(screen.getByText('sender-hello')).toBeInTheDocument()
    expect(screen.queryByText('hello@example.com')).not.toBeInTheDocument()
  })

  // Each of these keeps the marketing provider's senders somewhere in the workspace, so a lookup
  // that ignored the provider id would find 'sender-hello'.
  it.each([
    ['the workspace has no integrations', {}, workspaceWith({ integrations: undefined })],
    ['the workspace sets no marketing provider', {}, workspaceWith({ settings: {} })],
    [
      'the integration of the node is not in the workspace',
      { integration_id: 'int-gone' },
      senderWorkspace
    ],
    [
      // The API sends senders: null for a provider that has none; the canvas must still draw
      'the senders of the marketing provider are null',
      {},
      workspaceWith({
        integrations: [
          {
            id: 'int-marketing',
            name: 'int-marketing',
            type: 'email',
            email_provider: { kind: 'ses', senders: null }
          }
        ]
      })
    ],
    [
      'the integration of the node has no email provider',
      { integration_id: 'int-supabase' },
      workspaceWith({
        integrations: [
          ...(senderWorkspace.integrations ?? []),
          { id: 'int-supabase', name: 'Supabase', type: 'supabase' }
        ]
      })
    ]
  ])('shows the id of the sender when %s', (_, nodeConfig, workspace) => {
    mockTemplates = [makeTemplate('welcome-pdf', 'Send Checklist PDF', 'welcome')]
    mockWorkspace = workspace
    renderNode({ template_id: 'welcome-pdf', sender_id: 'sender-hello', ...nodeConfig })

    expect(screen.getByText('sender-hello')).toBeInTheDocument()
    expect(screen.queryByText('hello@example.com')).not.toBeInTheDocument()
  })

  it('prompts to select when the node has no config at all', () => {
    // A node the API returned without a config bag still renders, with no sender to look up
    mockTemplates = []
    renderNode(undefined as unknown as Record<string, unknown>)

    expect(screen.getByText('Select')).toBeInTheDocument()
  })
})
