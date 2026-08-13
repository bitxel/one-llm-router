import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api-client'

import { ApiKeyEditPanel } from './detail-edit'

vi.mock('@/lib/api-client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api-client')>('@/lib/api-client')
  return {
    ...actual,
    api: {
      ...actual.api,
      get: vi.fn(),
      post: vi.fn(),
    },
  }
})

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

const apiPostMock = vi.mocked(api.post)
const toastSuccessMock = vi.mocked(toast.success)
const toastErrorMock = vi.mocked(toast.error)

const account = {
  id: 7,
  name: 'my-account',
  base_url: 'https://api.openai.com',
  capabilities: ['op.openai.responses'],
}

function renderPanel() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ApiKeyEditPanel account={account} />
    </QueryClientProvider>,
  )
}

describe('ApiKeyEditPanel', () => {
  beforeEach(() => {
    apiPostMock.mockReset()
    toastSuccessMock.mockReset()
    toastErrorMock.mockReset()
  })

  it('prefills name, base_url and capabilities and saves a patch', async () => {
    apiPostMock.mockResolvedValue({})
    const user = userEvent.setup()
    renderPanel()

    expect(screen.getByTestId('edit-name-input')).toHaveValue('my-account')
    expect(screen.getByTestId('edit-base-url-input')).toHaveValue('https://api.openai.com')
    expect(screen.getByTestId('edit-capability-op.openai.responses')).toBeChecked()

    const nameInput = screen.getByTestId('edit-name-input')
    await user.clear(nameInput)
    await user.type(nameInput, '重命名账户')

    await user.click(screen.getByTestId('edit-account-submit'))

    await waitFor(() => {
      expect(apiPostMock).toHaveBeenCalledWith('/api/admin/accounts/7/update', {
        name: '重命名账户',
        api_key: undefined,
        base_url: 'https://api.openai.com',
        capabilities: ['op.openai.responses'],
      })
    })
    expect(toastSuccessMock).toHaveBeenCalledWith('Account updated')
  })

  it('omits api_key when left blank and posts the chosen capabilities', async () => {
    apiPostMock.mockResolvedValue({})
    const user = userEvent.setup()
    renderPanel()

    await user.click(screen.getByTestId('edit-capability-op.openai.chat_completions'))
    await user.click(screen.getByTestId('edit-account-submit'))

    await waitFor(() => {
      expect(apiPostMock).toHaveBeenCalledWith('/api/admin/accounts/7/update', {
        name: 'my-account',
        api_key: undefined,
        base_url: 'https://api.openai.com',
        capabilities: ['op.openai.responses', 'op.openai.chat_completions'],
      })
    })
  })

  it('surfaces client-side blank-name validation and keeps the payload client-valid only', async () => {
    apiPostMock.mockResolvedValue({})
    const user = userEvent.setup()
    renderPanel()

    const nameInput = screen.getByTestId('edit-name-input')
    await user.clear(nameInput)
    await user.type(nameInput, '   ')
    await user.click(screen.getByTestId('edit-account-submit'))

    expect(screen.getByText('name is required')).toBeInTheDocument()
    expect(apiPostMock).not.toHaveBeenCalled()
  })

  it('allows multi-byte names client-side (backend owns control-character rejection)', async () => {
    apiPostMock.mockResolvedValue({})
    const user = userEvent.setup()
    renderPanel()

    const nameInput = screen.getByTestId('edit-name-input')
    await user.clear(nameInput)
    await user.type(nameInput, '中文名称')
    await user.click(screen.getByTestId('edit-account-submit'))

    await waitFor(() => {
      expect(apiPostMock).toHaveBeenCalledWith('/api/admin/accounts/7/update', {
        name: '中文名称',
        api_key: undefined,
        base_url: 'https://api.openai.com',
        capabilities: ['op.openai.responses'],
      })
    })
  })

  it('shows a toast when the update fails', async () => {
    apiPostMock.mockRejectedValue(new Error('boom'))
    const user = userEvent.setup()
    renderPanel()

    await user.click(screen.getByTestId('edit-account-submit'))

    await waitFor(() => {
      expect(toastErrorMock).toHaveBeenCalledWith('Failed to update account')
    })
  })
})
