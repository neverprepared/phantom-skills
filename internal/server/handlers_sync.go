package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// syncSkill is a promoted skill materialized into the agent's skills dir.
type syncSkill struct {
	Name        string         `json:"name"`
	Slug        string         `json:"slug"`
	Status      string         `json:"status"`
	Origin      string         `json:"origin"`
	SHA         string         `json:"sha"`
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body"`
}

// handleSync: GET /api/skills/sync?since=<cursor> — the agent change-feed.
// Returns promoted skills changed since the cursor, the names of skills
// retired since the cursor (deletes), and a new cursor. First call (empty
// cursor) bootstraps a full pull.
func (d *Daemon) handleSync(w http.ResponseWriter, r *http.Request) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	sinceTS, sinceID := decodeCursor(r.URL.Query().Get("since"))
	page := d.cfg.Defaults.SyncChangeFeedPage

	rows, err := store.SyncFeed(r.Context(), binding.Key.Profile, sinceTS, sinceID, page)
	if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
		return
	}

	skills := make([]syncSkill, 0, len(rows))
	deletes := make([]string, 0)
	cursor := encodeCursor(sinceTS, sinceID)
	for _, row := range rows {
		switch row.Status {
		case "promoted":
			skills = append(skills, syncSkill{
				Name: row.Name, Slug: row.Slug, Status: row.Status, Origin: row.Origin,
				SHA: row.SHA, Frontmatter: row.Frontmatter, Body: row.Body,
			})
		case "retired":
			deletes = append(deletes, row.Name)
		}
		cursor = encodeCursor(row.UpdatedAt, row.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"skills":  skills,
		"deletes": deletes,
		"cursor":  cursor,
	})
}

// encodeCursor renders a (updated_at, id) keyset position as "<unixnano>.<id>".
func encodeCursor(ts time.Time, id int64) string {
	return strconv.FormatInt(ts.UnixNano(), 10) + "." + strconv.FormatInt(id, 10)
}

// decodeCursor parses a cursor back to (updated_at, id). An empty or malformed
// cursor starts from the beginning (epoch, 0) so the agent bootstraps a full
// pull.
func decodeCursor(s string) (time.Time, int64) {
	if s == "" {
		return time.Unix(0, 0).UTC(), 0
	}
	dot := strings.LastIndex(s, ".")
	if dot < 0 {
		return time.Unix(0, 0).UTC(), 0
	}
	nanos, err1 := strconv.ParseInt(s[:dot], 10, 64)
	id, err2 := strconv.ParseInt(s[dot+1:], 10, 64)
	if err1 != nil || err2 != nil {
		return time.Unix(0, 0).UTC(), 0
	}
	return time.Unix(0, nanos).UTC(), id
}
