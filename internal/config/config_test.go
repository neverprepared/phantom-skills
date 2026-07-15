package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAgentMissingVars(t *testing.T) {
	t.Setenv("CL_SKILLS_API", "")
	t.Setenv("CL_SKILLS_API_TOKEN", "")
	t.Setenv("CL_WORKSPACE_PROFILE", "")
	_, err := LoadAgent()
	if err == nil {
		t.Fatal("expected missing-vars error")
	}
	for _, want := range []string{"CL_SKILLS_API", "CL_SKILLS_API_TOKEN", "CL_WORKSPACE_PROFILE"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should name %s: %v", want, err)
		}
	}
}

func TestLoadAgentDefaults(t *testing.T) {
	t.Setenv("CL_SKILLS_API", "http://d:9997")
	t.Setenv("CL_SKILLS_API_TOKEN", "sk-x")
	t.Setenv("CL_WORKSPACE_PROFILE", "personal")
	t.Setenv("CL_SKILLS_SET", "")
	t.Setenv("CL_SKILLS_DIR", "")
	a, err := LoadAgent()
	if err != nil {
		t.Fatal(err)
	}
	if a.Skillset != "default" {
		t.Fatalf("skillset default = %q", a.Skillset)
	}
	if !strings.HasSuffix(a.SkillsDir, filepath.Join(".claude", "skills")) {
		t.Fatalf("skills dir default = %q", a.SkillsDir)
	}
}

func TestStateDirLayout(t *testing.T) {
	t.Setenv("PSKILLS_STATE_DIR", "/tmp/pskills-state")
	a := &Agent{Profile: "personal", Skillset: "default"}
	want := filepath.Join("/tmp/pskills-state", "personal", "default")
	if got := a.StateDir(); got != want {
		t.Fatalf("StateDir = %q want %q", got, want)
	}
}
