# one-llm-router — Frontend (Admin Control Panel)

> **Phase note**: Frontend work activates in feature **002-setup-wizard-and-admin-portal-skeleton** (see `specs/002-.../plan.md`). 002 stands up the Vite+React+shadcn skeleton, the setup wizard, and the admin portal shell; 003/004/005 add feature pages into this shell.

This document covers frontend-specific engineering standards. The root `AGENTS.md` is the authoritative reference for project-wide rules; this file extends it with frontend detail.

## Tech Stack Summary

| Layer | Choice |
|-------|--------|
| Framework | React 19 + Vite 6 |
| Language | TypeScript 5 (strict mode, no `any`) |
| UI Components | shadcn/ui (Tailwind CSS v4, Radix primitives) |
| Routing | TanStack Router v1 |
| Server State | TanStack Query v5 |
| Data Tables | TanStack Table v8 |
| Forms | React Hook Form + Zod |
| Charts | Recharts v2.15+ |
| i18n | react-i18next (en, zh-CN) |
| Lint + Format | Biome v2 |
| Type Check | `tsc --noEmit` (CI gate) |
| Unit / Integration | Vitest + React Testing Library + MSW |
| E2E | Playwright |
| Package Manager | pnpm 10.30.3 |

## Development Runtime Rule

Frontend edits are not visible in production-style local testing until the SPA is rebuilt, staged, embedded, and the server that serves it is restarted. After changing frontend code, run `make fe-build` from the repository root, then `make go-build` to stage `frontend/dist` into `internal/spa/dist` and rebuild `./one-llm-router`. Restart the running backend before validating in the browser. If OpenAPI-generated types changed, regenerate the TS client before building.

## Directory Structure

```
frontend/
├── public/                     # Static assets (favicon, robots.txt)
├── src/
│   ├── app/                    # App shell, providers, root layout
│   │   ├── providers.tsx       # QueryClient, Router, I18n, Theme providers
│   │   └── root.tsx            # Root component
│   ├── routes/                 # TanStack Router file-based routes
│   │   ├── __root.tsx          # Root route with layout
│   │   ├── index.tsx           # Dashboard home
│   │   ├── accounts/           # Upstream account management
│   │   ├── api-keys/           # Client API key management
│   │   ├── requests/           # Request history search
│   │   └── usage/              # Usage summary dashboards
│   ├── components/
│   │   ├── ui/                 # shadcn/ui components (project-owned)
│   │   └── shared/             # Shared business components
│   ├── hooks/                  # Custom React hooks
│   ├── lib/                    # Utility functions and helpers
│   ├── generated/              # @hey-api/openapi-ts output (never hand-edit)
│   ├── locales/
│   │   ├── en/                 # English translations
│   │   │   ├── common.json
│   │   │   ├── accounts.json
│   │   │   ├── api-keys.json
│   │   │   ├── requests.json
│   │   │   └── usage.json
│   │   └── zh-CN/              # Chinese translations (same structure)
│   ├── stores/                 # Zustand stores (only when justified)
│   └── styles/
│       └── globals.css         # Tailwind imports + CSS custom properties
├── tests/
│   ├── e2e/                    # Playwright E2E tests
│   └── setup.ts                # Vitest global setup (MSW, etc.)
├── AGENTS.md                   # This file
├── biome.json                  # Biome configuration
├── index.html                  # Vite entry HTML
├── package.json                # pnpm package config
├── tsconfig.json               # TypeScript config (strict)
├── vite.config.ts              # Vite configuration
└── playwright.config.ts        # Playwright configuration
```

## Routing Conventions

TanStack Router provides type-safe routing. Follow these patterns:

### Route Definition

```tsx
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

export const Route = createFileRoute('/accounts/')({
  validateSearch: z.object({
    page: z.number().default(1),
    pageSize: z.number().default(20),
    status: z.enum(['active', 'disabled', 'archived']).optional(),
    q: z.string().optional(),
  }),
  component: AccountsPage,
})
```

### Navigation

```tsx
import { Link, useNavigate } from '@tanstack/react-router'

// Declarative (preferred)
<Link to="/accounts" search={{ page: 1, status: 'active' }}>
  Active Accounts
</Link>

// Imperative
const navigate = useNavigate()
navigate({ to: '/accounts/$accountId', params: { accountId } })
```

### Rules
- All route search params MUST have a Zod schema for validation and type inference.
- Filters, pagination, and sorting live in URL search params — never in local/global state.
- Use route-level `loader` for data prefetching when beneficial.

