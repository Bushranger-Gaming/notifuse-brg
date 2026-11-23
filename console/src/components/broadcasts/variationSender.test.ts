import { describe, it, expect } from 'vitest'
import { getVariationSender, resolveVariationTestSender } from './variationSender'
import type { BroadcastVariation } from '../../services/api/broadcast'
import type { Workspace } from '../../services/api/types'

const emailIntegration = (id: string, senders: { id: string; name: string; email: string }[]) => ({
  id,
  name: id,
  type: 'email',
  email_provider: { kind: 'smtp', senders }
})

// Both providers hold a sender under the same id, so only the provider a sender is looked up on
// tells which of the two comes back.
const settings = {
  marketing_email_provider_id: 'int-marketing',
  transactional_email_provider_id: 'int-transactional'
}
const integrations = [
  emailIntegration('int-marketing', [
    { id: 'sender-shared', name: 'Marketing', email: 'marketing@example.com' },
    { id: 'sender-support', name: 'Support', email: 'support@example.com' }
  ]),
  emailIntegration('int-transactional', [
    { id: 'sender-shared', name: 'Transactional', email: 'transactional@example.com' }
  ])
]

const workspaceWith = (patch: Record<string, unknown> = {}) =>
  ({ id: 'ws-1', settings, integrations, ...patch }) as unknown as Workspace

const variationOf = (template: object | undefined, senderId?: string) =>
  ({
    variation_name: 'A',
    template_id: 'tmpl-1',
    sender_id: senderId,
    template
  }) as unknown as BroadcastVariation

const templateOf = (category: string, senderId?: string) => ({
  id: 'tmpl-1',
  category,
  email: { sender_id: senderId }
})

describe('getVariationSender template sender', () => {
  it('is looked up on the marketing provider for a marketing template', () => {
    const variation = variationOf(templateOf('marketing', 'sender-shared'))

    expect(getVariationSender(variation, workspaceWith())).toBe('Marketing <marketing@example.com>')
  })

  // A broadcast always sends through the marketing provider, so the From line resolves there
  // whatever the category of the template, never on the transactional provider
  it('is looked up on the marketing provider for a transactional template too', () => {
    const variation = variationOf(templateOf('transactional', 'sender-shared'))

    expect(getVariationSender(variation, workspaceWith())).toBe('Marketing <marketing@example.com>')
  })

  it('is undefined for a template whose sender only the transactional provider has', () => {
    const variation = variationOf(templateOf('transactional', 'sender-transactional-only'))
    const workspace = workspaceWith({
      integrations: [
        integrations[0],
        emailIntegration('int-transactional', [
          {
            id: 'sender-transactional-only',
            name: 'Transactional',
            email: 'transactional@example.com'
          }
        ])
      ]
    })

    expect(getVariationSender(variation, workspace)).toBeUndefined()
  })

  it('does not depend on the workspace setting a transactional provider', () => {
    const variation = variationOf(templateOf('transactional', 'sender-shared'))
    const workspace = workspaceWith({
      settings: { ...settings, transactional_email_provider_id: undefined }
    })

    expect(getVariationSender(variation, workspace)).toBe('Marketing <marketing@example.com>')
  })

  it('is undefined when the template has no email content', () => {
    const variation = variationOf({ id: 'tmpl-1', category: 'marketing' })

    expect(getVariationSender(variation, workspaceWith())).toBeUndefined()
  })

  it('is undefined when the template names no sender, whatever senders the provider has', () => {
    const variation = variationOf(templateOf('marketing'))

    expect(getVariationSender(variation, workspaceWith())).toBeUndefined()
  })

  it('is undefined when the variation has no template loaded', () => {
    expect(getVariationSender(variationOf(undefined), workspaceWith())).toBeUndefined()
  })
})

// A workspace missing a piece of what a sender is resolved through: the override falls back to its
// id, the template sender to undefined, and neither throws. Integrations that remain still hold
// senders, so a lookup that ignored the provider id would find one.
const incompleteWorkspaces: [string, Workspace][] = [
  ['has no integrations', workspaceWith({ integrations: undefined })],
  [
    'sets no marketing provider',
    workspaceWith({ settings: { ...settings, marketing_email_provider_id: undefined } })
  ],
  [
    'sets a marketing provider it has no integration for',
    workspaceWith({ settings: { ...settings, marketing_email_provider_id: 'int-gone' } })
  ],
  [
    'has a marketing integration without an email provider',
    workspaceWith({
      integrations: [
        { id: 'int-marketing', name: 'Marketing', type: 'supabase' },
        ...integrations.slice(1)
      ]
    })
  ]
]

