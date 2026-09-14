import { beforeEach, describe, expect, it, vi } from 'vitest'

import { detectLanguage, LANGUAGE_STORAGE_KEY, persistLanguage } from './detect'

function stubNavigatorLanguages(tags: string[] | undefined) {
  Object.defineProperty(window.navigator, 'languages', {
    value: tags,
    configurable: true,
  })
  Object.defineProperty(window.navigator, 'language', {
    value: tags?.[0] ?? 'en-US',
    configurable: true,
  })
}

describe('detectLanguage', () => {
  beforeEach(() => {
    window.localStorage.clear()
    stubNavigatorLanguages(['en-US'])
  })

  it.each(['zh-CN', 'en'] as const)('stored value %s wins over the browser', (stored) => {
    stubNavigatorLanguages(['de-DE', 'en-US'])
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, stored)
    expect(detectLanguage()).toBe(stored)
  })

  it('ignores a garbage stored value and falls back to the browser', () => {
    stubNavigatorLanguages(['de-DE', 'en-US'])
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'not-a-language')
    expect(detectLanguage()).toBe('en')
  })

  it('maps Chinese browser tags (zh-TW, zh, zh-HK) onto zh-CN', () => {
    for (const tag of ['zh-TW', 'zh', 'zh-HK']) {
      stubNavigatorLanguages([tag])
      expect(detectLanguage()).toBe('zh-CN')
    }
  })

  it('maps English variants (en-GB, en) onto en', () => {
    for (const tag of ['en-GB', 'en']) {
      stubNavigatorLanguages([tag])
      expect(detectLanguage()).toBe('en')
    }
  })

  it('walks navigator.languages in order and picks the first supported match', () => {
    stubNavigatorLanguages(['de-DE', 'fr-FR', 'zh-TW'])
    expect(detectLanguage()).toBe('zh-CN')
  })

  it('falls back to en for unsupported browsers', () => {
    stubNavigatorLanguages(['de-DE', 'fr-FR'])
    expect(detectLanguage()).toBe('en')
  })

  it('falls back to en when navigator exposes no languages', () => {
    stubNavigatorLanguages(undefined)
    expect(detectLanguage()).toBe('en')
  })

  it('ignores a corrupted localStorage instead of throwing', () => {
    const getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage unavailable (private mode)')
    })
    stubNavigatorLanguages(['zh-CN'])
    expect(detectLanguage()).toBe('zh-CN')
    getItem.mockRestore()
  })
})

describe('persistLanguage', () => {
  beforeEach(() => {
    window.localStorage.clear()
  })

  it('persists the explicit choice where detectLanguage finds it', () => {
    persistLanguage('zh-CN')
    expect(window.localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe('zh-CN')
    stubNavigatorLanguages(['en-US'])
    expect(detectLanguage()).toBe('zh-CN')
  })

  it('swallows storage failures instead of throwing', () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota exceeded')
    })
    expect(() => persistLanguage('en')).not.toThrow()
    setItem.mockRestore()
  })
})
