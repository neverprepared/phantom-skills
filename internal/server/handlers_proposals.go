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

// handleListProposals: GET /api/skills/proposals?status=&kind=
func (d *Daemon) handleListProposals(w http.ResponseWriter, r *http.Request) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	q := r.URL.Query()
	props, err := store.ListProposals(r.Context(), binding.Key.Profile, q.Get("status"), q.Get("kind"))
	if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": props})
}

// handleGetProposal: GET /api/skills/proposals/{id}
func (d *Daemon) handleGetProposal(w http.ResponseWriter, r *http.Request) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	id, ok := proposalID(w, r)
	if !ok {
		return
	}
	p, err := store.GetProposal(r.Context(), binding.Key.Profile, id)
	if errors.Is(err, pgstore.ErrNotFound) {
		WriteErrorEnvelope(w, http.StatusNotFound, ErrCodeNotFound, "no such proposal", nil)
		return
	}
	if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// createProposalReq is the agent-submitted create-candidate body.
type createProposalReq struct {
	Kind                string         `json:"kind"`
	SkillName           string         `json:"skill_name"`
	ProposedFrontmatter map[string]any `json:"proposed_frontmatter"`
	ProposedBody        string         `json:"proposed_body"`
	Diff                string         `json:"diff"`
	Rationale           string         `json:"rationale"`
	SessionID           string         `json:"session_id"`
}

// handleCreateProposal: POST /api/skills/proposals — an agent submits a
// create-candidate. Pipeline-generated proposals are inserted server-side.
func (d *Daemon) handleCreateProposal(w http.ResponseWriter, r *http.Request) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	var req createProposalReq
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		WriteErrorEnvelope(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid JSON: "+err.Error(), nil)
		return
	}
	if req.Kind == "" {
		req.Kind = "create"
	}
	if req.SkillName == "" {
		WriteErrorEnvelope(w, http.StatusBadRequest, ErrCodeBadRequest, "skill_name is required", nil)
		return
	}
	createdBy := "agent"
	if req.SessionID != "" {
		createdBy = "agent:" + req.SessionID
	}
	id, err := store.CreateProposal(r.Context(), pgstore.ProposalInput{
		Profile:             binding.Key.Profile,
		Kind:                req.Kind,
		SkillName:           req.SkillName,
		ProposedFrontmatter: req.ProposedFrontmatter,
		ProposedBody:        req.ProposedBody,
		Diff:                req.Diff,
		Rationale:           req.Rationale,
		CreatedBy:           createdBy,
	})
	if err != nil {
		WriteErrorEnvelope(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "pending"})
}

// decideReq is the approve/reject body.
type decideReq struct {
	By     string `json:"by"`
	Reason string `json:"reason"`
}

// handleApproveProposal: POST /api/skills/proposals/{id}/approve
func (d *Daemon) handleApproveProposal(w http.ResponseWriter, r *http.Request) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	id, ok := proposalID(w, r)
	if !ok {
		return
	}
	by := decideBy(r)
	res, err := store.ApproveProposal(r.Context(), binding.Key.Profile, id, by)
	if errors.Is(err, pgstore.ErrNotFound) {
		WriteErrorEnvelope(w, http.StatusNotFound, ErrCodeNotFound, "no such pending proposal", nil)
		return
	}
	if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
		return
	}
	if d.brain != nil {
		d.brain.RecordDecision(r.Context(), "approved", res.Kind, res.SkillName, by)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "approved", "applied": res})
}

// handleRejectProposal: POST /api/skills/proposals/{id}/reject
func (d *Daemon) handleRejectProposal(w http.ResponseWriter, r *http.Request) {
	store, ok := d.requireStore(w)
	if !ok {
		return
	}
	binding, _ := BindingFromContext(r.Context())
	id, ok := proposalID(w, r)
	if !ok {
		return
	}
	var body decideReq
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
	by := body.By
	if by == "" {
		by = "operator"
	}
	if err := store.RejectProposal(r.Context(), binding.Key.Profile, id, by, body.Reason); errors.Is(err, pgstore.ErrNotFound) {
		WriteErrorEnvelope(w, http.StatusNotFound, ErrCodeNotFound, "no such pending proposal", nil)
		return
	} else if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "rejected"})
}

func proposalID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		WriteErrorEnvelope(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid proposal id", nil)
		return 0, false
	}
	return id, true
}

func decideBy(r *http.Request) string {
	var body decideReq
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
	if body.By != "" {
		return body.By
	}
	return "operator"
}
