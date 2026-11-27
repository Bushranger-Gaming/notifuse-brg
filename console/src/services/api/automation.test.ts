import { describe, it, expect, vi, beforeEach } from 'vitest'
import { api } from './client'
import { automationApi } from './automation'

// The service module imports the api client, which pulls in the router.
vi.mock('./client', () => ({
  api: { post: vi.fn(), get: vi.fn() }
}))

beforeEach(() => {
  vi.mocked(api.get).mockReset()
})

describe('automationApi.listNovuWorkflows', () => {
  it('asks for the workflows of the workspace and answers the server list', async () => {
    const answer = { workflows: [{ id: 'crm-message', name: 'CRM · Message', uses_text: true }] }
    vi.mocked(api.get).mockResolvedValue(answer)

    const result = await automationApi.listNovuWorkflows('ws 1')

    expect(vi.mocked(api.get)).toHaveBeenCalledTimes(1)
    expect(vi.mocked(api.get)).toHaveBeenCalledWith('/api/automations.novuWorkflows?workspace_id=ws+1')
    expect(result).toEqual(answer)
  })

  it('lets the error of the server through', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('The Novu bridge is not set up on this server'))

    await expect(automationApi.listNovuWorkflows('ws-1')).rejects.toThrow('not set up')
  })
})
