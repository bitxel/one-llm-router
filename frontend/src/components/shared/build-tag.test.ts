import { describe, expect, it } from 'vitest'

import { buildVersionTag } from './build-tag'

describe('buildVersionTag', () => {
  it('formats the router version from health system metadata', () => {
    expect(
      buildVersionTag({
        router_version: ' v0.4.1 ',
        router_git_sha: 'abcdef1',
        router_built_at: '2026-04-29T00:00:00Z',
      }),
    ).toEqual({ kind: 'plain', label: 'build v0.4.1' })
  })

  it('omits the tag when runtime metadata is absent', () => {
    expect(buildVersionTag()).toBeNull()
    expect(buildVersionTag({ router_version: '' })).toBeNull()
    expect(buildVersionTag({ router_version: '   ' })).toBeNull()
  })
})
