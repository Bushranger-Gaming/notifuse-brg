import React from 'react'
import { Alert, Form, Input, Select } from 'antd'
import { useLingui } from '@lingui/react/macro'
import {
  NOVU_MESSAGE_BODY_MAX_LENGTH,
  NOVU_MESSAGE_TITLE_MAX_LENGTH,
  type NovuMessageNodeConfig
} from '../../../services/api/automation'
import { useNovuWorkflows } from '../hooks/useNovuWorkflows'

interface NovuMessageConfigFormProps {
  config: NovuMessageNodeConfig
  onChange: (config: NovuMessageNodeConfig) => void
  workspaceId: string
}

export const NovuMessageConfigForm: React.FC<NovuMessageConfigFormProps> = ({
  config,
  onChange,
  workspaceId
}) => {
  const { t } = useLingui()
  const { workflows, isLoading, error } = useNovuWorkflows(workspaceId)

  const workflowId = config.workflow_id || ''
  const selected = workflows.find((workflow) => workflow.id === workflowId)
  // Only a workflow the bridge says shows the flow's text makes the text required. While the list
  // is unavailable nothing is known, so nothing is demanded.
  const textRequired = selected?.uses_text === true
  // A workflow saved on the node that the bridge no longer lists (switched off in Novu, renamed)
  // stays visible and selectable by its id, with a note, rather than the node looking empty.
  const unlisted = !!workflowId && !isLoading && !error && !selected

  const handleWorkflowChange = (value: string) => {
    onChange({ ...config, workflow_id: value })
  }

  const handleTextChange = (field: 'title' | 'body', value: string) => {
    // undefined rather '': clearing a box drops the key instead of storing a blank
    onChange({ ...config, [field]: value || undefined })
  }

  const options = workflows.map((workflow) => ({
    value: workflow.id,
    label: workflow.name,
    // Searchable by name or id; antd filters on this field, not on the rendered label.
    searchText: `${workflow.name} ${workflow.id}`
  }))
  if (unlisted) {
    options.unshift({ value: workflowId, label: workflowId, searchText: workflowId })
  }

  return (
    <Form layout="vertical" className="nodrag">
      {/* htmlFor/id by hand: a Form.Item with no `name` generates no field id, so without these
          the label is not associated with its input for assistive tech. */}
      <Form.Item
        label={t`Workflow`}
        htmlFor="novu-workflow"
        required
        validateStatus={unlisted ? 'warning' : undefined}
        help={unlisted ? t`This workflow is not among the bridge's active workflows` : undefined}
        extra={t`The Novu workflow that sends the in-app message and web push`}
      >
        {error ? (
          <>
            <Alert
              type="warning"
              showIcon
              className="mb-2"
              title={t`The workflow list is unavailable`}
              description={error.message}
            />
            <Input
              id="novu-workflow"
              value={workflowId}
              onChange={(e) => handleWorkflowChange(e.target.value.trim())}
              placeholder="crm-message"
              maxLength={64}
              aria-label={t`Workflow id`}
            />
          </>
        ) : (
          <Select
            id="novu-workflow"
            showSearch
            optionFilterProp="searchText"
            loading={isLoading}
            placeholder={t`Select a workflow...`}
            value={workflowId || undefined}
            onChange={handleWorkflowChange}
            style={{ width: '100%' }}
            options={options}
            optionRender={(option) => (
              <div>
                <div>{option.data.label}</div>
                <div className="text-xs text-gray-500">{option.data.value}</div>
              </div>
            )}
            notFoundContent={isLoading ? t`Loading workflows...` : t`No active workflows`}
          />
        )}
      </Form.Item>

      <Form.Item
        label={t`Title`}
        htmlFor="novu-title"
        required={textRequired}
        extra={t`Shown by workflows that use the flow's text (e.g. CRM · Message)`}
      >
        <Input
          id="novu-title"
          value={config.title || ''}
          onChange={(e) => handleTextChange('title', e.target.value)}
          maxLength={NOVU_MESSAGE_TITLE_MAX_LENGTH}
          showCount
        />
      </Form.Item>

      <Form.Item
        label={t`Body`}
        htmlFor="novu-body"
        required={textRequired}
        extra={t`Shown by workflows that use the flow's text (e.g. CRM · Message)`}
      >
        <Input.TextArea
          id="novu-body"
          value={config.body || ''}
          onChange={(e) => handleTextChange('body', e.target.value)}
          maxLength={NOVU_MESSAGE_BODY_MAX_LENGTH}
          showCount
          rows={4}
        />
      </Form.Item>
    </Form>
  )
}
