import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { NovuMessageConfigForm } from './NovuMessageConfigForm'
import type { NovuMessageNodeConfig, NovuWorkflow } from '../../../services/api/automation'

// antd's Select mounts a ResizeObserver; jsdom doesn't provide one.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

// The list of workflows comes from the CRM bridge through the server; the form is tested against
// what the hook answers, in each of its states.
interface WorkflowsState {
  workflows: NovuWorkflow[]
  isLoading: boolean
  error: Error | null
}
let workflowsState: WorkflowsState
const useNovuWorkflowsMock = vi.fn((workspaceId: string) => {
  void workspaceId
  return workflowsState
})
vi.mock('../hooks/useNovuWorkflows', () => ({
  useNovuWorkflows: (workspaceId: string) => useNovuWorkflowsMock(workspaceId)
}))

const CRM_MESSAGE: NovuWorkflow = { id: 'crm-message', name: 'CRM · Message', uses_text: true }
const CRM_WELCOME: NovuWorkflow = { id: 'crm-welcome-inapp', name: 'CRM · Welcome', uses_text: false }

const HELP = "Title and body are shown by workflows that use the flow's text, such as CRM message"

beforeEach(() => {
  workflowsState = { workflows: [CRM_MESSAGE, CRM_WELCOME], isLoading: false, error: null }
  useNovuWorkflowsMock.mockClear()
})

const renderForm = (config: Partial<NovuMessageNodeConfig>, onChange = vi.fn()) => {
  const view = render(
    <I18nProvider i18n={i18n}>
      <NovuMessageConfigForm
        config={{ workflow_id: '', ...config }}
        onChange={onChange}
        workspaceId="ws-1"
      />
    </I18nProvider>
  )
  return { onChange, ...view }
}

// antd marks a required field on its label.
const isRequired = (label: string) =>
  screen.getByText(label, { selector: 'label' }).classList.contains('ant-form-item-required')

// The options of the open dropdown, as the name and the id beneath it.
const optionTexts = () =>
  Array.from(document.querySelectorAll('.ant-select-item-option')).map((option) => option.textContent)

const selectedText = (container: HTMLElement) =>
  container.querySelector('.ant-select-content')?.textContent ?? ''