describe('getVariationSender in a workspace that', () => {
  it.each(incompleteWorkspaces)('%s returns the override by its id', (_, workspace) => {
    const variation = variationOf(templateOf('marketing', 'sender-shared'), 'sender-support')

    expect(getVariationSender(variation, workspace)).toBe('sender-support')
  })

  it.each(incompleteWorkspaces)('%s returns no template sender', (_, workspace) => {
    const variation = variationOf(templateOf('marketing', 'sender-shared'))

    expect(getVariationSender(variation, workspace)).toBeUndefined()
  })
})

// The server leaves out the email of a template that has none
describe('getVariationSender with a template that has no email content', () => {
  it('names no sender while the variation has no override', () => {
    const template = { id: 'tmpl-1', category: 'marketing' }

    expect(getVariationSender(variationOf(template), workspaceWith())).toBeUndefined()
  })

  it('names the override of the variation', () => {
    const template = { id: 'tmpl-1', category: 'marketing' }

    expect(getVariationSender(variationOf(template, 'sender-support'), workspaceWith())).toBe(
      'Support <support@example.com>'
    )
  })
})

describe('getVariationSender without a workspace', () => {
  it('returns the override by its id', () => {
    const variation = variationOf(templateOf('marketing', 'sender-shared'), 'sender-support')

    expect(getVariationSender(variation, undefined)).toBe('sender-support')
  })
})

// The sender a test email goes out from, on the marketing provider and nowhere else: both providers
// hold a sender under the id sender-shared, and the transactional one holds a default of its own
const senderOf = (id: string, isDefault = false) => ({
  id,
  name: id,
  email: `${id}@example.com`,
  is_default: isDefault
})
const testWorkspace = (senders: ReturnType<typeof senderOf>[]) =>
  workspaceWith({
    integrations: [
      {
        ...emailIntegration('int-marketing', []),
        email_provider: { kind: 'smtp', senders }
      },
      {
        ...emailIntegration('int-transactional', []),
        email_provider: {
          kind: 'smtp',
          senders: [senderOf('sender-shared'), senderOf('sender-transactional-default', true)]
        }
      }
    ]
  })
const marketingSenders = [
  senderOf('sender-default', true),
  senderOf('sender-shared'),
  senderOf('sender-support')
]

