import type { BroadcastVariation } from '../../services/api/broadcast'
import type { Integration, Sender, Workspace } from '../../services/api/types'

// The sender a variation sends from: the one it overrides its template's with, else the
// template's own, both resolved on the marketing provider, which every broadcast goes through
// whatever the category of its template. An override the provider no longer has is shown by its
// id, because the send fails on it rather than falling back. Undefined when neither names a
// sender the workspace knows.
export const getVariationSender = (
  variation: BroadcastVariation,
  workspace: Workspace | undefined
): string | undefined => {
  const marketingProviderId = workspace?.settings.marketing_email_provider_id
  const senders = workspace?.integrations?.find((i: Integration) => i.id === marketingProviderId)
    ?.email_provider?.senders
  const format = (sender: Sender) => `${sender.name} <${sender.email}>`

  if (variation.sender_id) {
    const override = senders?.find((s: Sender) => s.id === variation.sender_id)
    return override ? format(override) : variation.sender_id
  }

  const templateSender = senders?.find((s: Sender) => s.id === variation.template?.email?.sender_id)
  return templateSender ? format(templateSender) : undefined
}

// What a test email of a variation is sent with: `ready` names the marketing provider and the sender
// the broadcast would send the variation from, and `source` says where that sender comes from.
export type VariationTestSender =
  | {
      status: 'ready'
      integrationId: string
      sender: Sender
      source: 'variation' | 'template' | 'default'
    }
  // The workspace has no marketing provider, which every broadcast goes through
  | { status: 'no-provider' }
  // The marketing provider has no sender at all
  | { status: 'no-sender' }
  // Neither the variation nor its template names a sender the provider has, and the provider flags
  // none as default, which is the only sender the send falls back to
  | { status: 'no-default' }
  // The sender the variation overrides its template's with is no longer on the marketing provider
  | { status: 'override-missing'; senderId: string }

// Resolves the sender of a test email the way the broadcast does, on the marketing provider: the
// override of the variation, else the sender of its template, else the provider's default (the one
// flagged as default: a provider that flags none has no default, and the send fails rather than
// taking its first sender). The test endpoint falls back to the default sender for an id it does not
// know, so an override the provider no longer has is reported here, where the broadcast would pause,
// rather than sent from another sender. A template sender the provider does not have falls back to
// the default, as it does for the broadcast.
export const resolveVariationTestSender = (
  variation: BroadcastVariation,
  workspace: Workspace | undefined
): VariationTestSender => {
  const marketingProviderId = workspace?.settings.marketing_email_provider_id
  const integration = workspace?.integrations?.find((i: Integration) => i.id === marketingProviderId)
  if (!integration?.email_provider) return { status: 'no-provider' }

  const integrationId = integration.id
  const senders = integration.email_provider.senders ?? []

  if (variation.sender_id) {
    const override = senders.find((s: Sender) => s.id === variation.sender_id)
    return override
      ? { status: 'ready', integrationId, sender: override, source: 'variation' }
      : { status: 'override-missing', senderId: variation.sender_id }
  }

  const templateSenderId = variation.template?.email?.sender_id
  const templateSender = templateSenderId
    ? senders.find((s: Sender) => s.id === templateSenderId)
    : undefined
  if (templateSender) {
    return { status: 'ready', integrationId, sender: templateSender, source: 'template' }
  }

  if (senders.length === 0) return { status: 'no-sender' }

  // Only a sender flagged as default is one the send falls back to
  const defaultSender = senders.find((s: Sender) => s.is_default)
  return defaultSender
    ? { status: 'ready', integrationId, sender: defaultSender, source: 'default' }
    : { status: 'no-default' }
}
