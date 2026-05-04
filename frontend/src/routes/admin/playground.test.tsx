import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { playgroundRun } from '@/generated/openapi'
import { api } from '@/lib/api-client'
import { callAdmin, RouterApiError } from '@/lib/router-api'

import { AdminPlayground } from './playground'
import { strings } from './playground.strings'

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

vi.mock('@/generated/openapi', async () => {
  const actual = await vi.importActual<typeof import('@/generated/openapi')>('@/generated/openapi')
  return {
    ...actual,
    playgroundRun: vi.fn(() => Promise.resolve({})),
  }
})

vi.mock('@/lib/router-api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/router-api')>('@/lib/router-api')
  return {
    ...actual,
    callAdmin: vi.fn(),
  }
})

vi.mock('@/lib/api-client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api-client')>('@/lib/api-client')
  return {
    ...actual,
    api: {
      ...actual.api,
      get: vi.fn(),
    },
  }
})

const apiGetMock = vi.mocked(api.get)
const playgroundRunMock = vi.mocked(playgroundRun)
const callAdminMock = vi.mocked(callAdmin)
const toastSuccessMock = vi.mocked(toast.success)
const toastErrorMock = vi.mocked(toast.error)

function renderPlayground() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <AdminPlayground />
    </QueryClientProvider>,
  )
}

function mockAccounts() {
  apiGetMock.mockResolvedValue({
    total: 2,
    accounts: [
      {
        id: 42,
        name: 'plus-account',
        provider: 'openai',
        auth_method: 'oauth_browser',
        status: 'active',
        email: 'plus@example.com',
        plan_type: 'chatgpt-plus',
        plan_type_label: 'ChatGPT Plus',
      },
      {
        id: 99,
        name: 'disabled-account',
        provider: 'openai',
        auth_method: 'api_key',
        status: 'disabled',
      },
    ],
  } as never)
}

const successResult = {
  run: {
    selection_mode: 'auto',
    endpoint: 'responses',
    outcome: 'success',
    latency_ms: 123,
  },
  account: {
    id: 42,
    name: 'plus-account',
    provider: 'openai',
    auth_method: 'oauth_browser',
    status: 'active',
  },
  upstream: {
    status_code: 200,
    response_mode: 'json',
  },
  output: {
    text: 'Hello from playground.',
    text_available: true,
    raw_response_available: false,
  },
  usage: {
    input: 4,
    output: 3,
  },
} as const

