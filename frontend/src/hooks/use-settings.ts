import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import {
  type DbSettings,
  type PluginIntent,
  type PluginSummary,
  type RuntimeSettings,
  type SettingsGetResponseBody,
  type SettingsPatchRequest,
  type SettingsPayload,
  type SettingsUpdateResponseBody,
  type SystemSummary,
  settingsGet,
  settingsUpdate,
} from '@/generated/openapi'
import { callAdmin } from '@/lib/router-api'

export type DBSettings = DbSettings

export type {
  DbSettings,
  PluginIntent,
  PluginSummary,
  RuntimeSettings,
  SettingsPayload,
  SystemSummary,
}

export type SettingsPatch = SettingsPatchRequest

const SETTINGS_KEY = ['admin', 'settings'] as const

export function useSettings() {
  return useQuery({
    queryKey: SETTINGS_KEY,
    queryFn: () => callAdmin<SettingsGetResponseBody>(settingsGet()),
    staleTime: 10_000,
  })
}

export function useUpdateSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (patch: SettingsPatch) =>
      callAdmin<SettingsUpdateResponseBody>(settingsUpdate({ body: patch })),
    // POST returns the full payload, so we hydrate the cache from
    // the mutation response rather than re-issuing a GET.
    onSuccess: (data) => {
      qc.setQueryData<SettingsPayload>(SETTINGS_KEY, data)
    },
  })
}
