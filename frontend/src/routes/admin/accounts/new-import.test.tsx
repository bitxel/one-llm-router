import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
  useParams,
} from '@tanstack/react-router'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { accountsImportAuthJson } from '@/generated/openapi'
import { callAdmin } from '@/lib/router-api'

import { AdminAccountsNewImport } from './new-import'

vi.mock('@/generated/openapi', async () => {
  const actual = await vi.importActual<typeof import('@/generated/openapi')>('@/generated/openapi')
  return {
    ...actual,
    accountsImportAuthJson: vi.fn(),
  }
})

vi.mock('@/lib/router-api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/router-api')>('@/lib/router-api')
  return {
    ...actual,
    callAdmin: vi.fn(),
  }
})

const accountsImportAuthJsonMock = vi.mocked(accountsImportAuthJson)
const callAdminMock = vi.mocked(callAdmin)

async function renderImport() {
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const adminRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin',
    component: () => <Outlet />,
  })
  const importRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/new-import',
    component: AdminAccountsNewImport,
  })
  const detailRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/$accountId',
    component: () => {
      const params = useParams({ strict: false })
      return <div data-testid="detail-route-stub">{params.accountId}</div>
    },
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([adminRoute.addChildren([importRoute, detailRoute])]),
    history: createMemoryHistory({ initialEntries: ['/admin/accounts/new-import'] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })

  await act(async () => {
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )
    await router.load()
  })
}

describe('AdminAccountsNewImport', () => {
  beforeEach(() => {
    accountsImportAuthJsonMock.mockReset()
    callAdminMock.mockReset()
  })

  it('uploads auth.json and redirects to the imported account detail route', async () => {
    accountsImportAuthJsonMock.mockReturnValue(Promise.resolve({} as never))
    callAdminMock.mockResolvedValue({
      account: {
        id: 42,
        name: 'imported-account',
        provider: 'openai',
        auth_method: 'oauth_import',
        status: 'active',
        email: 'import@example.com',
        plan_type: 'chatgpt-plus',
        plan_type_label: 'ChatGPT Plus',
        chatgpt_account_id: 'acct_import_42',
      },
    } as never)

    await renderImport()
    const user = userEvent.setup()
    const file = new File(['  {"OPENAI_API_KEY":null}  '], 'auth.json', {
      type: 'application/json',
    })

    fireEvent.change(screen.getByTestId('import-auth-json-input'), {
      target: { files: [file] },
    })
    await user.click(screen.getByTestId('import-auth-json-submit'))

    await waitFor(() => {
      expect(accountsImportAuthJsonMock).toHaveBeenCalledWith({
        body: { auth_json: '{"OPENAI_API_KEY":null}' },
        headers: { 'Content-Type': 'application/json' },
      })
    })
    expect(await screen.findByTestId('detail-route-stub')).toHaveTextContent('42')
  })

  it('imports pasted auth.json text as application/json and redirects', async () => {
    accountsImportAuthJsonMock.mockReturnValue(Promise.resolve({} as never))
    callAdminMock.mockResolvedValue({
      account: {
        id: 43,
        name: 'pasted-account',
        provider: 'openai',
        auth_method: 'oauth_import',
        status: 'active',
      },
    } as never)

    await renderImport()
    const user = userEvent.setup()

    await user.click(screen.getByTestId('import-source-paste'))
    fireEvent.change(screen.getByTestId('import-auth-json-textarea'), {
      target: {
        value: '  {"tokens":{"access_token":"acc","refresh_token":"ref","id_token":"id"}}  ',
      },
    })
    await user.click(screen.getByTestId('import-auth-json-submit'))

    expect(accountsImportAuthJsonMock).toHaveBeenCalledWith({
      body: {
        auth_json: '{"tokens":{"access_token":"acc","refresh_token":"ref","id_token":"id"}}',
      },
      headers: { 'Content-Type': 'application/json' },
    })
    expect(await screen.findByTestId('detail-route-stub')).toHaveTextContent('43')
  })

  it('requires a file before submitting', async () => {
    await renderImport()
    const user = userEvent.setup()

    await user.click(screen.getByTestId('import-auth-json-submit'))

    expect(await screen.findByTestId('error-banner')).toHaveTextContent(
      'Choose an auth.json file before importing.',
    )
  })

  it('requires pasted auth.json text before submitting in paste mode', async () => {
    await renderImport()
    const user = userEvent.setup()

    await user.click(screen.getByTestId('import-source-paste'))
    await user.click(screen.getByTestId('import-auth-json-submit'))

    expect(await screen.findByTestId('error-banner')).toHaveTextContent(
      'Paste auth.json content before importing.',
    )
  })
})
