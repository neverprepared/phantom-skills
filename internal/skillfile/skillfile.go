// Package skillfile is the shared SKILL.md codec: render a skill's frontmatter
// + body to the on-disk format Claude Code loads, parse it back, and compute
// the canonical content SHA that identifies a version. It is a leaf package
// (no daemon or client deps) so both sides agree on the format and hash.
//
// Managed skill dirs carry an `x-phantom-skills` frontmatter marker recording
// the skill's origin, status, and registry SHA. The syncer only ever writes or
// removes dirs bearing this marker — hand-authored local skills are invisible
// to it.
package skillfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// MarkerKey is the frontmatter field under which the ownership marker lives.
const MarkerKey = "x-phantom-skills"

// Marker annotates a synced skill dir as managed by phantom-skills.
type Marker struct {
	Origin string `yaml:"origin" json:"origin"`
	Status string `yaml:"status" json:"status"`
	SHA    string `yaml:"sha" json:"sha"` // the registry (daemon) content SHA we wrote
}

// Render produces the bytes of a SKILL.md: YAML frontmatter (frontmatter plus
// the injected marker) delimited by `---`, then a blank line, then the body.
// The marker is added under MarkerKey; callers pass frontmatter WITHOUT it.
func Render(frontmatter map[string]any, body string, marker Marker) ([]byte, error) {
	fm := make(map[string]any, len(frontmatter)+1)
	for k, v := range frontmatter {
		if k == MarkerKey {
			continue // never let a caller smuggle in a marker
		}
		fm[k] = v
	}
	fm[MarkerKey] = map[string]any{"origin": marker.Origin, "status": marker.Status, "sha": marker.SHA}

	yml, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("skillfile: marshal frontmatter: %w", err)
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(yml)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n")
	return b.Bytes(), nil
}

// Parse splits a SKILL.md into its frontmatter map and body. A file without a
// leading `---` frontmatter block parses as empty frontmatter + whole content
// as body (matching Claude Code's tolerance).
func Parse(data []byte) (frontmatter map[string]any, body string, err error) {
	s := string(data)
	if !strings.HasPrefix(s, "---") {
		return map[string]any{}, s, nil
	}
	// Strip the opening delimiter line, then split on the closing `---`.
	rest := strings.TrimPrefix(s, "---")
	rest = strings.TrimPrefix(rest, "\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return map[string]any{}, s, nil
	}
	fmText := rest[:idx]
	bodyText := rest[idx+len("\n---"):]
	bodyText = strings.TrimPrefix(bodyText, "\n")
	bodyText = strings.TrimPrefix(bodyText, "\n")

	fm := map[string]any{}
	if strings.TrimSpace(fmText) != "" {
		if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
			return nil, "", fmt.Errorf("skillfile: parse frontmatter: %w", err)
		}
	}
	return fm, bodyText, nil
}

// MarkerOf extracts the ownership marker from a parsed frontmatter map. The
// bool reports whether a marker was present (i.e. the dir is phantom-managed).
func MarkerOf(frontmatter map[string]any) (Marker, bool) {
	raw, ok := frontmatter[MarkerKey]
	if !ok {
		return Marker{}, false
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return Marker{}, false
	}
	return Marker{
		Origin: asString(m["origin"]),
		Status: asString(m["status"]),
		SHA:    asString(m["sha"]),
	}, true
}

// CanonicalSHA computes the content-addressed identity of a skill version:
// sha256 over the canonical JSON of the frontmatter (encoding/json sorts map
// keys deterministically) joined with the body. The marker is excluded — it's
// a local annotation, not part of the skill's identity.
func CanonicalSHA(frontmatter map[string]any, body string) string {
	clean := make(map[string]any, len(frontmatter))
	for k, v := range frontmatter {
		if k == MarkerKey {
			continue
		}
		clean[k] = v
	}
	fmJSON, _ := json.Marshal(clean)
	h := sha256.New()
	h.Write(fmJSON)
	h.Write([]byte{0})
	h.Write([]byte(body))
	return hex.EncodeToString(h.Sum(nil))
}

// Slugify reduces a skill name to a filesystem-safe directory slug. Names are
// expected to already be lowercase-hyphen; this is defensive normalization used
// by both the daemon (identity) and the syncer (locating dirs).
func Slugify(name string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
