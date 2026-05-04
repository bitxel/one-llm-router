// Package plugin hosts the compile-time plugin registry and the
// capability interfaces the router exposes as extension points.
//
// Mental model (see contracts/plugin-interface.md §Mental model):
//
//  1. Plugin is the root interface every plugin implements. It answers
//     one question: "who am I?" via ID(). "Am I turned on?" is NOT a
//     plugin question — the app reads config.Plugins.Enabled(id) from
//     the typed config struct. Plugins cannot override the app's
//     decision.
//  2. Capability interfaces — one per extension point (AdminAuth,
//     ClientKeyAuth, ProxyHook, …). A plugin declares participation by
//     satisfying the interface; BuildApp discovers participation via
//     type-assertion.
//  3. Registry (see registry.go, T-022). Bindings live there; this
//     file defines only the interfaces and the Deps struct.
//
// 002 ships ZERO concrete plugins. The interfaces exist and the
// registry exists, but nothing registers into it yet. 003/004/005 add
// concrete plugins.
//
// INVARIANT (enforced by tests): this package MUST NOT define an
// Enabled() method anywhere. Enabled/disabled is a config concern,
// not a plugin concern (architectural decision — see plugin-interface
// .md changelog 3.0).
package plugin

import (
	"context"
	"log/slog"
	"time"

	"xorm.io/xorm"
)

// Plugin is the root interface every plugin implements. It does not
// by itself provide any behaviour — behaviour comes from the
// capability interfaces a plugin additionally implements (see
// capability.go).
type Plugin interface {
	// ID is the stable machine-readable feature flag key,
	// lower_snake_case. It is the object key under
	// config.json.plugins.<ID> and is emitted in structured logs. Two
	// plugins sharing the same ID is a boot-time fatal (panic at
	// init() in registry.go).
	ID() string
}

// Initializer is an OPTIONAL capability the app calls once after the
// app confirms the plugin is enabled (via config.Plugins.Enabled(id))
// but before any capability method is invoked. A non-nil return
// aborts boot so misconfiguration surfaces loudly at start rather
// than at first request.
//
// Plugins that need no one-time setup simply omit this interface.
type Initializer interface {
	Plugin
	Init(deps Deps) error
}

// Deps is the NARROW set of dependencies a plugin receives at Init
// time. Anything beyond this set MUST justify itself during review —
// bloating Deps turns plugins into god objects that reach into every
// subsystem.
//
// Field notes:
//   - Ctx: cancelled when the process is shutting down. Plugins
//     should respect ctx.Done for any long-running setup.
//   - DB: nil while config.json is absent (setup gate engaged). 002's
//     only registered plugins run AFTER the setup gate opens, so in
//     practice this is never nil at Init time — but Deps documents
//     the contract for future 003+ plugins that might be registered
//     conditionally.
//   - Logger: pre-scoped by BuildApp with plugin_id=<ID> so every log
//     line from a plugin is greppable.
//   - Clock: mockable. Plugins MUST NOT call time.Now() directly;
//     call deps.Clock() instead so tests can inject a frozen clock.
type Deps struct {
	Ctx    context.Context
	DB     *xorm.Engine
	Logger *slog.Logger
	Clock  func() time.Time
}
