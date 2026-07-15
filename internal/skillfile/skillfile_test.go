package skillfile

import (
	"strings"
	"testing"
)

func TestRenderParseRoundTrip(t *testing.T) {
	fm := map[string]any{"name": "demo", "description": "Do a demo. Use when demoing."}
	body := "# Demo\n1. step one\n2. step two"
	data, err := Render(fm, body, Marker{Origin: "authored", Status: "promoted", SHA: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "---\n") {
		t.Fatalf("frontmatter must start at byte 0:\n%s", data)
	}

	gotFM, gotBody, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if gotFM["name"] != "demo" || gotFM["description"] != fm["description"] {
		t.Fatalf("frontmatter did not round-trip: %v", gotFM)
	}
	if strings.TrimSpace(gotBody) != strings.TrimSpace(body) {
		t.Fatalf("body did not round-trip:\n%q\nvs\n%q", gotBody, body)
	}
	m, ok := MarkerOf(gotFM)
	if !ok || m.SHA != "abc123" || m.Status != "promoted" || m.Origin != "authored" {
		t.Fatalf("marker not round-tripped: %+v ok=%v", m, ok)
	}
}

func TestParseUnmanagedHasNoMarker(t *testing.T) {
	data := "---\nname: hand-authored\ndescription: mine\n---\n\nbody here\n"
	fm, _, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := MarkerOf(fm); ok {
		t.Fatal("hand-authored file should have no marker")
	}
}

func TestParseNoFrontmatter(t *testing.T) {
	fm, body, err := Parse([]byte("just a body, no frontmatter"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fm) != 0 || !strings.Contains(body, "just a body") {
		t.Fatalf("unexpected parse: fm=%v body=%q", fm, body)
	}
}

func TestCanonicalSHAStableAndExcludesMarker(t *testing.T) {
	fm := map[string]any{"name": "x", "description": "d"}
	body := "b"
	a := CanonicalSHA(fm, body)
	// Re-order / re-alloc the map: sha must be identical (JSON sorts keys).
	fm2 := map[string]any{"description": "d", "name": "x"}
	if b := CanonicalSHA(fm2, body); a != b {
		t.Fatalf("sha not stable across map order: %s vs %s", a, b)
	}
	// Adding a marker must NOT change the sha.
	fm3 := map[string]any{"name": "x", "description": "d", MarkerKey: map[string]any{"sha": "zzz"}}
	if c := CanonicalSHA(fm3, body); a != c {
		t.Fatalf("marker changed the canonical sha: %s vs %s", a, c)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"git-worktree-flow": "git-worktree-flow",
		"Git Worktree Flow": "git-worktree-flow",
		"a//b__c":           "a-b-c",
		"--trim--":          "trim",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Fatalf("Slugify(%q) = %q want %q", in, got, want)
		}
	}
}