describe('AdminPlayground', () => {
  beforeEach(() => {
    apiGetMock.mockReset()
    playgroundRunMock.mockClear()
    callAdminMock.mockReset()
    toastSuccessMock.mockReset()
    toastErrorMock.mockReset()
    mockAccounts()
  })

  it('submits automatic mode with the default model and renders the result', async () => {
    callAdminMock.mockResolvedValue(successResult as never)
    const user = userEvent.setup()
    renderPlayground()

    expect(await screen.findByRole('heading', { name: 'Playground' })).toBeInTheDocument()
    expect(screen.queryByText('Release 004')).not.toBeInTheDocument()
    expect(screen.getByTestId('playground-workspace')).toHaveClass(
      'xl:grid-cols-[minmax(0,0.96fr)_minmax(0,1.04fr)]',
    )
    expect(screen.getByTestId('playground-session-key-optional')).toHaveAccessibleName(
      `${strings.labels.optionalField}: ${strings.labels.sessionKey}`,
    )
    expect(screen.getByTestId('playground-include-raw-optional')).toHaveAccessibleName(
      `${strings.labels.optionalField}: ${strings.labels.includeRaw}`,
    )
    expect(await screen.findByDisplayValue(strings.defaultModel)).toBeInTheDocument()
    expect(screen.queryByTestId('playground-active-accounts')).not.toBeInTheDocument()
    expect(screen.queryByText('Active account pool')).not.toBeInTheDocument()
    const automaticMode = screen.getByRole('button', { name: strings.modes.auto })
    expect(automaticMode).toHaveAttribute('aria-pressed', 'true')
    await user.type(screen.getByTestId('playground-textarea'), 'hello')
    await user.click(screen.getByTestId('playground-submit'))

    await waitFor(() => expect(playgroundRunMock).toHaveBeenCalledTimes(1))
    expect(playgroundRunMock).toHaveBeenCalledWith({
      body: {
        selection_mode: 'auto',
        endpoint: 'responses',
        model: strings.defaultModel,
        text: 'hello',
        max_output_tokens: 1024,
        include_raw_response: false,
      },
    })
    expect(await screen.findByText('Hello from playground.')).toBeInTheDocument()
    const result = screen.getByTestId('playground-result')
    expect(within(result).getByText(strings.labels.mode)).toBeInTheDocument()
    expect(within(result).getByText(strings.labels.endpoint)).toBeInTheDocument()
    expect(within(result).getByText(strings.endpoints.responses)).toBeInTheDocument()
    expect(within(result).getByText(strings.labels.authMethod)).toBeInTheDocument()
    expect(within(result).getByText(strings.authMethod.oauth_browser)).toBeInTheDocument()
    expect(toastSuccessMock).toHaveBeenCalledWith(strings.toasts.success)
  })

  it('shows account picker options with runnable account diagnostics', async () => {
    const user = userEvent.setup()
    renderPlayground()

    await waitFor(() =>
      expect(screen.getByRole('button', { name: strings.modes.account })).toBeEnabled(),
    )
    await user.click(screen.getByRole('button', { name: strings.modes.account }))
    const form = screen.getByTestId('playground-form')
    const labels = Array.from(form.querySelectorAll('label'))
    const modeLabel = labels.find((label) => label.textContent?.startsWith(strings.labels.mode))
    const accountLabel = labels.find((label) =>
      label.textContent?.startsWith(strings.labels.account),
    )
    const endpointLabel = labels.find((label) =>
      label.textContent?.startsWith(strings.labels.endpoint),
    )
    if (!modeLabel || !accountLabel || !endpointLabel) {
      throw new Error('Expected playground form labels to be present')
    }
    expect(
      Boolean(modeLabel.compareDocumentPosition(accountLabel) & Node.DOCUMENT_POSITION_FOLLOWING),
    ).toBe(true)
    expect(
      Boolean(
        accountLabel.compareDocumentPosition(endpointLabel) & Node.DOCUMENT_POSITION_FOLLOWING,
      ),
    ).toBe(true)
    await user.click(screen.getByTestId('playground-account-select'))

    expect(
      await screen.findByRole('option', {
        name: /#42.*plus-account.*openai.*active.*OAuth browser.*plus@example\.com.*ChatGPT Plus/i,
      }),
    ).toBeInTheDocument()
  })

  it('submits chat completions endpoint and switches integration guide examples', async () => {
    callAdminMock.mockResolvedValue({
      ...successResult,
      run: { ...successResult.run, endpoint: 'chat_completions' },
      output: {
        text: 'Hello from chat.',
        text_available: true,
        raw_response_available: false,
      },
    } as never)
    const user = userEvent.setup()
    renderPlayground()

    await user.click(screen.getByTestId('playground-endpoint-chat_completions'))
    await user.type(screen.getByTestId('playground-textarea'), 'hello')
    await user.click(screen.getByTestId('playground-submit'))

    await waitFor(() => expect(playgroundRunMock).toHaveBeenCalledTimes(1))
    expect(playgroundRunMock).toHaveBeenCalledWith({
      body: expect.objectContaining({
        selection_mode: 'auto',
        endpoint: 'chat_completions',
        model: strings.defaultModel,
        text: 'hello',
      }),
    })
    expect(await screen.findByText('Hello from chat.')).toBeInTheDocument()
    expect(screen.getAllByText(strings.endpoints.chat_completions).length).toBeGreaterThan(0)

    await user.click(screen.getByTestId('playground-integration-guide-open'))
    const guide = await screen.findByTestId('playground-integration-guide')
    const code = within(guide).getByTestId('playground-integration-guide-code')
    expect(code).toHaveTextContent(`curl -sS "${window.location.origin}/v1/chat/completions"`)
    expect(code).toHaveTextContent('"messages":[{"role":"user"')
    expect(guide).toHaveTextContent('/v1/chat/completions')
    expect(within(guide).getByTestId('playground-integration-guide-api-select')).toHaveTextContent(
      strings.endpoints.chat_completions,
    )
  })

  it('opens the simplified integration guide with current-origin examples and language tabs', async () => {
    const user = userEvent.setup()
    renderPlayground()

    await user.click(screen.getByTestId('playground-integration-guide-open'))

    const guide = await screen.findByTestId('playground-integration-guide')
    const code = within(guide).getByTestId('playground-integration-guide-code')
    expect(code).not.toHaveTextContent('ROUTER_BASE_URL')
    expect(code).not.toHaveTextContent('ROUTER_API_KEY')
    expect(code).not.toHaveTextContent('-N')
    expect(code).toHaveTextContent(`curl -sS "${window.location.origin}/v1/responses"`)
    expect(code).toHaveTextContent('-H "Authorization: Bearer "')
    expect(code).toHaveTextContent("-d '{")
    expect(code).toHaveTextContent('/v1/responses')
    expect(code).toHaveTextContent('"stream":false')
    expect(guide).not.toHaveTextContent('data plane')
    expect(guide).not.toHaveTextContent('Response mode')
    expect(guide).not.toHaveTextContent('SSE-capable')
    expect(within(guide).getByTestId('playground-integration-guide-api-select')).toHaveTextContent(
      strings.endpoints.responses,
    )
    const closeButton = within(guide).getByRole('button', { name: strings.actions.closeGuide })
    expect(closeButton).toHaveTextContent('')
    expect(closeButton).toHaveClass('border-0')
    expect(closeButton).toHaveClass('shadow-none')

    await user.click(within(guide).getByTestId('playground-integration-guide-api-select'))
    await user.click(
      await screen.findByRole('option', { name: strings.endpoints.chat_completions }),
    )
    expect(within(guide).getByTestId('playground-integration-guide-code')).toHaveTextContent(
      `curl -sS "${window.location.origin}/v1/chat/completions"`,
    )
    expect(within(guide).getByTestId('playground-integration-guide-code')).toHaveTextContent(
      '"messages":[{"role":"user"',
    )
    expect(guide).toHaveTextContent('/v1/chat/completions')

    await user.click(within(guide).getByRole('tab', { name: strings.guide.tabs.python }))
    expect(within(guide).getByTestId('playground-integration-guide-code')).toHaveTextContent(
      `router_base_url = "${window.location.origin}"`,
    )
    expect(within(guide).getByTestId('playground-integration-guide-code')).toHaveTextContent(
      'router_api_key = ""',
    )

    await user.click(within(guide).getByRole('tab', { name: strings.guide.tabs.javascript }))
    expect(within(guide).getByTestId('playground-integration-guide-code')).toHaveTextContent(
      `const routerBaseUrl = "${window.location.origin}";`,
    )
    expect(within(guide).getByTestId('playground-integration-guide-code')).toHaveTextContent(
      'const routerApiKey = "";',
    )
    expect(within(guide).getByTestId('playground-integration-guide-code')).toHaveTextContent(
      'credentials: "omit"',
    )

    await user.click(within(guide).getByRole('tab', { name: strings.guide.tabs.go }))
    expect(within(guide).getByTestId('playground-integration-guide-code')).toHaveTextContent(
      'package main',
    )
    expect(within(guide).getByTestId('playground-integration-guide-code')).toHaveTextContent(
      `http.NewRequest(http.MethodPost, "${window.location.origin}/v1/chat/completions"`,
    )
    expect(within(guide).getByTestId('playground-integration-guide-code')).toHaveTextContent(
      'req.Header.Set("Authorization", "Bearer ")',
    )

    await user.click(closeButton)
    expect(screen.queryByTestId('playground-integration-guide')).not.toBeInTheDocument()
  })

  it('renders no-text results without inventing output and shows raw JSON when available', async () => {
    callAdminMock.mockResolvedValue({
      ...successResult,
      run: { ...successResult.run, outcome: 'no_extractable_text' },
      output: {
        text: '',
        text_available: false,
        raw_response_available: true,
        raw_response: { id: 'resp_no_text' },
      },
    } as never)
    const user = userEvent.setup()
    renderPlayground()

    await user.type(screen.getByTestId('playground-textarea'), 'hello')
    await user.click(screen.getByTestId('playground-raw-switch'))
    await user.click(screen.getByTestId('playground-submit'))

    expect(await screen.findByText(strings.emptyOutput)).toBeInTheDocument()
    expect(screen.getByText(strings.labels.rawResponse)).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Beautify' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: 'Raw' })).toHaveAttribute('aria-selected', 'false')
    expect(
      screen.getByRole('button', { name: strings.actions.copyRawResponse }),
    ).toBeInTheDocument()
    expect(screen.getByText(/resp_no_text/)).toBeInTheDocument()
  })

  it('submits explicit account mode without falling back to automatic', async () => {
    callAdminMock.mockResolvedValue({
      ...successResult,
      run: { ...successResult.run, selection_mode: 'account' },
    } as never)
    const user = userEvent.setup()
    renderPlayground()

    await waitFor(() =>
      expect(screen.getByRole('button', { name: strings.modes.account })).toBeEnabled(),
    )
    await user.click(screen.getByRole('button', { name: strings.modes.account }))
    await user.type(screen.getByTestId('playground-textarea'), 'hello')
    await user.click(screen.getByTestId('playground-submit'))

    await waitFor(() => expect(playgroundRunMock).toHaveBeenCalledTimes(1))
    expect(playgroundRunMock).toHaveBeenCalledWith({
      body: expect.objectContaining({
        selection_mode: 'account',
        endpoint: 'responses',
        account_id: 42,
      }),
    })
  })

  it('keeps duplicate submit protection in the UI while a run is pending', async () => {
    callAdminMock.mockReturnValue(new Promise(() => {}) as never)
    const user = userEvent.setup()
    renderPlayground()

    await user.type(screen.getByTestId('playground-textarea'), 'hello')
    await user.click(screen.getByTestId('playground-submit'))

    await waitFor(() => expect(screen.getByTestId('playground-submit')).toBeDisabled())
    expect(playgroundRunMock).toHaveBeenCalledTimes(1)
  })

  it('keeps validation keyboard-friendly and announces terminal status', async () => {
    callAdminMock.mockResolvedValue(successResult as never)
    const user = userEvent.setup()
    renderPlayground()

    await user.click(screen.getByTestId('playground-submit'))
    expect(await screen.findByText(strings.validation.textRequired)).toBeInTheDocument()
    expect(screen.getByTestId('playground-textarea')).toHaveFocus()

    await user.type(screen.getByTestId('playground-textarea'), 'hello')
    await user.click(screen.getByTestId('playground-submit'))

    expect(await screen.findByRole('status')).toHaveTextContent('Hello from playground.')
  })

  it('renders a no-active empty state and prevents a local submit', async () => {
    apiGetMock.mockResolvedValue({ total: 0, accounts: [] } as never)
    const user = userEvent.setup()
    renderPlayground()

    expect(await screen.findByTestId('playground-no-active')).toHaveTextContent(
      strings.noActiveAccounts,
    )
    expect(screen.getByTestId('playground-new-account-link')).toHaveAttribute(
      'href',
      '/admin/accounts/new',
    )
    await user.type(screen.getByTestId('playground-textarea'), 'hello')
    expect(screen.getByTestId('playground-submit')).toBeDisabled()
    expect(playgroundRunMock).not.toHaveBeenCalled()
  })

  it('surfaces playground business errors with the existing error banner', async () => {
    callAdminMock.mockRejectedValue(
      new RouterApiError({
        code: 4002,
        msg: 'playground_no_active_account',
        data: {},
        requestId: 'req-playground',
        status: 200,
      }),
    )
    const user = userEvent.setup()
    renderPlayground()

    await user.type(screen.getByTestId('playground-textarea'), 'hello')
    await user.click(screen.getByTestId('playground-submit'))

    expect(await screen.findByTestId('error-banner')).toHaveTextContent(
      'playground_no_active_account',
    )
    expect(screen.getByTestId('error-banner')).toHaveTextContent('req-playground')
    expect(screen.getByTestId('playground-textarea')).toHaveValue('hello')
    expect(toastErrorMock).toHaveBeenCalledWith(strings.toasts.failed)
  })

  it('links known failed accounts and redacts token-like diagnostics', async () => {
    callAdminMock.mockRejectedValue(
      new RouterApiError({
        code: 4004,
        msg: 'playground_upstream_error',
        data: {
          account_id: 42,
          upstream_status: 401,
          provider_message: 'Bearer sk-secret-token',
        },
        requestId: 'req-upstream',
        status: 200,
      }),
    )
    const user = userEvent.setup()
    renderPlayground()

    await user.type(screen.getByTestId('playground-textarea'), 'hello')
    await user.click(screen.getByTestId('playground-submit'))

    expect(await screen.findByText('Open account #42')).toHaveAttribute(
      'href',
      '/admin/accounts/42',
    )
    expect(screen.getByText('[redacted]')).toBeInTheDocument()
    expect(screen.queryByText(/sk-secret-token/)).not.toBeInTheDocument()
  })
})
