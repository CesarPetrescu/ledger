package admin

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cesarpetrescu/ledger/internal/oauth"
)

func (s *Server) reviewAuthorization(w http.ResponseWriter, r *http.Request) {
	review, err := s.oauth.ReviewAuthorization(r.Context(), r.URL.Query())
	if err != nil {
		s.authorizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, review)
}

func (s *Server) decideAuthorization(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"`
	}
	if err := decodeJSON(w, r, &input, 1024); err != nil {
		writeDecodeError(w, err)
		return
	}
	if input.Action != "approve" && input.Action != "deny" {
		writeError(w, http.StatusBadRequest, "Invalid action.")
		return
	}
	destination, err := s.oauth.DecideAuthorization(r.Context(), r.URL.Query(), input.Action == "approve")
	if err != nil {
		s.authorizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect_url": destination})
}

func (s *Server) authorizationError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, oauth.ErrInvalidAuthorization) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.internalError(w, r, err)
}

func (s *Server) changeApprovalPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requests.Allow("approval-password:"+oauth.RealIP(r, s.trusted), 5, time.Minute) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again in a minute.")
		return
	}
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &input, 32<<10); err != nil {
		writeDecodeError(w, err)
		return
	}
	if len(input.CurrentPassword) > 4096 || len(input.NewPassword) > 4096 || !utf8.ValidString(input.NewPassword) || utf8.RuneCountInString(strings.TrimSpace(input.NewPassword)) < 12 {
		writeError(w, http.StatusBadRequest, "Use at least 12 characters, with a maximum of 4096 bytes.")
		return
	}
	if !s.verifyOwnerPassword(w, r, input.CurrentPassword) {
		return
	}
	if oauth.VerifyPassword(s.config.PasswordHash, input.NewPassword) {
		writeError(w, http.StatusBadRequest, "Choose an approval password different from your owner password.")
		return
	}
	hash, err := oauth.HashPassword(input.NewPassword)
	if err == nil {
		err = s.db.SetOAuthPasswordHash(r.Context(), hash)
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
