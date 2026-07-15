// Package config loads the agent-side contract from CL_SKILLS_* environment
// variables. Claude Code spawns `pskillctl client mcp` with these set; the
// loader validates them and resolves the skills directory. Unlike
// phantom-brain there is no brain lifecycle here — the agent is a thin proxy to
// the daemon plus a local sync target.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Agent is the resolved agent contract for one MCP session.
type Agent struct {
	API       string // CL_SKILLS_API         — daemon URL
	Token     string // CL_SKILLS_API_TOKEN   — bearer; daemon resolves to (profile, skillset)
	Profile   string // CL_WORKSPACE_PROFILE  — must match the token's profile
	Skillset  string // CL_SKILLS_SET         — skillset name (default "default")
	SkillsDir string // CL_SKILLS_DIR         — where SKILL.md dirs are synced (default ~/.claude/skills)
}

// LoadAgent reads and validates the agent contract. Missing required vars are
// reported together as a single aggregated error so operators fix the MCP
// config in one pass rather than one var per restart.
func LoadAgent() (*Agent, error) {
	a := &Agent{
		API:       strings.TrimSpace(os.Getenv("CL_SKILLS_API")),
		Token:     strings.TrimSpace(os.Getenv("CL_SKILLS_API_TOKEN")),
		Profile:   strings.TrimSpace(os.Getenv("CL_WORKSPACE_PROFILE")),
		Skillset:  strings.TrimSpace(os.Getenv("CL_SKILLS_SET")),
		SkillsDir: strings.TrimSpace(os.Getenv("CL_SKILLS_DIR")),
	}

	var missing []string
	if a.API == "" {
		missing = append(missing, "CL_SKILLS_API")
	}
	if a.Token == "" {
		missing = append(missing, "CL_SKILLS_API_TOKEN")
	}
	if a.Profile == "" {
		missing = append(missing, "CL_WORKSPACE_PROFILE")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("config: missing required env vars: %s", strings.Join(missing, ", "))
	}

	if a.Skillset == "" {
		a.Skillset = "default"
	}
	if a.SkillsDir == "" {
		a.SkillsDir = DefaultSkillsDir()
	}
	a.SkillsDir = expandHome(a.SkillsDir)
	return a, nil
}

// StateDir is where the agent keeps per-scope local state (the write-ahead
// queue, the sync cursor). Layout: {PSKILLS_STATE_DIR or ~/.config/phantom-skills}/<profile>/<skillset>.
func (a *Agent) StateDir() string {
	root := strings.TrimSpace(os.Getenv("PSKILLS_STATE_DIR"))
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, ".config", "phantom-skills")
		} else {
			root = filepath.Join(".config", "phantom-skills")
		}
	}
	return filepath.Join(root, a.Profile, a.Skillset)
}

// DefaultSkillsDir is ~/.claude/skills — where Claude Code discovers personal
// skills.
func DefaultSkillsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".claude", "skills")
	}
	return filepath.Join(home, ".claude", "skills")
}

// expandHome turns a leading ~/ into $HOME. Claude Code's MCP env block doesn't
// expand the shell tilde.
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~/"))
}
