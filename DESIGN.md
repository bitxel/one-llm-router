# one-llm-router Design System

## 1. Design Intent

Neo-retro operations console: dense, angular, readable, and telemetry-first. The interface should feel like a control plane an operator can trust under pressure. It should not look like a landing page or a generic AI dashboard.

## 2. Visual Language

- Use the existing IBM Plex Sans and IBM Plex Mono pair.
- Keep corners sharp: 2px is the default radius; 3-4px only for existing component exceptions.
- Use flame orange as the primary accent. Semantic colors are reserved for state: ok, warn, error, info.
- Prefer full-width page flow and bounded panels. Do not nest cards inside cards.
- Use icons for actions when a clear lucide icon exists, paired with text for non-obvious commands.
- Keep letter spacing at `0` for normal text. Positive tracking is allowed only for small uppercase metadata.

## 3. Layout Rules

- Build mobile first, then enhance at `sm` (640px), `md` (768px), `lg` (1024px), `xl` (1280px).
- Phone gutters are 16px. Desktop gutters are 24px.
- The app shell must stack below `md`: TopBar, horizontal admin navigation, then content. Fixed sidebars are desktop-only.
- Main content must own the vertical scroll. The document must not gain global horizontal scroll at 360px or 390px.
- Data tables, JSON bodies, code samples, and long identifiers may scroll horizontally inside local containers, never by widening the whole page.

## 4. Component Rules

- Mobile interactive targets must be at least 44px tall and 44px wide where applicable. Desktop may retain denser controls.
- Panel headers need truncation-safe title and meta slots.
- Switches, segmented controls, selects, inputs, icon buttons, and modal close buttons must remain touchable on phones.
- Long hostnames, request ids, DSNs, model names, and account labels must truncate, wrap, or scroll in a local container.
- Detail layers must fit inside `100dvh` with internal scrolling.

## 5. Responsive Acceptance

Before finishing frontend UI work that touches layout, verify these routes with Playwright at 360px and 390px widths:

- `/setup`
- `/admin`
- `/admin/accounts`
- `/admin/requests`
- `/admin/settings`
- `/admin/playground`

Acceptance: `document.documentElement.scrollWidth <= document.documentElement.clientWidth` for the app chrome and page body. Intentional inner scrollers are acceptable only when the scroll container is visible and scoped to table/code content.

## 6. Design Review Traps

- No viewport-width font scaling.
- No negative letter spacing.
- No desktop-only sidebars on phone widths.
- No action bars that depend on hover-only affordances.
- No text clipped inside buttons, tags, cards, or panel headers.
- No hidden business state on mobile unless it has an adjacent accessible path.