## Data Fetching Conventions

### Query Organization

```tsx
// src/hooks/use-accounts.ts
import { queryOptions, useQuery, useMutation } from '@tanstack/react-query'
import { client } from '@/generated'

export const accountsQueryOptions = (search: AccountSearch) =>
  queryOptions({
    queryKey: ['accounts', search],
    queryFn: () => client.listAccounts({ query: search }),
  })

export function useAccounts(search: AccountSearch) {
  return useQuery(accountsQueryOptions(search))
}

export function useCreateAccount() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: client.createAccount,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['accounts'] })
    },
  })
}
```

### Rules
- One query options factory per resource (accounts, apiKeys, requests, usage).
- Query keys follow the pattern: `[resource, ...params]`.
- Mutations MUST invalidate affected queries on success.
- Never duplicate server state into Zustand or React state.

## Component Conventions

### File Naming
- Components: `PascalCase.tsx` (e.g., `AccountTable.tsx`)
- Hooks: `use-kebab-case.ts` (e.g., `use-accounts.ts`)
- Utilities: `kebab-case.ts` (e.g., `format-date.ts`)
- Route files: follow TanStack Router file-based routing conventions

### Component Structure

```tsx
import { useTranslation } from 'react-i18next'

interface AccountTableProps {
  accounts: Account[]
  isLoading: boolean
}

export function AccountTable({ accounts, isLoading }: AccountTableProps) {
  const { t } = useTranslation('accounts')

  // ... component logic

  return (
    // ... JSX
  )
}
```

### Rules
- Use named exports (not default exports) for all components.
- Props are defined as interfaces, not inline types.
- Every user-visible string uses `t()` from react-i18next.
- Prefer composition over prop drilling — use TanStack Query hooks inside child components rather than passing data through many layers.

## shadcn/ui Conventions

- Components live in `src/components/ui/` and are project-owned code.
- Add components via `pnpm dlx shadcn@latest add [component]`.
- Customize appearance through CSS custom properties (theme tokens) in `globals.css`, not by editing component internals directly.
- When extending a component, create a wrapper in `src/components/shared/` that composes the base component.
- Never import from Radix directly — use the shadcn/ui wrappers.

## Responsive Design Standards

- Read root `PRODUCT.md` and `DESIGN.md` before changing user-facing UI.
- Build mobile first, then enhance at Tailwind `sm`, `md`, `lg`, and `xl`. The admin sidebar is desktop-only; phone widths use the horizontal nav below the TopBar.
- Phone gutters are 16px. Desktop gutters are 24px. Do not create page-level horizontal overflow at 360px or 390px.
- Mobile controls must keep at least a 44px touch target for buttons, icon buttons, inputs, selects, switches, and segmented toggles. Desktop density may stay compact with responsive classes.
- Data tables, code blocks, request bodies, and long identifiers may use local overflow containers. They must not widen the document.
- Do not scale type with viewport width and do not use negative letter spacing. Positive tracking is reserved for compact uppercase metadata.
- Before finishing layout work, run a Playwright mobile check for `/setup`, `/admin`, `/admin/accounts`, `/admin/requests`, `/admin/settings`, and `/admin/playground`.

## Form Conventions

```tsx
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'

const createAccountSchema = z.object({
  name: z.string().min(1).max(128),
  provider: z.enum(['openai', 'anthropic']),
  credentials: z.string().min(1),
})

type CreateAccountForm = z.infer<typeof createAccountSchema>

export function CreateAccountDialog() {
  const form = useForm<CreateAccountForm>({
    resolver: zodResolver(createAccountSchema),
  })
  const createAccount = useCreateAccount()

  function onSubmit(data: CreateAccountForm) {
    createAccount.mutate(data)
  }

  return (
    <Form {...form}>
      {/* shadcn/ui Form fields */}
    </Form>
  )
}
```

### Rules
- Every form has a Zod schema that doubles as runtime validation.
- Zod schemas for forms MAY reuse types generated from OpenAPI where applicable.
- Server errors from mutations are displayed inline near the relevant field.

## i18n Conventions

Implemented with **react-i18next + i18next** (typed keys, statically bundled catalogs). Supported languages: `en` (fallback) and `zh-CN`; the registry in `src/i18n/locales.ts` is the single source of truth.

