package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Server) listProjectRepos(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := store.ValidateProjectSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	repos, err := s.db.ListRepos(r.Context(), slug)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

func (s *Server) linkProjectRepo(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL    string `json:"url"`
		Branch string `json:"branch"`
		Path   string `json:"path"`
		Role   string `json:"role"`
		Note   string `json:"note"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	repo, err := s.db.LinkRepo(r.Context(), store.NewRepo{ProjectSlug: r.PathValue("slug"), URL: input.URL, Branch: input.Branch, Path: input.Path, Role: input.Role, Note: input.Note,
		Source: writeSource, ClientID: clientIdentifier(sessionFrom(r))})
	s.repoResult(w, r, http.StatusCreated, repo, err)
}

func (s *Server) unlinkProjectRepo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "repository id must be a positive integer")
		return
	}
	repo, err := s.db.UnlinkRepo(r.Context(), id, "", true)
	s.repoResult(w, r, http.StatusOK, repo, err)
}

func (s *Server) repoResult(w http.ResponseWriter, r *http.Request, status int, repo store.ProjectRepo, err error) {
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		writeJSON(w, status, repo)
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, "project or repository not found")
	case errors.Is(err, store.ErrRepoExists), errors.Is(err, store.ErrRepoLimit):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &pgErr) || r.Context().Err() != nil:
		s.internalError(w, r, err)
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// The GitHub sync token: the console can set, check, and remove it, never read it back.

func (s *Server) gitHubSyncStatus(w http.ResponseWriter, r *http.Request) {
	if s.github == nil {
		writeError(w, http.StatusServiceUnavailable, "GitHub sync is not available on this server")
		return
	}
	status, err := s.github.Status(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) putGitHubSync(w http.ResponseWriter, r *http.Request) {
	if s.github == nil {
		writeError(w, http.StatusServiceUnavailable, "GitHub sync is not available on this server")
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	status, err := s.github.SetToken(r.Context(), input.Token)
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, status)
	case errors.As(err, &pgErr) || r.Context().Err() != nil:
		s.internalError(w, r, err)
	default:
		// The token is never echoed back, only why GitHub or Ledger refused it.
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func (s *Server) deleteGitHubSync(w http.ResponseWriter, r *http.Request) {
	if s.github == nil {
		writeError(w, http.StatusServiceUnavailable, "GitHub sync is not available on this server")
		return
	}
	if err := s.github.Clear(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
