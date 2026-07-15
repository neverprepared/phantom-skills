package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// ScopeKey identifies one authenticated scope: a (profile, skillset) pair a
// bearer token is bound to. The centralized shared registry usually runs a
// single scope (personal/default), but the binding model is preserved for
// multi-tenant parity with phantom-brain.
type ScopeKey struct {
	Profile  string
	Skillset string
}

// String formats as profile/skillset for log fields and operator output.
func (k ScopeKey) String() string { return k.Profile + "/" + k.Skillset }

// ScopeBinding is everything the daemon knows about one authenticated scope:
// its identity, the bearer token operators issued, and the merged defaults.
type ScopeBinding struct {
	Key      ScopeKey
	Auth     ScopeAuth
	Defaults ScopeDefaults
}

// Registry is the live view of every scope the daemon serves. The auth
// middleware looks up bearer tokens here; SIGHUP triggers a re-scan.
//
// Concurrency: every method takes an internal RWMutex. The auth path is
// read-heavy so RLock is the common case; SIGHUP takes a write lock briefly to
// swap the maps.
type Registry struct {
	mu      sync.RWMutex
	byToken map[string]ScopeBinding
	byScope map[ScopeKey]ScopeBinding
}

// NewRegistry returns an empty registry. Use Load to populate.
func NewRegistry() *Registry {
	return &Registry{
		byToken: map[string]ScopeBinding{},
		byScope: map[ScopeKey]ScopeBinding{},
	}
}

// LookupByToken returns the binding for a bearer token. The bool reports
// presence so the auth middleware can distinguish "token missing" from "token
// belonged to a scope removed via SIGHUP" (same 401 outcome, logged differently).
func (r *Registry) LookupByToken(token string) (ScopeBinding, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.byToken[token]
	return b, ok
}

// LookupByScope returns the binding for a (profile, skillset).
func (r *Registry) LookupByScope(k ScopeKey) (ScopeBinding, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.byScope[k]
	return b, ok
}

// Scopes returns every binding sorted by ScopeKey for stable iteration.
func (r *Registry) Scopes() []ScopeBinding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ScopeBinding, 0, len(r.byScope))
	for _, b := range r.byScope {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.Profile != out[j].Key.Profile {
			return out[i].Key.Profile < out[j].Key.Profile
		}
		return out[i].Key.Skillset < out[j].Key.Skillset
	})
	return out
}

// Load walks {configDir}/profiles/*/auth.toml, parses each scope's config +
// auth, merges overrides over the global defaults, and atomically swaps the
// in-memory maps. Returns the number of scopes loaded. Any single scope's
// parse error stops the whole load and leaves the previous registry intact — a
// typo'd auth.toml is operator error, and booting with a partial scope set
// would drop tokens silently.
func (r *Registry) Load(configDir string, defaults ScopeDefaults) (int, error) {
	if configDir == "" {
		return 0, errors.New("server: Registry.Load requires a config dir")
	}
	newByToken := map[string]ScopeBinding{}
	newByScope := map[ScopeKey]ScopeBinding{}

	profilesRoot := filepath.Join(configDir, "profiles")
	scopes, err := os.ReadDir(profilesRoot)
	if errors.Is(err, os.ErrNotExist) {
		// No scopes configured yet — an empty but valid registry. The daemon
		// serves /health and returns 401 on every authed endpoint.
		r.swap(newByToken, newByScope)
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("server: read %s: %w", profilesRoot, err)
	}

	for _, se := range scopes {
		if !se.IsDir() {
			continue
		}
		profile := se.Name()
		overrides, auth, err := LoadScopeFiles(configDir, profile)
		if err != nil {
			return 0, err
		}
		skillset := overrides.Skillset
		if skillset == "" {
			skillset = "default"
		}
		key := ScopeKey{Profile: profile, Skillset: skillset}
		if _, dup := newByToken[auth.BearerToken]; dup {
			return 0, fmt.Errorf("server: duplicate bearer_token across scopes; conflict at %s", key)
		}
		newByToken[auth.BearerToken] = ScopeBinding{
			Key:      key,
			Auth:     auth,
			Defaults: MergedDefaults(defaults, overrides),
		}
		newByScope[key] = newByToken[auth.BearerToken]
	}
	r.swap(newByToken, newByScope)
	return len(newByScope), nil
}

func (r *Registry) swap(byToken map[string]ScopeBinding, byScope map[ScopeKey]ScopeBinding) {
	r.mu.Lock()
	r.byToken = byToken
	r.byScope = byScope
	r.mu.Unlock()
}
