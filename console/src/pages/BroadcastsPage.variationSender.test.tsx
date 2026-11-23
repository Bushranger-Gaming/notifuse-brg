import '../__tests__/resizeObserverMock'
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ConfigProvider } from 'antd'
import { VariationFrom } from './BroadcastsPage'
import { getVariationSender } from '../components/broadcasts/variationSender'
import type { BroadcastVariation } from '../services/api/broadcast'
import type { Workspace } from '../services/api/types'

const provider = (id: string, senders: { id: string; name: string; email: string }[]) => ({
  id,
  name: id,
  type: 'email',
  email_provider: { kind: 'smtp', senders }
})

// The marketing provider holds the sender a variation may override with; the transactional
// provider holds one the broadcast never goes through
const workspace = {
  id: 'ws-1',
  settings: {
    marketing_email_provider_id: 'int-marketing',
    transactional_email_provider_id: 'int-transactional'
  },
  integrations: [
    provider('int-marketing', [
      { id: 'sender-template', name: 'Template', email: 'template@example.com' },
      { id: 'sender-support', name: 'Support', email: 'support@example.com' }
    ]),
    provider('int-transactional', [
      { id: 'sender-billing', name: 'Billing', email: 'billing@example.com' }
    ])
  ]
} as unknown as Workspace

const variation = (senderId?: string, templateSenderId = 'sender-template'): BroadcastVariation =>
  ({
    variation_name: 'A',
    template_id: 'tmpl-1',
    sender_id: senderId,
    template: { id: 'tmpl-1', category: 'marketing', email: { sender_id: templateSenderId } }
  }) as unknown as BroadcastVariation

const renderFrom = (v: BroadcastVariation) =>
  render(
    <ConfigProvider>
      <VariationFrom variation={v} workspace={workspace} />
    </ConfigProvider>
  )

describe('variation row sender', () => {
  it('shows the template sender while the variation has no sender of its own', () => {
    renderFrom(variation())

    expect(screen.getByText('From:')).toBeInTheDocument()
    expect(screen.getByText(/Template <template@example.com>/)).toBeInTheDocument()
  })

  it('reads "From:", a space, then the sender, in one line', () => {
    const { container } = renderFrom(variation('sender-support'))

    expect(container).toHaveTextContent('From: Support <support@example.com>')
  })

  it('reads "From:", a space, then "Default sender" when no sender is known', () => {
    const { container } = renderFrom(variation(undefined, 'sender-unknown'))

    expect(container).toHaveTextContent('From: Default sender')
  })

  it('shows the sender the variation overrides with, as Name <email>, in place of the template sender', () => {
    renderFrom(variation('sender-support'))

    expect(screen.getByText(/Support <support@example.com>/)).toBeInTheDocument()
    expect(screen.queryByText(/template@example.com/)).not.toBeInTheDocument()
  })

  it('shows "Default sender" when neither the variation nor the template names a known sender', () => {
    renderFrom(variation(undefined, 'sender-unknown'))

    expect(screen.getByText(/Default sender/)).toBeInTheDocument()
  })

  it('shows the id of an override the marketing provider no longer has, not the template sender', () => {
    renderFrom(variation('sender-gone'))

    expect(screen.getByText(/sender-gone/)).toBeInTheDocument()
    expect(screen.queryByText(/template@example.com/)).not.toBeInTheDocument()
  })

  it('looks the override up on the marketing provider only', () => {
    // sender-billing belongs to the transactional provider, which the broadcast does not use
    renderFrom(variation('sender-billing'))

    expect(screen.getByText(/sender-billing/)).toBeInTheDocument()
    expect(screen.queryByText(/billing@example.com/)).not.toBeInTheDocument()
  })
})

describe('getVariationSender', () => {
  it('resolves the override on the marketing provider as Name <email>', () => {
    expect(getVariationSender(variation('sender-support'), workspace)).toBe(
      'Support <support@example.com>'
    )
  })

  it('falls back to the template sender when the variation has no override', () => {
    expect(getVariationSender(variation(), workspace)).toBe('Template <template@example.com>')
  })

  it('returns the override id when the marketing provider does not have it', () => {
    expect(getVariationSender(variation('sender-gone'), workspace)).toBe('sender-gone')
  })

  it('returns undefined when neither the variation nor the template names a known sender', () => {
    expect(getVariationSender(variation(undefined, 'sender-unknown'), workspace)).toBeUndefined()
  })

  it('returns undefined for a variation without an override when there is no workspace', () => {
    expect(getVariationSender(variation(), undefined)).toBeUndefined()
  })
})
