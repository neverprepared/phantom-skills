package server

import (
	"encoding/json"
	"net/http"
)

// Error envelope codes used across the API. STRING constants (not int enums)
// because operators and clients pattern-match on them in logs and code.
const (
	ErrCodeInvalidToken    = "INVALID_TOKEN"
	ErrCodeScopeNotFound   = "SCOPE_NOT_FOUND"
	ErrCodeNotFound        = "NOT_FOUND"
	ErrCodeBadRequest      = "BAD_REQUEST"
	ErrCodeConflict        = "CONFLICT"
	ErrCodePayloadTooLarge = "PAYLOAD_TOO_LARGE"
	ErrCodeStorageBackend  = "STORAGE_BACKEND_ERROR"
	ErrCodeInternal        = "INTERNAL_ERROR"
)

// ErrorEnvelope is the on-the-wire shape every error response uses.
type ErrorEnvelope struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details,omitempty"`
	} `json:"error"`
}

// WriteErrorEnvelope writes a JSON error response with the given status, code,
// and message. Details is optional context. Any encoding failure falls back to
// http.Error so the client at least sees the status code.
func WriteErrorEnvelope(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	env := ErrorEnvelope{}
	env.Error.Code = code
	env.Error.Message = message
	env.Error.Details = details
	body, err := json.Marshal(env)
	if err != nil {
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeJSON writes a success JSON body. Encoding failures degrade to a 500
// error envelope.
func writeJSON(w http.ResponseWriter, status int, body any) {
	buf, err := json.Marshal(body)
	if err != nil {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, "encode response: "+err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}
