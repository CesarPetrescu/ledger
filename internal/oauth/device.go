package oauth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const DeviceGrant = "urn:ietf:params:oauth:grant-type:device_code"

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.requests.Allow("device:"+RealIP(r, s.trusted), 5, time.Minute) {
		oauthError(w, 429, "temporarily_unavailable", "try again later")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request", "invalid form")
		return
	}
	client, err := s.db.GetClient(r.Context(), r.PostForm.Get("client_id"))
	if err != nil || client.Kind != "device" {
		oauthError(w, 400, "invalid_client", "register a device client first")
		return
	}
	scopes, ok := ParseScopes(r.PostForm.Get("scope"))
	if !ok {
		oauthError(w, 400, "invalid_scope", "unsupported scope")
		return
	}
	if resource := r.PostForm.Get("resource"); resource != "" && resource != s.config.PublicURL+"/mcp" {
		oauthError(w, 400, "invalid_target", "invalid resource")
		return
	}
	raw, code, err := s.db.CreateDevice(r.Context(), client.ClientID, scopes)
	if err != nil {
		http.Error(w, "internal server error", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"device_code": raw, "user_code": code, "verification_uri": s.config.PublicURL + "/admin/connect", "expires_in": 600, "interval": 5})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.requests.Allow("revoke:"+RealIP(r, s.trusted), 20, time.Minute) {
		oauthError(w, 429, "temporarily_unavailable", "try again later")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil || r.PostForm.Get("client_id") == "" || strings.TrimSpace(r.PostForm.Get("token")) == "" {
		oauthError(w, 400, "invalid_request", "client_id and token are required")
		return
	}
	if err := s.db.RevokeToken(r.Context(), r.PostForm.Get("token"), r.PostForm.Get("client_id")); err != nil {
		http.Error(w, "internal server error", 500)
		return
	}
	w.WriteHeader(http.StatusOK)
}
