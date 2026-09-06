package admin

import (
	"errors"
	"net/http"
	"time"

	"github.com/cesarpetrescu/ledger/internal/oauth"
	"github.com/cesarpetrescu/ledger/internal/store"
)

func (s *Server) deviceRequest(w http.ResponseWriter, r *http.Request) {
	if !s.requests.Allow("device-code:"+oauth.RealIP(r, s.trusted), 10, time.Minute) {
		writeError(w, 429, "Too many code attempts. Try again in a minute.")
		return
	}
	var input struct {
		UserCode string `json:"user_code"`
		Action   string `json:"action"`
	}
	if err := decodeJSON(w, r, &input, 2048); err != nil {
		writeError(w, 400, "Invalid request.")
		return
	}
	code := store.NormalizeUserCode(input.UserCode)
	if len(code) != 8 {
		writeError(w, 400, "Enter the eight-character code from your terminal.")
		return
	}
	if input.Action == "lookup" {
		d, err := s.db.LookupDevice(r.Context(), code)
		if errors.Is(err, store.ErrInvalidGrant) {
			writeError(w, 404, "Code is invalid, expired, or already handled.")
			return
		}
		if err != nil {
			writeError(w, 500, "Could not load request.")
			return
		}
		writeJSON(w, 200, d)
		return
	}
	if input.Action != "approve" && input.Action != "deny" {
		writeError(w, 400, "Invalid action.")
		return
	}
	err := s.db.DecideDevice(r.Context(), code, input.Action == "approve")
	if errors.Is(err, store.ErrInvalidGrant) {
		writeError(w, 409, "Code is invalid, expired, or already handled.")
		return
	}
	if err != nil {
		writeError(w, 500, "Could not save decision.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
