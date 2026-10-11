package admin

import (
	"net/http"
	"strings"
	"time"
)

type sessionView struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Method     string    `json:"method"`
	Remember   bool      `json:"remember"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

// listSessions lists the signed-in devices.
func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.db.ListAdminSessions(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	current := sessionFrom(r).PublicID
	views := make([]sessionView, len(sessions))
	for i, session := range sessions {
		views[i] = sessionView{ID: session.PublicID, Label: session.Label, Method: session.Method, Remember: session.Remember,
			CreatedAt: session.CreatedAt, LastSeenAt: session.LastSeenAt, ExpiresAt: session.ExpiresAt, Current: session.PublicID == current}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": views})
}

// revokeSession signs one device out; its next request is refused. Revoking the current session
// is signing out, so its cookie is cleared too.
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	deleted, err := s.db.DeleteAdminSessionByPublicID(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if id == sessionFrom(r).PublicID {
		clearSessionCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	revoked, err := s.db.RevokeOtherAdminSessions(r.Context(), sessionFrom(r).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": revoked})
}

// sessionLabel names a device from its User-Agent, such as "Chrome on Windows". The Android app
// sends "LedgerAndroid/<version> (Android <release>; <model>)".
func sessionLabel(userAgent string) string {
	if strings.HasPrefix(userAgent, "LedgerAndroid/") {
		return "Ledger app on Android"
	}
	// Order matters: Edge and Opera also say Chrome, Chrome also says Safari, iPhones say
	// "like Mac OS X", and Android says Linux.
	first := func(pairs ...string) string {
		for i := 0; i < len(pairs); i += 2 {
			for _, token := range strings.Split(pairs[i], "|") {
				if strings.Contains(userAgent, token) {
					return pairs[i+1]
				}
			}
		}
		return ""
	}
	browser := first("Edg/|EdgA/|EdgiOS/", "Edge", "OPR/|Opera", "Opera", "SamsungBrowser/", "Samsung Internet",
		"Firefox/|FxiOS/", "Firefox", "Chrome/|CriOS/|Chromium/", "Chrome", "Safari/", "Safari")
	system := first("iPhone", "iPhone", "iPad", "iPad", "Android", "Android", "Windows", "Windows", "CrOS", "ChromeOS",
		"Macintosh|Mac OS X", "macOS", "Linux", "Linux")
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	case system != "":
		return "Browser on " + system
	}
	return "Unknown device"
}
