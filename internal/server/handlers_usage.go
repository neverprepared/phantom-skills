package server

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/neverprepared/phantom-skills/internal/pgstore"
)

// usageReq is the POST /usage body: a batch of telemetry events.
type usageReq struct {
	Events []usageEvent `json:"events"`
}

type usageEvent struct {
	Skill   string         `json:"skill"`
	Event   string         `json:"event"`
	Session string         `json:"session"`
	Machine string         `json:"machine"`
	TS      time.Time      `json:"ts"`
	Context map[string]any `json:"context"`
}

// handleUsage: POST /api/skills/usage — ingest a batch of usage events (deduped).
func (d *Daemon) handleUsage(w http.ResponseWriter, r *http.Request) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	var req usageReq
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		WriteErrorEnvelope(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid JSON: "+err.Error(), nil)
		return
	}
	events := make([]pgstore.UsageEvent, 0, len(req.Events))
	for _, e := range req.Events {
		events = append(events, pgstore.UsageEvent{
			SkillName: e.Skill, SessionID: e.Session, Machine: e.Machine,
			Event: e.Event, Context: e.Context, TS: e.TS,
		})
	}
	accepted, err := store.IngestUsage(r.Context(), binding.Key.Profile, events)
	if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": accepted, "received": len(req.Events)})
}
