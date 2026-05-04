import { Outlet } from '@tanstack/react-router'

import { TopBar, type TopBarTag } from '@/components/neo'

/**
 * SetupLayout — first-install chrome. Carries only the brand TopBar
 * (configuring tag + theme toggle); the right-hand installer rail is
 * owned by the wizard itself via `Canvas variant="rail"` so the stepper
 * can share the wizard's state.
 */
export function SetupLayout() {
  const tags: TopBarTag[] = [{ kind: 'info', label: 'configuring' }]

  return (
    <div className="flex min-h-full w-full flex-col text-[var(--text)]">
      <TopBar slash="/ installer" tags={tags} />
      <main id="main-content" tabIndex={-1} className="flex-1">
        <Outlet />
      </main>
    </div>
  )
}
