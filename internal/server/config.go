package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultConfigDir is where the daemon expects server.toml and the profiles/
// tree. Overridable via PSKILLS_CONFIG_DIR. Production:
// ~/.config/phantom-skills-server (mounted read-only into the container).
func DefaultConfigDir() string {
	if v := strings.TrimSpace(os.Getenv("PSKILLS_CONFIG_DIR")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config/phantom-skills-server" // last-ditch
	}
	return filepath.Join(home, ".config", "phantom-skills-server")
}

// DefaultDataDir is where the daemon keeps runtime state (the global flock,
// future caches). Overridable via PSKILLS_DATA_DIR.
func DefaultDataDir() string {
	if v := strings.TrimSpace(os.Getenv("PSKILLS_DATA_DIR")); v != "" {
		return v
	}
	return "/var/lib/phantom-skills"
}

// ServerConfig mirrors server.toml. Fields are exported because
// BurntSushi/toml uses reflection.
type ServerConfig struct {
	Server struct {
		Port     int    `toml:"port"`
		Host     string `toml:"host"`
		LogLevel string `toml:"log_level"`
	} `toml:"server"`

	// Postgres wires the skills registry System of Record. Empty DSN (after
	// env resolution) ⇒ Postgres disabled; the daemon still serves /health
	// and config validation but skills CRUD returns 503. Env override:
	// PSKILLS_POSTGRES_DSN.
	Postgres PostgresConfig `toml:"postgres"`

	// Brain wires phantom-brain integration: the daemon records pipeline
	// decisions (create/prune/promote) and telemetry rollups back into
	// long-term memory. Optional — absent block ⇒ integration disabled.
	Brain BrainConfig `toml:"brain"`

	// Pipeline holds knobs the intelligence pipeline reads. The algorithms
	// live in internal/pipeline; only the values live here.
	Pipeline PipelineConfig `toml:"pipeline"`

	Defaults ScopeDefaults `toml:"defaults"`
}

// PostgresConfig mirrors [postgres]. DSN points at the phantom_skills database.
type PostgresConfig struct {
	DSN string `toml:"dsn"`
}

// BrainConfig mirrors [brain]: how the daemon reaches phantom-brain's HTTP API.
type BrainConfig struct {
	API             string `toml:"api"`
	Token           string `toml:"token"`
	Profile         string `toml:"profile"`
	Vault           string `toml:"vault"`
	RecordLearnings bool   `toml:"record_learnings"`
	RecordTelemetry bool   `toml:"record_telemetry"`
}

// Enabled reports whether the operator wired a phantom-brain endpoint.
func (c BrainConfig) Enabled() bool { return strings.TrimSpace(c.API) != "" }

// PipelineConfig mirrors [pipeline]. auto_approve_below_risk = 0 keeps every
// create/prune/promote human-gated (the default).
type PipelineConfig struct {
	DetectorPollIntervalSecs int `toml:"detector_poll_interval_secs"`
	MinUsageForPromote       int `toml:"min_usage_for_promote"`
	AutoApproveBelowRisk     int `toml:"auto_approve_below_risk"`
}

// ScopeDefaults are the per-scope knobs. The same shape lives in
// profiles/<scope>/config.toml; nonzero fields there override these.
type ScopeDefaults struct {
	SyncChangeFeedPage int `toml:"sync_change_feed_page"`
}

// LoadServerConfig reads {configDir}/server.toml. Missing file is an error —
// the daemon refuses to start without an explicit config so operators don't
// get surprising port/host defaults.
func LoadServerConfig(configDir string) (*ServerConfig, error) {
	path := filepath.Join(configDir, "server.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("server: read %s: %w", path, err)
	}
	var cfg ServerConfig
	if _, err := toml.Decode(string(raw), &cfg); err != nil {
		return nil, fmt.Errorf("server: parse %s: %w", path, err)
	}
	applyServerDefaults(&cfg)
	return &cfg, nil
}

// applyServerDefaults fills in values the operator left unset and applies env
// overrides for the secrets/endpoints that are awkward to keep in a file.
func applyServerDefaults(cfg *ServerConfig) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 9997
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	if cfg.Server.LogLevel == "" {
		cfg.Server.LogLevel = "info"
	}

	if v := strings.TrimSpace(os.Getenv("PSKILLS_POSTGRES_DSN")); v != "" {
		cfg.Postgres.DSN = v
	}
	if v := strings.TrimSpace(os.Getenv("PSKILLS_BRAIN_TOKEN")); v != "" {
		cfg.Brain.Token = v
	}

	p := &cfg.Pipeline
	if p.DetectorPollIntervalSecs == 0 {
		p.DetectorPollIntervalSecs = 60
	}
	if p.MinUsageForPromote == 0 {
		p.MinUsageForPromote = 5
	}
	// AutoApproveBelowRisk intentionally defaults to 0 (fully human-gated).

	d := &cfg.Defaults
	if d.SyncChangeFeedPage == 0 {
		d.SyncChangeFeedPage = 100
	}
}

// ScopeOverrides is parsed from profiles/<scope>/config.toml. Every field is
// optional; only nonzero values override the global defaults. Skillset lets a
// scope name the skillset its bearer token is bound to (defaults to "default").
type ScopeOverrides struct {
	Skillset           string `toml:"skillset"`
	SyncChangeFeedPage int    `toml:"sync_change_feed_page"`
}

// ScopeAuth is parsed from profiles/<scope>/auth.toml. BearerToken is the only
// required field; Description is operator-facing only and never surfaces in
// API responses.
type ScopeAuth struct {
	BearerToken string `toml:"bearer_token"`
	Description string `toml:"description"`
}

// LoadScopeFiles reads config.toml + auth.toml for one scope. Errors on a
// missing/unreadable auth.toml (a scope without auth is unusable) but tolerates
// a missing config.toml (the scope inherits global defaults).
func LoadScopeFiles(configDir, scope string) (ScopeOverrides, ScopeAuth, error) {
	base := filepath.Join(configDir, "profiles", scope)
	var overrides ScopeOverrides
	if raw, err := os.ReadFile(filepath.Join(base, "config.toml")); err == nil {
		if _, err := toml.Decode(string(raw), &overrides); err != nil {
			return overrides, ScopeAuth{}, fmt.Errorf("server: parse %s/config.toml: %w", base, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return overrides, ScopeAuth{}, fmt.Errorf("server: read %s/config.toml: %w", base, err)
	}

	authPath := filepath.Join(base, "auth.toml")
	authRaw, err := os.ReadFile(authPath)
	if err != nil {
		return overrides, ScopeAuth{}, fmt.Errorf("server: read %s: %w", authPath, err)
	}
	var auth ScopeAuth
	if _, err := toml.Decode(string(authRaw), &auth); err != nil {
		return overrides, ScopeAuth{}, fmt.Errorf("server: parse %s: %w", authPath, err)
	}
	if strings.TrimSpace(auth.BearerToken) == "" {
		return overrides, ScopeAuth{}, fmt.Errorf("server: %s missing bearer_token", authPath)
	}
	return overrides, auth, nil
}

// MergedDefaults applies overrides over the global defaults. Zero values in
// overrides leave the global default in place — the only signal we have for
// "operator left this knob unset" since TOML doesn't distinguish missing from
// zero.
func MergedDefaults(global ScopeDefaults, overrides ScopeOverrides) ScopeDefaults {
	out := global
	if overrides.SyncChangeFeedPage != 0 {
		out.SyncChangeFeedPage = overrides.SyncChangeFeedPage
	}
	return out
}
