import type { QueryClient } from '@tanstack/react-query'

export const adminAccountsListQueryKey = ['admin', 'accounts', 'list'] as const

export const adminAccountDetailQueryKey = (accountId: string | number) =>
  ['admin', 'account', String(accountId)] as const

export async function invalidateAdminAccountQueries(
  queryClient: QueryClient,
  accountId?: string | number | null,
) {
  await queryClient.invalidateQueries({ queryKey: adminAccountsListQueryKey })
  if (accountId != null) {
    await queryClient.invalidateQueries({ queryKey: adminAccountDetailQueryKey(accountId) })
  }
}
