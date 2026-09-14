import { useTranslation } from 'react-i18next'

import { type LocaleCode, persistLanguage, SUPPORTED_LOCALES } from '@/i18n'

/**
 * SegmentedLanguageToggle — text-based language switch embedded in the
 * top bar next to the theme toggle, mirroring SegmentedThemeToggle's
 * visual contract (radiogroup + role=radio, 44 px touch targets on
 * mobile, square corners, CSS-token palette).
 *
 * Each segment is labelled in its own language (EN / 中文) so the
 * operator can always find their language. Selecting a segment
 * persists the explicit choice to localStorage — browser-derived
 * defaults are never written.
 */
export function SegmentedLanguageToggle() {
  const { t, i18n } = useTranslation('common')
  const current = i18n.language

  return (
    <div
      role="radiogroup"
      aria-label={t('language.label')}
      className="inline-flex h-12 items-center gap-[2px] border p-[2px] sm:h-[26px]"
      style={{
        background: 'var(--panel-2)',
        borderColor: 'var(--line-2)',
        borderRadius: 3,
      }}
    >
      {SUPPORTED_LOCALES.map((locale) => (
        <Segment
          key={locale.code}
          target={locale.code}
          current={current}
          label={locale.nativeName}
          onSelect={(code) => {
            persistLanguage(code)
            void i18n.changeLanguage(code)
          }}
        />
      ))}
    </div>
  )
}

function Segment({
  target,
  current,
  onSelect,
  label,
}: {
  target: LocaleCode
  current: string
  onSelect: (code: LocaleCode) => void
  label: string
}) {
  const on = current === target
  return (
    // biome-ignore lint/a11y/useSemanticElements: Semantic `<input type="radio">` would destroy the segmented visual pattern (inset shadow + accent fill). Role="radio" on a button is specced as equivalent in WAI-ARIA 1.2.
    <button
      type="button"
      role="radio"
      aria-checked={on}
      aria-label={label}
      title={label}
      onClick={() => onSelect(target)}
      className="inline-flex h-11 min-w-11 cursor-pointer items-center justify-center px-2 transition-colors sm:h-5 sm:min-w-0 sm:px-[6px]"
      style={{
        background: on ? 'var(--panel)' : 'transparent',
        color: on ? 'var(--accent)' : 'var(--text-muted)',
        boxShadow: on ? 'inset 0 0 0 1px var(--line)' : 'none',
        borderRadius: 2,
        fontSize: 11,
        letterSpacing: '0.02em',
      }}
    >
      {label}
    </button>
  )
}
