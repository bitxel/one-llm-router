package config

import (
	"sync/atomic"
)

// livePtr is the in-process publishing slot for the currently-effective
// *Config. Reads through LiveReader and writes through LivePublisher
// are both lock-free under atomic.Pointer semantics — publishing is a
// single CAS-worthy pointer swap, observers see either the old
// snapshot or the new one in its entirety.
//
// Contract: callers MUST NOT mutate a *Config after it has been passed
// to Publisher.Store. Always construct a fresh *Config per publish.
// This keeps the "no torn reads" guarantee because observers hold only
// the pointer, never a mutex across the dereference.
//
// The slot starts nil. On steady-state boot BuildApp performs the
// first Store before any request-serving goroutine runs, so post-
// setup callers never observe a nil load. During setup-pending (no
// config.json on disk) BuildApp returns WITHOUT storing, so the slot
// remains nil until the wizard commit materializes config.json and
// reloads — handlers that run in setup-pending mode (today, only
// /api/admin/health) MUST tolerate a nil Load() return rather than
// deref blindly. Tests that exercise reader paths in isolation MUST
// pre-seed Publisher.Store with a non-nil *Config.
var livePtr atomic.Pointer[Config]

// liveSourcePtr mirrors livePtr for SourceMap snapshots so handlers
// that need to answer "where did this field come from?" (most notably
// POST /api/admin/settings/update, which must return 2013
// env_override_readonly when an operator patches an env-sourced key)
// can read the current provenance without passing maps through the
// handler graph. Publishing is ordered: Publisher.Store(cfg) is always
// followed by SourcePublisher.Store(src) from the same goroutine, so
// a reader that loads SourceMap before Config observes an entry that
// is either self-consistent with a newer Config (safe — source-based
// guards still reject writes correctly) or with an older Config
// (which is the exact snapshot the caller was going to patch anyway).
// Mismatches at this pair never widen the writable set; they can only
// make a write more conservative.
var liveSourcePtr atomic.Pointer[SourceMap]

// LiveReader is the read-only façade over the in-process config slot.
// Exposed as an exported value (not a package function) so call sites
// can be dependency-injected in tests — production code takes
// `config.Reader` while unit tests can substitute a fake reader sharing
// the same Load() shape.
type LiveReader struct{}

// Load returns the currently-published *Config. Safe to call from any
// goroutine; returns nil iff Publisher.Store has never been invoked.
// The returned pointer is shared with other concurrent readers and
// MUST be treated as read-only.
func (LiveReader) Load() *Config {
	return livePtr.Load()
}

// LivePublisher is the write façade. Store atomically replaces the
// published snapshot; the previously-published *Config becomes
// unreachable from this package but remains valid for any reader that
// is still holding a local reference to it.
type LivePublisher struct{}

// Store publishes cfg as the new effective configuration. Passing nil
// is legal — it returns the slot to its pre-BuildApp state and is used
// exclusively by tests that want to reset the global between cases.
// Production code never stores nil.
func (LivePublisher) Store(cfg *Config) {
	livePtr.Store(cfg)
}

// LiveSourceReader is the read façade over the published SourceMap.
// Returns nil until SourcePublisher.Store is invoked — handlers MUST
// tolerate nil (treat every field as patchable; fail open is safer
// than fail closed when provenance is unavailable).
type LiveSourceReader struct{}

// Load returns the currently-published SourceMap pointer. The caller
// MUST treat it as read-only — it is shared with every concurrent
// reader and mutations would race other Load callers.
func (LiveSourceReader) Load() *SourceMap {
	return liveSourcePtr.Load()
}

// LiveSourcePublisher is the write façade for the SourceMap slot.
type LiveSourcePublisher struct{}

// Store publishes src. Passing nil resets the slot (used by tests to
// isolate cases that have not wired source-based rejections).
func (LiveSourcePublisher) Store(src SourceMap) {
	if src == nil {
		liveSourcePtr.Store(nil)
		return
	}
	// Copy into a fresh map before publishing so later mutations of
	// the caller's map never race observers.
	cp := make(SourceMap, len(src))
	for k, v := range src {
		cp[k] = v
	}
	liveSourcePtr.Store(&cp)
}

// Reader and Publisher are the package-level singletons. BuildApp
// initialises them by calling Publisher.Store with the loaded config;
// handler code reads via Reader.Load(). See plan.md §Risk R-7 for the
// rationale behind using a single shared slot rather than per-subsystem
// snapshots.
var (
	Reader          LiveReader
	Publisher       LivePublisher
	SourceReader    LiveSourceReader
	SourcePublisher LiveSourcePublisher
)
