import { useEffect, useRef, useState } from 'react'
import { Modal, Button, Form, Input, Alert, App, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'
import { transactionalNotificationsApi } from '../../services/api/transactional_notifications'
import type { BroadcastVariation } from '../../services/api/broadcast'
import type { Workspace } from '../../services/api/types'
import { resolveVariationTestSender } from './variationSender'

const { Text } = Typography

interface SendVariationTestModalProps {
  workspaceId: string
  workspace: Workspace | undefined
  variation: BroadcastVariation
  onClose: () => void
}

// Sends the template of one variation to one address through the template test path, which renders
// the subject and leaves the broadcast's message history, and so its A/B results, alone. The test
// goes through the marketing provider, from the sender the broadcast would send the variation from,
// and is refused when that sender is gone (the test path would send from the default sender instead)
// or when there is none to send from, a provider that flags no default sender having no fallback.
// It is rendered only while it is open, so a row does not keep a mounted modal.
export function SendVariationTestModal({
  workspaceId,
  workspace,
  variation,
  onClose
}: SendVariationTestModalProps) {
  const { t } = useLingui()
  const { message } = App.useApp()
  const [form] = Form.useForm<{ email: string }>()
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string>()
  // Set from the moment a send starts to the moment it ends. Enter submits the form without going
  // through the loading button, and two submits can validate before the first one renders
  const inFlight = useRef(false)
  // A send can end after the dialog was closed, and opened again: the dialog is unmounted on close
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  const target = resolveVariationTestSender(variation, workspace)

  const handleFinish = async ({ email }: { email: string }) => {
    if (inFlight.current || target.status !== 'ready') return
    inFlight.current = true
    setSending(true)
    setError(undefined)

    // The dialog shows what went wrong while it is open; once it is gone the person still hears of it
    const fail = (reason: string) => {
      if (mounted.current) setError(reason)
      else message.error(reason)
    }

    try {
      const response = await transactionalNotificationsApi.testTemplate(
        workspaceId,
        variation.template_id,
        target.integrationId,
        target.sender.id,
        email
      )
      if (response.success) {
        message.success(t`Test email sent successfully`)
        // The dialog this send was started from is not the one that is open now, if any
        if (mounted.current) onClose()
      } else {
        fail(response.error || t`Something went wrong`)
      }
    } catch (err) {
      // The backend says what is wrong, for example that the template has no email content
      fail(err instanceof Error && err.message ? err.message : t`Something went wrong`)
    } finally {
      inFlight.current = false
      // React 18 drops this on a dialog that is gone, without a warning
      setSending(false)
    }
  }

  return (
    <Modal
      title={t`Send Test Email`}
      open
      onCancel={onClose}
      footer={[
        <Button key="cancel" onClick={onClose}>
          {t`Cancel`}
        </Button>,
        <Button
          key="send"
          type="primary"
          loading={sending}
          disabled={target.status !== 'ready'}
          onClick={() => form.submit()}
        >
          {t`Send Test Email`}
        </Button>
      ]}
    >
      <Form form={form} layout="vertical" onFinish={handleFinish}>
        {target.status === 'no-provider' && (
          <Alert
            className="mb-4"
            type="error"
            showIcon
            title={t`The workspace has no marketing email provider, so there is nothing to send the test through`}
          />
        )}
        {target.status === 'no-sender' && (
          <Alert
            className="mb-4"
            type="error"
            showIcon
            title={t`The marketing email provider has no sender to send the test from`}
          />
        )}
        {target.status === 'no-default' && (
          <Alert
            className="mb-4"
            type="error"
            showIcon
            title={t`The marketing email provider has no default sender; choose a sender on the variation or its template`}
          />
        )}
        {target.status === 'override-missing' && (
          <Alert
            className="mb-4"
            type="error"
            showIcon
            title={t`This variation's sender is no longer on the marketing email provider; the broadcast would pause`}
          />
        )}
        {target.status === 'ready' && (
          <Form.Item label={t`From`}>
            <Text>{`${target.sender.name} <${target.sender.email}>`}</Text>
            {target.source === 'default' && (
              <Text type="secondary" className="block text-xs mt-1">
                {t`This variation names no sender on the marketing email provider, so the test is sent from the default sender`}
              </Text>
            )}
          </Form.Item>
        )}
        <Form.Item
          name="email"
          label={t`Recipient Email`}
          rules={[
            { required: true, message: t`Email is required` },
            { type: 'email', message: t`Please enter a valid email` }
          ]}
        >
          <Input placeholder={t`recipient@example.com`} autoFocus disabled={sending} />
        </Form.Item>
        {error && <Alert type="error" showIcon title={error} />}
      </Form>
    </Modal>
  )
}
