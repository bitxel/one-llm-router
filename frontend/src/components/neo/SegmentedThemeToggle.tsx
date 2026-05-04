import { Moon, Sun } from 'lucide-react'
import { useEffect, useState } from 'react'

type Theme = 'light' | 'dark'

const STORAGE_KEY = 'one-llm-router.theme'

function readInitial(): Theme {
  if (typeof window === 'undefined') return 'dark'
  const stored = window.localStorage.getItem(STORAGE_KEY)
  if (stored === 'light' || stored === 'dark') return stored
  if (typeof window.matchMedia !== 'function') return 'dark'
  return window.matchMedia('(prefers-color-scheme: light)')?.matches ? 'light' : 'dark'
}

function apply(theme: Theme) {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  root.setAttribute('data-theme', theme)
  // Legacy `.light` class retained so any residual selector still matches.
  root.classList.toggle('light', theme === 'light')
}

/**
 * SegmentedThemeToggle — icon-only dark|light switch embedded in the
 * top bar, echoing v9 §.theme-toggle.
 *
 * Stateful: persists `one-llm-router.theme` in localStorage and
 * honours `Cmd/Ctrl + Shift + L` as a global toggle shortcut.
 */
export function SegmentedThemeToggle() {
  const [theme, setTheme] = useState<Theme>(readInitial)

  useEffect(() => {
    apply(theme)
    try {
      window.localStorage.setItem(STORAGE_KEY, theme)
    } catch {
      /* storage may be unavailable (private mode) — fall through */
    }
  }, [theme])

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key.toLowerCase() === 'l') {
        e.preventDefault()
        setTheme((t) => (t === 'dark' ? 'light' : 'dark'))
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  return (
    <div
      role="radiogroup"
      aria-label="Theme"
      className="inline-flex h-12 items-center gap-[2px] border p-[2px] sm:h-[26px]"
      style={{
        background: 'var(--panel-2)',
        borderColor: 'var(--line-2)',
        borderRadius: 3,
      }}
    >
      <Segment
        target="dark"
        current={theme}
        onSelect={setTheme}
        icon={<Moon className="h-4 w-4 sm:h-3 sm:w-3" strokeWidth={1.8} />}
        label="Dark"
      />
      <Segment
        target="light"
        current={theme}
        onSelect={setTheme}
        icon={<Sun className="h-4 w-4 sm:h-3 sm:w-3" strokeWidth={1.8} />}
        label="Light"
      />
    </div>
  )
}

function Segment({
  target,
  current,
  onSelect,
  icon,
  label,
}: {
  target: Theme
  current: Theme
  onSelect: (t: Theme) => void
  icon: React.ReactNode
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
      className="inline-flex h-11 w-11 cursor-pointer items-center justify-center transition-colors sm:h-5 sm:w-5"
      style={{
        background: on ? 'var(--panel)' : 'transparent',
        color: on ? 'var(--accent)' : 'var(--text-muted)',
        boxShadow: on ? 'inset 0 0 0 1px var(--line)' : 'none',
        borderRadius: 2,
      }}
    >
      {icon}
    </button>
  )
}
