import { describe, expect, it } from 'vitest'

/**
 * Catalog parity gate.
 *
 * Enumerates every `src/locales/<locale>/<namespace>.json` file via
 * import.meta.glob (NOT a hardcoded locale list) so that adding a
 * language directory + registering it in `src/i18n/locales.ts` is the
 * only work needed for the new language to be covered by this test.
 *
 * Failure here means a translation is missing a key that another
 * locale has — the release blocker for silent untranslated UI.
 */

interface JsonTree {
  [key: string]: unknown
}

const modules = import.meta.glob('./*/*.json', {
  eager: true,
  import: 'default',
}) as Record<string, JsonTree>

const catalogsByLocale = new Map<string, Map<string, JsonTree>>()
for (const [path, tree] of Object.entries(modules)) {
  // Path shape: ./<locale>/<namespace>.json
  const match = /^\.\/([^/]+)\/([^/]+)\.json$/.exec(path)
  if (!match) throw new Error(`unexpected catalog path: ${path}`)
  const [, locale, namespace] = match
  if (!catalogsByLocale.has(locale)) catalogsByLocale.set(locale, new Map())
  catalogsByLocale.get(locale)?.set(namespace, tree)
}

function leafKeys(tree: JsonTree, prefix = ''): string[] {
  return Object.entries(tree).flatMap(([key, value]) => {
    const path = prefix ? `${prefix}.${key}` : key
    return value !== null && typeof value === 'object' && !Array.isArray(value)
      ? leafKeys(value as JsonTree, path)
      : [path]
  })
}

const enNamespaces = [...(catalogsByLocale.get('en')?.keys() ?? [])]

describe('catalog parity', () => {
  it('discovers the en baseline with all expected namespaces', () => {
    expect(catalogsByLocale.get('en')).toBeDefined()
    expect(enNamespaces.sort()).toEqual(
      [
        'accounts',
        'common',
        'dashboard',
        'errors',
        'playground',
        'requests',
        'settings',
        'setup',
      ].sort(),
    )
  })

  it('every locale ships the same namespace file set as en', () => {
    for (const [locale, catalogs] of catalogsByLocale) {
      expect([...catalogs.keys()].sort(), `locale ${locale}`).toEqual(enNamespaces.sort())
    }
  })

  it('every locale has exactly the same key set as en per namespace', () => {
    expect(catalogsByLocale.size).toBeGreaterThan(1)
    for (const [locale, catalogs] of catalogsByLocale) {
      if (locale === 'en') continue
      for (const namespace of enNamespaces) {
        const enKeys = leafKeys(catalogsByLocale.get('en')?.get(namespace) ?? {}).sort()
        const localeKeys = leafKeys(catalogs.get(namespace) ?? {}).sort()
        expect(
          localeKeys.filter((key) => !enKeys.includes(key)),
          `${locale}/${namespace} has keys missing from en`,
        ).toEqual([])
        expect(
          enKeys.filter((key) => !localeKeys.includes(key)),
          `${locale}/${namespace} is missing keys present in en`,
        ).toEqual([])
      }
    }
  })

  it('no catalog value is an empty string (except intentionally blank hints)', () => {
    // `newOauthDevice.fields.countdown.hint` is blank by design (parity
    // with the original UI which rendered no hint).
    const ALLOWED_EMPTY_KEYS = new Set(['newOauthDevice.fields.countdown.hint'])
    for (const [locale, catalogs] of catalogsByLocale) {
      for (const [namespace, tree] of catalogs) {
        for (const key of leafKeys(tree)) {
          const value = key
            .split('.')
            .reduce<unknown>(
              (node, part) =>
                node !== null && typeof node === 'object' ? (node as JsonTree)[part] : undefined,
              tree,
            )
          if (typeof value === 'string' && value === '') {
            expect(ALLOWED_EMPTY_KEYS.has(key), `${locale}/${namespace}.${key} is empty`).toBe(true)
          }
        }
      }
    }
  })
})
