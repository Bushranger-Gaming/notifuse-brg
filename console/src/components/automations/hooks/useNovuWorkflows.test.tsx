import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useNovuWorkflows } from './useNovuWorkflows'
import { automationApi, type NovuWorkflow } from '../../../services/api/automation'
import { ApiError } from '../../../services/api/errors'

const workflows: NovuWorkflow[] = [
  { id: 'crm-message', name: 'CRM · Message', uses_text: true },
  { id: 'crm-welcome-inapp', name: 'CRM · Welcome', uses_text: false }
]

// The client keeps its own retry setting off in the hook; the one here only makes a leaked default
// show up as extra calls instead of a slow test.
const makeWrapper = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retryDelay: 1 } } })
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
  return wrapper
}

describe('useNovuWorkflows', () => {
  let listNovuWorkflows: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    listNovuWorkflows = vi.spyOn(automationApi, 'listNovuWorkflows')
  })
  afterEach(() => {
    listNovuWorkflows.mockRestore()
  })

  it('answers the workflows of the workspace', async () => {
    listNovuWorkflows.mockResolvedValue({ workflows })

    const { result } = renderHook(() => useNovuWorkflows('ws-1'), { wrapper: makeWrapper() })

    expect(result.current.isLoading).toBe(true)
    expect(result.current.workflows).toEqual([])
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.workflows).toEqual(workflows)
    expect(result.current.error).toBeNull()
    expect(listNovuWorkflows).toHaveBeenCalledWith('ws-1')
  })

  it('carries the message of the server when the bridge cannot answer, and asks once', async () => {
    listNovuWorkflows.mockRejectedValue(
      new ApiError('The Novu bridge is not set up on this server: BRG_NOVU_BRIDGE_URL and BRG_NOVU_BRIDGE_SECRET are required', 503)
    )

    const { result } = renderHook(() => useNovuWorkflows('ws-1'), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.error).not.toBeNull())
    expect(result.current.error?.message).toContain('BRG_NOVU_BRIDGE_URL')
    expect(result.current.workflows).toEqual([])
    expect(result.current.isLoading).toBe(false)
    // Retried, it would answer the same: the form lets the author type the id meanwhile.
    expect(listNovuWorkflows).toHaveBeenCalledTimes(1)
  })

  it('asks nothing without a workspace', () => {
    renderHook(() => useNovuWorkflows(''), { wrapper: makeWrapper() })

    expect(listNovuWorkflows).not.toHaveBeenCalled()
  })

  it('is one request for every node and form of a workspace', async () => {
    listNovuWorkflows.mockResolvedValue({ workflows })
    const wrapper = makeWrapper()

    const first = renderHook(() => useNovuWorkflows('ws-1'), { wrapper })
    const second = renderHook(() => useNovuWorkflows('ws-1'), { wrapper })

    await waitFor(() => expect(first.result.current.workflows).toHaveLength(2))
    await waitFor(() => expect(second.result.current.workflows).toHaveLength(2))
    expect(listNovuWorkflows).toHaveBeenCalledTimes(1)
  })
})
