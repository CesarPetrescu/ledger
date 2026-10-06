package admin

import (
	"net/http"
	"strconv"

	"github.com/cesarpetrescu/ledger/internal/store"
)

// API keys let a server such as Adastrion Core use /api/v1. The secret is returned once, at creation.

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.db.ListAPIKeys(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	key, secret, err := s.db.CreateAPIKey(r.Context(), input.Name, []string{store.ScopeResearchDispatch})
	if err != nil {
		if store.IsCheckViolation(err) {
			writeError(w, http.StatusBadRequest, "the key name is not allowed")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "secret": secret})
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return
	}
	key, err := s.db.RevokeAPIKey(r.Context(), id)
	if store.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "API key not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, key)
}
