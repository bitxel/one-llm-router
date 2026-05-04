import type { TopBarTag } from '@/components/neo'

export interface RouterSystemSummary {
  router_version?: string | null
  router_git_sha?: string | null
  router_built_at?: string | null
}

export function buildVersionTag(system?: RouterSystemSummary | null): TopBarTag | null {
  const rawVersion = system?.router_version
  const version = typeof rawVersion === 'string' ? rawVersion.trim() : ''
  if (!version) return null
  return { kind: 'plain', label: `build ${version}` }
}