### File Structure
```
src/i18n/
├── index.ts         # initI18n(): sync init, <html lang> sync, tDynamic()
├── locales.ts       # SUPPORTED_LOCALES registry (code, Intl locale, prefixes)
├── detect.ts        # localStorage -> navigator -> 'en' detection
├── resources.ts     # catalog assembly (en + zh-CN)
└── i18next.d.ts     # CustomTypeOptions — typed t() keys
src/locales/
├── en/              # common dashboard accounts requests playground settings setup errors
└── zh-CN/           # same structure, deep key parity enforced by src/locales/parity.test.ts
```

### Usage

```tsx
import { useTranslation } from 'react-i18next'

function AccountStatus({ status }: { status: string }) {
  const { t } = useTranslation('accounts')
  return <Badge>{t(`accountStatus.${status}` as 'accountStatus.active')}</Badge>
}
```

### Rules
- Namespace per feature module (`common` for shared chrome, `errors` keyed by the symbols in `lib/errcode.ts`).
- Key format: `module.section.key`. Components use `useTranslation(ns)`; module-scope helpers use `const t = i18n.getFixedT(null, ns)` (language resolves at call time; the page root must call `useTranslation(ns)` so the subtree re-renders on switch).
- Never hardcode user-facing strings in JSX. Whitelist: brand names, compact metric units (`2h 30m`), log levels, `<noscript>`, zod library defaults, developer contract-violation messages.
- Validation messages: store catalog keys in zod schemas (`'setup:validation.dsnRequired'`) and render through `tDynamic(error.message)` from `@/lib/error-message` — never call `t()` at module scope for message text.
- Dates/numbers go through `@/lib/intl` (locale follows the active language). Never construct `Intl.*` formatters at module scope — they freeze the locale; memoize per locale or construct per call.
- Language detection: stored choice (`one-llm-router.lang`) beats browser inference; inferred defaults are never persisted — only the switcher (`SegmentedLanguageToggle` in the TopBar) writes.
- Embedded JSX in translations uses `<Trans>`; catalog entries mark element slots as `<0>`, `<1>`, … in BOTH locales.
- Catalog parity (`pnpm vitest run src/locales/parity.test.ts`) is a release gate: every locale must have the exact en key set (including `_one/_other` plural suffixes).
- E2E pins `locale: 'en-US'` in `playwright.config.ts`; unit tests boot i18n pinned to `en` in `tests/setup.ts`.

### Adding a language (runbook)
1. Copy `src/locales/en/` → `src/locales/<code>/` and translate (keep `_one/_other` suffix keys).
2. Append one entry to `SUPPORTED_LOCALES` in `src/i18n/locales.ts` (code, Intl locale, native name, browser-tag prefixes).
3. Done — detection, the TopBar switcher, `<html lang>` sync, typed keys, and the parity gate pick it up automatically.

## Testing Conventions

### Unit / Integration (Vitest)

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AccountTable } from './AccountTable'
import { createTestWrapper } from '@/tests/setup'

describe('AccountTable', () => {
  it('displays account list', async () => {
    render(<AccountTable accounts={mockAccounts} isLoading={false} />, {
      wrapper: createTestWrapper(),
    })

    expect(screen.getByText('Production Account')).toBeInTheDocument()
  })
})
```

### E2E (Playwright)

```ts
import { test, expect } from '@playwright/test'

test('operator can disable an upstream account', async ({ page }) => {
  await page.goto('/accounts')
  await page.getByRole('row', { name: /Production/ }).getByRole('button', { name: /actions/i }).click()
  await page.getByRole('menuitem', { name: /disable/i }).click()
  await page.getByRole('button', { name: /confirm/i }).click()
  await expect(page.getByText('disabled')).toBeVisible()
})
```

### Rules
- Use `userEvent` (not `fireEvent`) for realistic browser event simulation.
- Use MSW handlers to mock API responses at the network layer.
- E2E tests target 5–10 critical operator journeys, not every button.
- Test IDs (`data-testid`) are allowed but prefer accessible selectors (`getByRole`, `getByText`).

## Accessibility Baseline

- All interactive elements must be keyboard-navigable.
- Focus indicators must be visible (shadcn/ui provides these by default).
- Use semantic HTML: `<nav>`, `<main>`, `<section>`, `<button>`, `<table>`.
- Color contrast must meet WCAG 2.1 AA (4.5:1 for text).
- Screen reader labels via `aria-label` where visible text is insufficient.

## Performance Guidelines

- Lazy-load route components using TanStack Router code splitting.
- Use TanStack Table + TanStack Virtual for tables with >100 rows (TanStack Table does not ship virtualization itself).
- Images use responsive sizes and modern formats (WebP/AVIF).
- Monitor bundle size in CI; investigate regressions >10%.
