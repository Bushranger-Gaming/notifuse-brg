import React from 'react'
import { Handle, Position, useConnection, type NodeProps } from '@xyflow/react'
import { Bell } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import { BaseNode } from './BaseNode'
import { nodeTypeColors, getNodeDescription } from './constants'
import { useAutomation } from '../context'
import { useNovuWorkflows } from '../hooks/useNovuWorkflows'
import type { AutomationFlowNode, Structural } from '../utils/flowConverter'
import type { NovuMessageNodeConfig } from '../../../services/api/automation'

type NovuMessageNodeProps = NodeProps<AutomationFlowNode>

export const NovuMessageNode: React.FC<NovuMessageNodeProps> = ({ data, selected }) => {
  const { t } = useLingui()
  const { workspace } = useAutomation()
  const { workflows } = useNovuWorkflows(workspace.id)
  const config = data.config as Structural<NovuMessageNodeConfig>
  const workflowId = config?.workflow_id
  // The workflow name once the list of the bridge is in; its id until then, or when the bridge
  // does not list it or cannot be reached.
  const workflowName = workflowId
    ? (workflows.find((workflow) => workflow.id === workflowId)?.name ?? workflowId)
    : undefined
  const connection = useConnection()
  const isConnecting = connection.inProgress
  const targetHandleSize = isConnecting ? 16 : 10
  const targetHandleColor = isConnecting ? '#22c55e' : data.isOrphan ? '#f97316' : '#3b82f6'
  const sourceHandleColor = data.isOrphan ? '#f97316' : '#3b82f6'

  return (
    <>
      <Handle
        type="target"
        position={Position.Top}
        style={{
          background: targetHandleColor,
          width: targetHandleSize,
          height: targetHandleSize,
          transition: 'all 0.15s ease'
        }}
      />
      <BaseNode
        type="novu_message"
        label={t`In-app / push message`}
        description={getNodeDescription(data.config)}
        icon={<Bell size={16} color={selected ? undefined : nodeTypeColors.novu_message} />}
        selected={selected}
        isOrphan={data.isOrphan}
        onDelete={data.onDelete}
      >
        {workflowName ? (
          <div className="truncate max-w-[200px]" title={workflowId}>
            {workflowName}
          </div>
        ) : (
          <div className="text-orange-500">{t`Select`}</div>
        )}
      </BaseNode>
      <Handle
        type="source"
        position={Position.Bottom}
        style={{ background: sourceHandleColor, width: 10, height: 10 }}
      />
    </>
  )
}