describe('resolveVariationTestSender', () => {
  it('is the override of the variation, found on the marketing provider', () => {
    const variation = variationOf(templateOf('marketing', 'sender-shared'), 'sender-support')

    expect(resolveVariationTestSender(variation, testWorkspace(marketingSenders))).toEqual({
      status: 'ready',
      integrationId: 'int-marketing',
      sender: senderOf('sender-support'),
      source: 'variation'
    })
  })

  it('is the sender of the template when the variation overrides nothing', () => {
    const variation = variationOf(templateOf('marketing', 'sender-shared'))

    expect(resolveVariationTestSender(variation, testWorkspace(marketingSenders))).toEqual({
      status: 'ready',
      integrationId: 'int-marketing',
      sender: senderOf('sender-shared'),
      source: 'template'
    })
  })

  it('is the default sender of the marketing provider when neither names a sender', () => {
    const variation = variationOf(templateOf('marketing'))

    expect(resolveVariationTestSender(variation, testWorkspace(marketingSenders))).toEqual({
      status: 'ready',
      integrationId: 'int-marketing',
      sender: senderOf('sender-default', true),
      source: 'default'
    })
  })

  it('is the default sender when the variation has no template loaded', () => {
    expect(
      resolveVariationTestSender(variationOf(undefined), testWorkspace(marketingSenders))
    ).toMatchObject({ status: 'ready', source: 'default', sender: { id: 'sender-default' } })
  })

  // The broadcast does the same: a sender of the template that the provider lacks is not an error
  it('is the default sender when the sender of the template is not on the marketing provider', () => {
    const variation = variationOf(templateOf('marketing', 'sender-transactional-default'))

    expect(resolveVariationTestSender(variation, testWorkspace(marketingSenders))).toMatchObject({
      status: 'ready',
      source: 'default',
      sender: { id: 'sender-default' }
    })
  })

  // The backend falls back to the sender flagged as default only, and fails when none is flagged,
  // so the first sender is never taken for one
  it('reports no default sender when neither names a sender and the marketing provider flags none', () => {
    const variation = variationOf(templateOf('marketing'))
    const senders = [senderOf('sender-first'), senderOf('sender-second')]

    expect(resolveVariationTestSender(variation, testWorkspace(senders))).toEqual({
      status: 'no-default'
    })
  })

  it('reports no default sender when the sender of the template is not on a provider that flags none', () => {
    const variation = variationOf(templateOf('marketing', 'sender-transactional-default'))
    const senders = [senderOf('sender-first'), senderOf('sender-second')]

    expect(resolveVariationTestSender(variation, testWorkspace(senders))).toEqual({
      status: 'no-default'
    })
  })

  it('reports no default sender when the variation has no template loaded and the provider flags none', () => {
    const senders = [senderOf('sender-first'), senderOf('sender-second')]

    expect(resolveVariationTestSender(variationOf(undefined), testWorkspace(senders))).toEqual({
      status: 'no-default'
    })
  })

  // No default is needed while a sender is named: the override and the template sender are found
  // by their ids
  it('is the override of the variation when the marketing provider flags no default', () => {
    const variation = variationOf(templateOf('marketing', 'sender-second'), 'sender-first')
    const senders = [senderOf('sender-first'), senderOf('sender-second')]

    expect(resolveVariationTestSender(variation, testWorkspace(senders))).toEqual({
      status: 'ready',
      integrationId: 'int-marketing',
      sender: senderOf('sender-first'),
      source: 'variation'
    })
  })

  it('is the sender of the template when the marketing provider flags no default', () => {
    const variation = variationOf(templateOf('marketing', 'sender-second'))
    const senders = [senderOf('sender-first'), senderOf('sender-second')]

    expect(resolveVariationTestSender(variation, testWorkspace(senders))).toEqual({
      status: 'ready',
      integrationId: 'int-marketing',
      sender: senderOf('sender-second'),
      source: 'template'
    })
  })

  // The flag picks the default, not the position: here the flagged sender is not the first
  it('is the sender the marketing provider flags as default when it is not the first', () => {
    const variation = variationOf(templateOf('marketing'))
    const senders = [
      senderOf('sender-first'),
      senderOf('sender-flagged', true),
      senderOf('sender-last')
    ]

    expect(resolveVariationTestSender(variation, testWorkspace(senders))).toEqual({
      status: 'ready',
      integrationId: 'int-marketing',
      sender: senderOf('sender-flagged', true),
      source: 'default'
    })
  })

  // The server leaves out the email of a template that has none
  it('is the default sender when the template has no email content', () => {
    const variation = variationOf({ id: 'tmpl-1', category: 'marketing' })

    expect(resolveVariationTestSender(variation, testWorkspace(marketingSenders))).toEqual({
      status: 'ready',
      integrationId: 'int-marketing',
      sender: senderOf('sender-default', true),
      source: 'default'
    })
  })

  // An empty list of senders is sent as null by the server, or left out
  it.each([
    { description: 'null', senders: null },
    { description: 'left out', senders: undefined }
  ])('reports no sender when the senders of the marketing provider are $description', ({ senders }) => {
    const variation = variationOf(templateOf('marketing', 'sender-shared'))
    const workspace = workspaceWith({
      integrations: [
        { ...emailIntegration('int-marketing', []), email_provider: { kind: 'smtp', senders } }
      ]
    })

    expect(resolveVariationTestSender(variation, workspace)).toEqual({ status: 'no-sender' })
  })

  it('reports an override the marketing provider lacks, though the transactional provider has it', () => {
    const variation = variationOf(templateOf('marketing', 'sender-shared'), 'sender-transactional-default')

    expect(resolveVariationTestSender(variation, testWorkspace(marketingSenders))).toEqual({
      status: 'override-missing',
      senderId: 'sender-transactional-default'
    })
  })

  it('reports no sender when the marketing provider has none', () => {
    const variation = variationOf(templateOf('marketing', 'sender-shared'))

    expect(resolveVariationTestSender(variation, testWorkspace([]))).toEqual({ status: 'no-sender' })
  })

  // Whatever the variation names, a test has nowhere to go without the marketing provider
  const noProviderWorkspaces: [string, Workspace | undefined][] = [
    ...incompleteWorkspaces,
    ['is not known', undefined]
  ]
  it.each(noProviderWorkspaces)('reports no provider for a workspace that %s', (_, workspace) => {
    const variation = variationOf(templateOf('marketing', 'sender-shared'), 'sender-support')

    expect(resolveVariationTestSender(variation, workspace)).toEqual({ status: 'no-provider' })
  })
})
