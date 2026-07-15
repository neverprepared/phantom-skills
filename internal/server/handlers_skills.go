package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/neverprepared/phantom-skills/internal/pgstore"
)

// maxBodyBytes caps a skill registration payload. A SKILL.md body targets
// <5k tokens; 1 MiB is generous headroom for frontmatter + body + slack.
const maxBodyBytes = 1 << 20

// requireStore returns the store or writes a 503 if Postgres isn't wired.
func (d *Daemon) requireStore(w http.ResponseWriter) (*pgstore.Store, bool) {
	if d.store == nil {
		WriteErrorEnvelope(w, http.StatusServiceUnavailable, ErrCodeStorageBackend,
			"skills registry unavailable: no [postgres] dsn configured", nil)
		return nil, false
	}
	return d.store, true
}

// handleListSkills: GET /api/skills/skills?status=&tag=&after_id=&limit=
func (d *Daemon) handleListSkills(w http.ResponseWriter, r *http.Request) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	q := r.URL.Query()
	afterID, _ := strconv.ParseInt(q.Get("after_id"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	skills, err := store.ListSkills(r.Context(), binding.Key.Profile, pgstore.ListOpts{
		Status:  q.Get("status"),
		Tag:     q.Get("tag"),
		AfterID: afterID,
		Limit:   limit,
	})
	if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
		return
	}
	var next int64
	if len(skills) > 0 {
		next = skills[len(skills)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": skills, "next": next})
}

// handleGetSkill: GET /api/skills/skills/{name}
func (d *Daemon) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	name := chi.URLParam(r, "name")
	sk, ver, err := store.GetSkill(r.Context(), binding.Key.Profile, name)
	if errors.Is(err, pgstore.ErrNotFound) {
		WriteErrorEnvelope(w, http.StatusNotFound, ErrCodeNotFound, "no such skill: "+name, nil)
		return
	}
	if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skill": sk, "version": ver})
}

// registerReq is the POST/PUT body for registering a skill version.
type registerReq struct {
	Name        string         `json:"name"`
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body"`
	Author      string         `json:"author"`
	Source      string         `json:"source"`
	Origin      string         `json:"origin"`
	Status      string         `json:"status"`
	Tags        []string       `json:"tags"`
}

// handleCreateSkill: POST /api/skills/skills — register a (possibly new) skill's
// version. Idempotent on identical content.
func (d *Daemon) handleCreateSkill(w http.ResponseWriter, r *http.Request) {
	d.registerVersion(w, r, "")
}

// handleUpdateSkill: PUT /api/skills/skills/{name} — new version of an existing
// skill. The path name wins over any body name.
func (d *Daemon) handleUpdateSkill(w http.ResponseWriter, r *http.Request) {
	d.registerVersion(w, r, chi.URLParam(r, "name"))
}

func (d *Daemon) registerVersion(w http.ResponseWriter, r *http.Request, pathName string) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	var req registerReq
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		WriteErrorEnvelope(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid JSON: "+err.Error(), nil)
		return
	}
	name := req.Name
	if pathName != "" {
		name = pathName
	}
	if name == "" {
		WriteErrorEnvelope(w, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		return
	}
	ver, created, err := store.RegisterVersion(r.Context(), pgstore.RegisterInput{
		Profile:     binding.Key.Profile,
		Name:        name,
		Frontmatter: req.Frontmatter,
		Body:        req.Body,
		Author:      req.Author,
		Source:      req.Source,
		Origin:      req.Origin,
		Status:      req.Status,
		Tags:        req.Tags,
	})
	if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"name": name, "version": ver.Version, "sha": ver.SHA, "created": created})
}

// handleListVersions: GET /api/skills/skills/{name}/versions
func (d *Daemon) handleListVersions(w http.ResponseWriter, r *http.Request) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	name := chi.URLParam(r, "name")
	versions, err := store.ListVersions(r.Context(), binding.Key.Profile, name)
	if errors.Is(err, pgstore.ErrNotFound) {
		WriteErrorEnvelope(w, http.StatusNotFound, ErrCodeNotFound, "no such skill: "+name, nil)
		return
	}
	if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

// handleRetireSkill: DELETE /api/skills/skills/{name} — soft-delete.
func (d *Daemon) handleRetireSkill(w http.ResponseWriter, r *http.Request) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	name := chi.URLParam(r, "name")
	if err := store.RetireSkill(r.Context(), binding.Key.Profile, name); errors.Is(err, pgstore.ErrNotFound) {
		WriteErrorEnvelope(w, http.StatusNotFound, ErrCodeNotFound, "no such skill: "+name, nil)
		return
	} else if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "status": "retired"})
}
