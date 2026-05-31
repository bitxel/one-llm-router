package adminapi

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/setup"
)

// ConfigUpdater is the default SettingsUpdater production wiring uses.
// It serialises writes under setup.Serialiser so settings updates and
// a (concurrent) setup commit never race on the config file lock.
//
// Responsibilities:
//
//   - Read the live *Config (via reader).
//   - Apply the typed patch onto a fresh copy.
//   - WriteAtomic the new config.json at path.
//   - Publish the new config via publisher.
//
// The separation between ConfigUpdater and the loader/publisher in
// internal/config keeps file I/O + publish semantics out of the
// handler, so handler tests can exercise validation behaviour without
// touching disk.
type ConfigUpdater struct {
	path      string
	reader    ConfigReader
	publisher LivePublisher

	// env is the environment lookup closure used by post-write
	// re-loads. Production wiring passes config.OSEnv(); tests pass
	// a MapEnv. Nil falls back to config.OSEnv(). The env overlay is
	// the source of truth for the 2013 env_override_readonly guard —
	// we must re-read it after WriteAtomic so the published SourceMap
	// reflects the new file plus the current env.
	env config.Env

	// onReload is an optional hook fired after the new *config.Config
	// has been persisted and published. BuildApp wires it to
	// applyLogLevel so /api/admin/settings/update flips the root
	// logger's slog.LevelVar without a restart (FR-011). The hook
	// runs while mu is held, which keeps "persisted then hot-reloaded"
	// observable as a single atomic step from the caller's POV.
	onReload func(*config.Config)

	// mu provides same-process serialisation so two simultaneous
	// admin patches cannot clobber each other at the atomic-rename
	// boundary. Concurrent access with the wizard commit path is
	// handled by setup.Serialiser upstream.
	mu sync.Mutex
}

// LivePublisher is the narrow write-side of internal/config's atomic
// slot. *config.LivePublisher satisfies it.
type LivePublisher interface {
	Store(cfg *config.Config)
}

// NewConfigUpdater wires a production ConfigUpdater. All three
// arguments are required — nil produces a receiver that errors on
// first call rather than panicking in WriteAtomic.
func NewConfigUpdater(path string, reader ConfigReader, publisher LivePublisher) *ConfigUpdater {
	return &ConfigUpdater{path: path, reader: reader, publisher: publisher}
}

// SetEnv installs the env closure used for post-write SourceMap
// re-computation. Passing nil is tolerated — the updater then skips
// re-publishing SourceMap, which preserves the prior boot-time map.
// That remains correct for the 002 env set (only db.driver and db.url
// are env-overridable, neither is patchable via settings).
func (u *ConfigUpdater) SetEnv(env config.Env) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.env = env
}

// SetOnReload installs an optional post-publish hook. Passing nil
// clears any previously registered hook. Callers typically wire the
// hook at app-construction time to push live runtime settings (e.g.
// log level) into long-lived subsystems.
func (u *ConfigUpdater) SetOnReload(fn func(*config.Config)) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.onReload = fn
}

// Update implements SettingsUpdater.
func (u *ConfigUpdater) Update(patch SettingsPatch) (*config.Config, error) {
	if u == nil || u.reader == nil || u.publisher == nil {
		return nil, errors.New("adminapi.ConfigUpdater: not wired")
	}
	if u.path == "" {
		return nil, errors.New("adminapi.ConfigUpdater: path is empty")
	}

	// Take both locks in a stable order to avoid dead-locks with the
	// wizard commit path: setup.Serialiser FIRST (matches commit
	// ordering), our own mu SECOND.
	setup.Serialiser.Lock()
	defer setup.Serialiser.Unlock()

	u.mu.Lock()
	defer u.mu.Unlock()

	cur := u.reader.Load()
	if cur == nil {
		return nil, errors.New("adminapi.ConfigUpdater: no live config")
	}
	next := cloneConfig(cur)
	applyPatch(next, patch)

	if err := config.WriteAtomic(u.path, next); err != nil {
		return nil, fmt.Errorf("write config.json: %w", err)
	}
	u.publisher.Store(next)

	// Re-load to re-compute the SourceMap: a settings update can
	// flip a field from "env:ROUTER_*" to "file" (or vice-versa when
	// env changes behind the router's back) and the published map
	// must stay in sync so future /api/admin/settings/update calls
	// see the correct provenance for the 2013 guard. The re-load
	// also validates that the WriteAtomic actually produced a
	// loadable file — fail loud here rather than on the next boot.
	if u.env != nil {
		if _, src, lerr := config.Load(context.Background(), u.path, u.env); lerr == nil {
			config.SourcePublisher.Store(src)
		}
	}

	if u.onReload != nil {
		u.onReload(next)
	}
	return next, nil
}

// cloneConfig returns a deep copy of cfg suitable for mutation.
// Implemented by-field rather than via reflection so adding a new
// field produces a compile error here — a silent miss would cause a
// settings update to either share memory (race condition) or lose
// the field entirely.
func cloneConfig(cfg *config.Config) *config.Config {
	out := *cfg
	out.Runtime.ModelRenames = cloneConfigModelRenameRules(cfg.Runtime.ModelRenames)
	// Defensive copy of the plugin sub-structs (currently already
	// value types but written out for symmetry with future pointer
	// fields).
	out.Plugins = config.PluginsConfig{
		AdminAuth: config.AdminAuthPluginConfig{
			Enabled:           cfg.Plugins.AdminAuth.Enabled,
			SessionTTLMinutes: cfg.Plugins.AdminAuth.SessionTTLMinutes,
			JWTSecretRef:      cfg.Plugins.AdminAuth.JWTSecretRef,
		},
		ClientKeys: config.ClientKeysPluginConfig{
			Enabled: cfg.Plugins.ClientKeys.Enabled,
		},
	}
	return &out
}

// applyPatch mutates cfg in-place with the non-nil fields of patch.
func applyPatch(cfg *config.Config, p SettingsPatch) {
	if p.LogClientRequestBody != nil {
		cfg.Runtime.LogClientRequestBody = *p.LogClientRequestBody
	}
	if p.LogUpstreamRequestBody != nil {
		cfg.Runtime.LogUpstreamRequestBody = *p.LogUpstreamRequestBody
	}
	if p.LogUpstreamResponseBody != nil {
		cfg.Runtime.LogUpstreamResponseBody = *p.LogUpstreamResponseBody
	}
	if p.LogRetentionDays != nil {
		cfg.Runtime.LogRetentionDays = *p.LogRetentionDays
	}
	if p.LogLevel != nil {
		cfg.Runtime.LogLevel = *p.LogLevel
	}
	if p.ModelRenames != nil {
		cfg.Runtime.ModelRenames = cloneConfigModelRenameRules(*p.ModelRenames)
	}
	if p.AdminAuthEnabled != nil {
		cfg.Plugins.AdminAuth.Enabled = *p.AdminAuthEnabled
	}
	if p.ClientKeysEnabled != nil {
		cfg.Plugins.ClientKeys.Enabled = *p.ClientKeysEnabled
	}
}

func cloneConfigModelRenameRules(in []config.ModelRenameRule) []config.ModelRenameRule {
	if in == nil {
		return []config.ModelRenameRule{}
	}
	out := make([]config.ModelRenameRule, len(in))
	copy(out, in)
	return out
}