describe('NovuMessageConfigForm', () => {
  it('asks for the workflows of the workspace it is editing', () => {
    renderForm({})

    expect(useNovuWorkflowsMock).toHaveBeenCalledWith('ws-1')
  })

  describe('with the list of workflows', () => {
    it('shows the name of the chosen workflow, not its id', () => {
      const { container } = renderForm({ workflow_id: 'crm-message' })

      expect(selectedText(container)).toBe('CRM · Message')
      expect(screen.queryByText('crm-message')).not.toBeInTheDocument()
    })

    it('lists every workflow by name, with its id beneath', async () => {
      renderForm({})

      fireEvent.mouseDown(screen.getByRole('combobox'))

      await waitFor(() => expect(optionTexts()).toHaveLength(2))
      expect(optionTexts()).toEqual(['CRM · Messagecrm-message', 'CRM · Welcomecrm-welcome-inapp'])
    })

    it('stores the id of the workflow picked, keeping the rest of the config', async () => {
      const { onChange } = renderForm({ workflow_id: 'crm-message', title: 'Hi', body: 'There' })

      fireEvent.mouseDown(screen.getByRole('combobox'))
      await waitFor(() => expect(optionTexts()).toHaveLength(2))
      fireEvent.click(screen.getByTitle('CRM · Welcome'))

      expect(onChange).toHaveBeenCalledTimes(1)
      expect(onChange.mock.calls[0][0]).toEqual({
        workflow_id: 'crm-welcome-inapp',
        title: 'Hi',
        body: 'There'
      })
    })

    it('shows a placeholder when no workflow is chosen yet', () => {
      renderForm({})

      expect(screen.getByText('Select a workflow...')).toBeInTheDocument()
    })

    it('says so when the bridge lists no active workflow', async () => {
      workflowsState = { workflows: [], isLoading: false, error: null }
      renderForm({})

      fireEvent.mouseDown(screen.getByRole('combobox'))

      expect(await screen.findByText('No active workflows')).toBeInTheDocument()
    })

    it('keeps a workflow the bridge no longer lists, by its id, with a warning', () => {
      const { container } = renderForm({ workflow_id: 'retired-flow' })

      expect(selectedText(container)).toBe('retired-flow')
      expect(screen.getByText("This workflow is not among the bridge's active workflows")).toBeInTheDocument()
    })

    it('has no warning for a workflow the bridge lists', () => {
      renderForm({ workflow_id: 'crm-message' })

      expect(screen.queryByText(/not among the bridge/)).not.toBeInTheDocument()
    })
  })

  describe('while the list loads', () => {
    beforeEach(() => {
      workflowsState = { workflows: [], isLoading: true, error: null }
    })

    it('shows the select loading, and does not call a saved workflow unlisted', () => {
      const { container } = renderForm({ workflow_id: 'crm-message' })

      expect(container.querySelector('.ant-select-loading')).not.toBeNull()
      expect(screen.queryByText(/not among the bridge/)).not.toBeInTheDocument()
      expect(selectedText(container)).toBe('crm-message')
    })

    it('says it is loading when the select is opened', async () => {
      renderForm({})

      fireEvent.mouseDown(screen.getByRole('combobox'))

      expect(await screen.findByText('Loading workflows...')).toBeInTheDocument()
    })
  })

  describe('when the list fails', () => {
    beforeEach(() => {
      workflowsState = {
        workflows: [],
        isLoading: false,
        error: new Error('The Novu bridge is not set up on this server: BRG_NOVU_BRIDGE_URL and BRG_NOVU_BRIDGE_SECRET are required')
      }
    })

    it('says why, in the words of the server', () => {
      renderForm({})

      expect(screen.getByText('The workflow list is unavailable')).toBeInTheDocument()
      expect(screen.getByText(/BRG_NOVU_BRIDGE_URL and BRG_NOVU_BRIDGE_SECRET are required/)).toBeInTheDocument()
    })

    it('still lets the author type a workflow id', () => {
      const { onChange } = renderForm({ title: 'Hi' })

      fireEvent.change(screen.getByLabelText('Workflow id'), { target: { value: 'crm-message' } })

      expect(onChange).toHaveBeenCalledWith({ workflow_id: 'crm-message', title: 'Hi' })
      expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    })

    it('shows the id already saved on the node', () => {
      renderForm({ workflow_id: 'crm-welcome-inapp' })

      expect(screen.getByLabelText('Workflow id')).toHaveValue('crm-welcome-inapp')
      expect(screen.queryByText(/not among the bridge/)).not.toBeInTheDocument()
    })

    it('does not trim away the id as it is typed other than around it', () => {
      const { onChange } = renderForm({})

      fireEvent.change(screen.getByLabelText('Workflow id'), { target: { value: '  crm-message ' } })

      expect(onChange.mock.calls[0][0].workflow_id).toBe('crm-message')
    })

    it('demands no text, since nothing is known of the workflow', () => {
      renderForm({ workflow_id: 'crm-message' })

      expect(isRequired('Title')).toBe(false)
      expect(isRequired('Body')).toBe(false)
    })
  })

  describe('title and body', () => {
    it('are always shown, each with the help text', () => {
      renderForm({ workflow_id: 'crm-welcome-inapp' })

      expect(screen.getByLabelText('Title')).toBeInTheDocument()
      expect(screen.getByLabelText('Body')).toBeInTheDocument()
      expect(screen.getAllByText(HELP)).toHaveLength(1)
    })

    it('are shown before any workflow is chosen', () => {
      renderForm({})

      expect(screen.getByLabelText('Title')).toBeInTheDocument()
      expect(screen.getByLabelText('Body')).toBeInTheDocument()
    })

    it('are required when the chosen workflow uses the flow text', () => {
      renderForm({ workflow_id: 'crm-message' })

      expect(isRequired('Title')).toBe(true)
      expect(isRequired('Body')).toBe(true)
    })

    it('are optional when the chosen workflow does not', () => {
      renderForm({ workflow_id: 'crm-welcome-inapp' })

      expect(isRequired('Title')).toBe(false)
      expect(isRequired('Body')).toBe(false)
    })

    it('are optional while no workflow is chosen, and for one the bridge does not list', () => {
      const { unmount } = renderForm({})
      expect(isRequired('Title')).toBe(false)
      unmount()

      renderForm({ workflow_id: 'retired-flow' })
      expect(isRequired('Title')).toBe(false)
      expect(isRequired('Body')).toBe(false)
    })

    it('follow the workflow picked', async () => {
      const { rerender } = renderForm({ workflow_id: 'crm-welcome-inapp' })
      expect(isRequired('Title')).toBe(false)

      rerender(
        <I18nProvider i18n={i18n}>
          <NovuMessageConfigForm config={{ workflow_id: 'crm-message' }} onChange={vi.fn()} workspaceId="ws-1" />
        </I18nProvider>
      )

      expect(isRequired('Title')).toBe(true)
    })

    it('show what is stored on the node', () => {
      renderForm({ workflow_id: 'crm-message', title: 'Welcome', body: 'Your bonus is ready' })

      expect(screen.getByLabelText('Title')).toHaveValue('Welcome')
      expect(screen.getByLabelText('Body')).toHaveValue('Your bonus is ready')
    })

    it('write the title and the body to the config', () => {
      const { onChange } = renderForm({ workflow_id: 'crm-message' })

      fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Welcome' } })
      expect(onChange).toHaveBeenLastCalledWith({ workflow_id: 'crm-message', title: 'Welcome' })

      fireEvent.change(screen.getByLabelText('Body'), { target: { value: 'Your bonus is ready' } })
      expect(onChange).toHaveBeenLastCalledWith({ workflow_id: 'crm-message', body: 'Your bonus is ready' })
    })

    it('drop the key rather than store a blank when cleared', () => {
      const { onChange } = renderForm({ workflow_id: 'crm-message', title: 'Welcome', body: 'Hi' })

      fireEvent.change(screen.getByLabelText('Title'), { target: { value: '' } })

      const [update] = onChange.mock.calls[0]
      expect(update.title).toBeUndefined()
      expect(JSON.parse(JSON.stringify(update))).toEqual({ workflow_id: 'crm-message', body: 'Hi' })
    })

    it('stop at the limits the bridge applies', () => {
      renderForm({ workflow_id: 'crm-message' })

      expect(screen.getByLabelText('Title')).toHaveAttribute('maxlength', '200')
      expect(screen.getByLabelText('Body')).toHaveAttribute('maxlength', '1000')
    })
  })
})
