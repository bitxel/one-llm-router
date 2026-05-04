# one-llm-router Product Context

## Product Register

one-llm-router is product UI, not brand UI. Design serves repeated operator work: configuring a router, adding upstream accounts, checking health, inspecting request records, and testing provider compatibility. Visual impact is secondary to fast scanning, predictable controls, and trustworthy system state.

## Primary Users

- Platform operators who run the router on an internal network and need to see health, accounts, request failures, and runtime settings quickly.
- Internal client developers who need one stable endpoint for Codex-compatible clients and need clear feedback when setup or routing is blocked.
- Protocol adapter maintainers who inspect request details, provider transport choices, and compatibility behavior.

## Jobs To Be Done

- Install the router from a cold start without persisting secrets before commit.
- Add or inspect upstream accounts, including API-key and OAuth-backed rows.
- Confirm that the router has active capacity before sending `/v1/*` traffic.
- Search request history and open detail records without leaking captured bodies in list views.
- Tune observability and plugin intent settings without restarting the service.
- Run a playground request and copy integration examples for client developers.

## Product Voice

Calm, terse, operator-grade. Use stable nouns from the system: account, request, upstream, plugin intent, setup gate, router, data plane. Avoid hype, marketing claims, and generic SaaS filler.

## Mobile Promise

The admin portal is not a mobile-first consumer app, but it must remain operational on phone-width screens for emergency checks. At 360px and wider, operators must be able to navigate, read page state, submit forms, flip switches, and open detail layers without global horizontal page overflow. Dense tables and code bodies may use local horizontal scrolling only inside their own containers.

## Anti-References

- Purple or blue-purple gradient SaaS surfaces.
- Glassmorphism, decorative blobs, bokeh, and card stacks used as ornament.
- Oversized marketing heroes on operational pages.
- Generic copy such as "boost productivity" or "all-in-one platform".
- Hiding critical operator controls on mobile instead of adapting their layout.
