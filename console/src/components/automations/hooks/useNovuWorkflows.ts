import { useQuery } from '@tanstack/react-query'
import { automationApi, type NovuWorkflow } from '../../../services/api/automation'

export interface UseNovuWorkflowsReturn {
  workflows: NovuWorkflow[]
  isLoading: boolean
  // The reason the list is unavailable: the bridge is not set up on the server, cannot be
  // reached, or the request failed. The server's own message, which says which.
  error: Error | null
}

// The Novu workflows of the CRM bridge, shared by the node's form and its card on the canvas: one
// query key, so one request for both. Not retried: a bridge that is not set up answers the same
// way every time, and the form lets the author type a workflow id meanwhile.
export function useNovuWorkflows(workspaceId: string): UseNovuWorkflowsReturn {
  const { data, isLoading, error } = useQuery({
    queryKey: ['novu-workflows', workspaceId],
    queryFn: () => automationApi.listNovuWorkflows(workspaceId),
    enabled: !!workspaceId,
    retry: false,
    staleTime: 60 * 1000
  })

  return {
    workflows: data?.workflows ?? [],
    isLoading,
    error: error ?? null
  }
}
