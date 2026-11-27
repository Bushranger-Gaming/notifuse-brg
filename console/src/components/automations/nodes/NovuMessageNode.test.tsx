import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import type { NodeProps } from '@xyflow/react'
import { NovuMessageNode } from './NovuMessageNode'
import type { AutomationFlowNode, AutomationNodeData } from '../utils/flowConverter'
import type { NovuWorkflow } from '../../../services/api/automation'

// NovuMessageNode only needs Handle/Position/useConnection from @xyflow/react; render them as
// inert so the node can mount without a ReactFlow provider.
vi.mock('@xyflow/react', () => ({
  Handle: () => null,
  Position: { Top: 'top', Bottom: 'bottom' },
  useConnection: () => ({ inProgress: false })
}))

// The workspace the workflows are listed for comes from the automation context.
vi.mock('../context', () => ({
  useAutomation: () => ({ workspace: { id: 'ws-1' } })
}))

let workflows: NovuWorkflow[] = []
const useNovuWorkflowsMock = vi.fn((workspaceId: string) => {
  void workspaceId
  return { workflows, isLoading: false, error: null }
})
vi.mock('../hooks/useNovuWorkflows', () => ({
  useNovuWorkflows: (workspaceId: string) => useNovuWorkflowsMock(workspaceId)
}))

beforeEach(() => {
  workflows = [
    { id: 'crm-message', name: 'CRM · Message', uses_text: true },
    { id: 'crm-welcome-inapp', name: 'CRM · Welcome', uses_text: false }
  ]
  useNovuWorkflowsMock.mockClear()
})

const renderNode = (config: Record<string, unknown>) => {
  const data: AutomationNodeData = { nodeType: 'novu_message', config, label: 'In-app / push message' }
  const props = { data, selected: false } as NodeProps<AutomationFlowNode>
  return render(
    <I18nProvider i18n={i18n}>
      <NovuMessageNode {...props} />
    </I18nProvider>
  )
}

describe('NovuMessageNode', () => {
  it('is titled by the step', () => {
    renderNode({ workflow_id: 'crm-message' })

    expect(screen.getByText('In-app / push message')).toBeInTheDocument()
  })

  it('shows the name of the workflow, not its id', () => {
    renderNode({ workflow_id: 'crm-welcome-inapp' })

    expect(screen.getByText('CRM · Welcome')).toBeInTheDocument()
    expect(screen.queryByText('crm-welcome-inapp')).not.toBeInTheDocument()
  })

  it('lists the workflows of the workspace of the automation', () => {
    renderNode({ workflow_id: 'crm-message' })

    expect(useNovuWorkflowsMock).toHaveBeenCalledWith('ws-1')
  })

  it('shows the id of a workflow the bridge does not list, or while the list is unavailable', () => {
    workflows = []
    renderNode({ workflow_id: 'crm-message' })

    expect(screen.getByText('crm-message')).toBeInTheDocument()
    expect(screen.queryByText('Select')).not.toBeInTheDocument()
  })

  it('asks for a workflow when none is chosen', () => {
    renderNode({ workflow_id: '' })

    expect(screen.getByText('Select')).toBeInTheDocument()
  })

  it('shows the description the author wrote', () => {
    renderNode({ workflow_id: 'crm-message', description: 'Welcome push, day 1' })

    expect(screen.getByText('Welcome push, day 1')).toBeInTheDocument()
  })
})
