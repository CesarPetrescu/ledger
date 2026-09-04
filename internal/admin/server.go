// Package admin serves the operator web API behind /admin/api.
//
// Authentication is a separate Argon2id password. Sessions are opaque random
// identifiers stored hashed in PostgreSQL and carried in a hardened cookie.
// Every endpoint except login requires a live session; state-changing
// endpoints additionally require the exact public Origin and the per-session
// CSRF token. Failures never reveal internals.
package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cesarpetrescu/ledger/internal/oauth"
	"github.com/cesarpetrescu/ledger/internal/retrieval"
	"github.com/cesarpetrescu/ledger/internal/store"
)

const (
	sessionCookie  = "ledger_admin_session"
	csrfHeader     = "X-CSRF-Token"
	writeSource    = "ledger-admin"
	maxBodyBytes   = 64 << 10
	maxLoginBytes  = 8 << 10
	defaultEntries = 100
	maxEntries     = 500
	maxSearchRunes = 1000
	defaultClients = 50
	maxClients     = 100
)

type Config struct {
	PublicURL         string
	PasswordHash      string
	InternalProxyCIDR string
	IndexURL          string
}

type Server struct {
	config   Config
	origin   string
	db       *store.DB
	index    *retrieval.Client
	mux      *http.ServeMux
	trusted  *netip.Prefix
	requests *oauth.RateLimiter
	failures *oauth.RateLimiter
	events   *eventStream
}

type sessionKey struct{}

func NewServer(config Config, db *store.DB) *Server {
	if !strings.HasPrefix(config.PasswordHash, "$argon2id$") {
		panic("LEDGER_ADMIN_PASSWORD_HASH must be an Argon2id PHC string")
	}
	s := &Server{config: config, origin: publicOrigin(config.PublicURL), db: db, index: retrieval.NewClient(config.IndexURL), mux: http.NewServeMux(), requests: oauth.NewRateLimiter(), failures: oauth.NewRateLimiter(), events: newEventStream(db)}
	if config.InternalProxyCIDR != "" {
		prefix, err := netip.ParsePrefix(config.InternalProxyCIDR)
		if err != nil {
			panic("invalid LEDGER_INTERNAL_PROXY_CIDR")
		}
		s.trusted = &prefix
	}
	s.mux.HandleFunc("GET /admin/api/session", s.session)
	s.mux.HandleFunc("POST /admin/api/logout", s.logout)
	s.mux.HandleFunc("GET /admin/api/overview", s.overview)
	s.mux.HandleFunc("GET /admin/api/projects", s.listProjects)
	s.mux.HandleFunc("GET /admin/api/projects/{slug}", s.getProject)
	s.mux.HandleFunc("PUT /admin/api/projects/{slug}", s.putProject)
	s.mux.HandleFunc("POST /admin/api/projects/{slug}/entries", s.appendEntry)
	s.mux.HandleFunc("POST /admin/api/search", s.search)
	s.mux.HandleFunc("GET /admin/api/oauth/clients", s.listClients)
	s.mux.HandleFunc("POST /admin/api/oauth/revoke", s.revokeClient)
	s.mux.HandleFunc("GET /admin/api/events", func(w http.ResponseWriter, r *http.Request) { s.events.serve(s.origin, w, r) })
	return s
}

// RunEvents streams committed database changes to connected operator consoles.
func (s *Server) RunEvents(ctx context.Context) error { return s.events.run(ctx) }

// publicOrigin reduces LEDGER_PUBLIC_URL to the exact browser Origin value.
func publicOrigin(publicURL string) string {
	u, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		panic("LEDGER_PUBLIC_URL must be an absolute URL")
	}
	scheme := strings.ToLower(u.Scheme)
	hostname := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	return scheme + "://" + host
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	header.Set("Content-Type", "application/json; charset=utf-8")
	if r.URL.Path == "/admin/api/login" {
		if r.Method != http.MethodPost {
			header.Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.login(w, r)
		return
	}
	session, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Header.Get("Origin") != s.origin {
			writeError(w, http.StatusForbidden, "origin not allowed")
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(session.CSRFToken)) != 1 {
			writeError(w, http.StatusForbidden, "invalid csrf token")
			return
		}
	}
	s.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, session)))
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (store.AdminSession, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || !plausibleToken(cookie.Value) {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return store.AdminSession{}, false
	}
	session, err := s.db.LookupAdminSession(r.Context(), cookie.Value)
	if err != nil {
		if !store.IsNotFound(err) {
			s.internalError(w, r, err)
			return store.AdminSession{}, false
		}
		clearSessionCookie(w)
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return store.AdminSession{}, false
	}
	return session, true
}

// plausibleToken filters obviously forged cookies before any database work.
func plausibleToken(value string) bool {
	if len(value) != 43 {
		return false
	}
	for _, c := range value {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.origin {
		writeError(w, http.StatusForbidden, "origin not allowed")
		return
	}
	ip := oauth.RealIP(r, s.trusted)
	if !s.requests.Allow("login:"+ip, 20, time.Minute) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input, maxLoginBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	failureKey := "login-failure:" + ip
	if s.failures.Blocked(failureKey, 4, 15*time.Minute) {
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "too many failed logins")
		return
	}
	if !oauth.VerifyPassword(s.config.PasswordHash, input.Password) {
		if !s.failures.Allow(failureKey, 4, 15*time.Minute) {
			w.Header().Set("Retry-After", "900")
			writeError(w, http.StatusTooManyRequests, "too many failed logins")
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	ctx := r.Context()
	if previous, err := r.Cookie(sessionCookie); err == nil && plausibleToken(previous.Value) {
		if err := s.db.DeleteAdminSession(ctx, previous.Value); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if err := s.db.ExpireAdminSessions(ctx); err != nil {
		s.internalError(w, r, err)
		return
	}
	session, err := s.db.CreateAdminSession(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: session.ID, Path: "/admin", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, Expires: session.ExpiresAt, MaxAge: int(time.Until(session.ExpiresAt).Seconds())})
	writeJSON(w, http.StatusOK, map[string]any{"csrf_token": session.CSRFToken, "expires_at": session.ExpiresAt})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/admin", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func sessionFrom(r *http.Request) store.AdminSession {
	session, _ := r.Context().Value(sessionKey{}).(store.AdminSession)
	return session
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	session := sessionFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "csrf_token": session.CSRFToken, "expires_at": session.ExpiresAt})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.db.DeleteAdminSession(r.Context(), sessionFrom(r).ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts, err := s.db.AdminCounts(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	projects, err := s.db.ListProjects(ctx, "")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	recent, err := s.db.RecentEntries(ctx, 10)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	recentResponse := make([]map[string]any, 0, len(recent))
	for _, entry := range recent {
		item := entryResponse(entry.Entry)
		item["project_name"] = entry.ProjectName
		recentResponse = append(recentResponse, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": counts, "projects": projects, "recent_entries": recentResponse})
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	tier := r.URL.Query().Get("tier")
	if tier != "" && !slices.Contains(store.Tiers, tier) {
		writeError(w, http.StatusBadRequest, "tier must be one of "+strings.Join(store.Tiers, ", "))
		return
	}
	projects, err := s.db.ListProjects(r.Context(), tier)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := store.ValidateProjectSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entries := defaultEntries
	if raw := r.URL.Query().Get("entries"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxEntries {
			writeError(w, http.StatusBadRequest, "entries must be between 1 and "+strconv.Itoa(maxEntries))
			return
		}
		entries = parsed
	}
	var before *int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "before must be a positive entry ID")
			return
		}
		before = &parsed
	}
	result, nextBefore, err := s.db.GetProjectPage(r.Context(), slug, entries, before)
	if err != nil {
		if store.IsInvalidEntryCursor(err) {
			writeError(w, http.StatusBadRequest, "before is not an entry in this project")
			return
		}
		if store.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		s.internalError(w, r, err)
		return
	}
	payload := map[string]any{"project": result.Project, "entries": entryResponses(result.Entries)}
	if nextBefore != nil {
		payload["next_before"] = strconv.FormatInt(*nextBefore, 10)
	}
	writeJSON(w, http.StatusOK, payload)
}

type projectInput struct {
	Name        string `json:"name"`
	Tier        string `json:"tier"`
	HoursWK     int    `json:"hours_wk"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Goal        string `json:"goal"`
	Deadline    string `json:"deadline"`
	NeedsMe     string `json:"needs_me"`
	Automate    string `json:"automate"`
	Stack       string `json:"stack"`
}

func (s *Server) putProject(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	var input projectInput
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	project := store.Project{Slug: slug, Name: input.Name, Tier: input.Tier, HoursWK: input.HoursWK, Type: input.Type, Description: input.Description, Goal: input.Goal, Deadline: input.Deadline, NeedsMe: input.NeedsMe, Automate: input.Automate, Stack: input.Stack}
	if err := store.ValidateProject(project); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.db.UpsertProject(r.Context(), project)
	if err != nil {
		if store.IsCheckViolation(err) {
			writeError(w, http.StatusBadRequest, "project violates the registry constraints")
			return
		}
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) appendEntry(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := store.ValidateProjectSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		Kind string `json:"kind"`
		Body string `json:"body"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := store.ValidateEntry(input.Kind, input.Body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, err := s.db.AppendEntry(r.Context(), slug, input.Kind, input.Body, writeSource, clientIdentifier(sessionFrom(r)))
	if err != nil {
		switch {
		case store.IsForeignKeyViolation(err):
			writeError(w, http.StatusNotFound, "project not found")
		case store.IsCheckViolation(err):
			writeError(w, http.StatusBadRequest, "entry violates the registry constraints")
		default:
			s.internalError(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, entryResponse(entry))
}

func entryResponse(entry store.Entry) map[string]any {
	return map[string]any{
		"id": strconv.FormatInt(entry.ID, 10), "slug": entry.Slug, "kind": entry.Kind, "body": entry.Body,
		"source": entry.Source, "client_id": entry.ClientID, "created_at": entry.CreatedAt,
	}
}

func entryResponses(entries []store.Entry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entryResponse(entry))
	}
	return out
}

// clientIdentifier attributes admin writes to a session without exposing session material.
func clientIdentifier(session store.AdminSession) string {
	sum := sha256.Sum256([]byte("entry-attribution:" + session.ID))
	return "admin-session-" + hex.EncodeToString(sum[:6])
}

type searchHit struct {
	Ref         string     `json:"ref"`
	Kind        string     `json:"kind"`
	Score       float64    `json:"score"`
	Snippet     string     `json:"snippet"`
	ProjectSlug string     `json:"project_slug"`
	ProjectName string     `json:"project_name"`
	EntryID     string     `json:"entry_id,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	Source      string     `json:"source,omitempty"`
	ClientID    string     `json:"client_id,omitempty"`
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query   string `json:"q"`
		Limit   int    `json:"limit"`
		Project string `json:"project"`
		Kind    string `json:"kind"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" || utf8.RuneCountInString(input.Query) > maxSearchRunes {
		writeError(w, http.StatusBadRequest, "q must be 1 to 1000 characters")
		return
	}
	if input.Limit == 0 {
		input.Limit = 10
	}
	if input.Limit < 1 || input.Limit > 30 {
		writeError(w, http.StatusBadRequest, "limit must be between 1 and 30")
		return
	}
	if input.Project != "" {
		if err := store.ValidateProjectSlug(input.Project); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if input.Kind != "" && !slices.Contains(store.EntryKinds, input.Kind) && input.Kind != "project" {
		writeError(w, http.StatusBadRequest, "kind must be project or one of "+strings.Join(store.EntryKinds, ", "))
		return
	}
	// ponytail: filters are applied after retrieval over the top 30 index hits; push them into ledger-index if recall matters.
	indexLimit := input.Limit
	if input.Project != "" || input.Kind != "" {
		indexLimit = 30
	}
	ctx := r.Context()
	result, err := s.index.Search(ctx, input.Query, indexLimit)
	if err != nil {
		log.Printf("admin: index search: %v", err)
		writeError(w, http.StatusServiceUnavailable, "search unavailable")
		return
	}
	ids := make([]int64, 0, len(result.Hits))
	for _, hit := range result.Hits {
		if raw, ok := strings.CutPrefix(hit.Ref, "entry:"); ok {
			if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
	}
	entries, err := s.db.EntriesByID(ctx, ids)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	projects, err := s.db.ListProjects(ctx, "")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	names := make(map[string]store.Project, len(projects))
	for _, project := range projects {
		names[project.Slug] = project
	}
	hits := make([]searchHit, 0, input.Limit)
	for _, ranked := range result.Hits {
		hit := searchHit{Ref: ranked.Ref, Kind: ranked.Kind, Score: ranked.Score, Snippet: ranked.Snippet, ProjectSlug: ranked.ProjectSlug, ProjectName: names[ranked.ProjectSlug].Name}
		if raw, ok := strings.CutPrefix(ranked.Ref, "entry:"); ok {
			if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
				if entry, found := entries[id]; found {
					hit.EntryID = strconv.FormatInt(entry.ID, 10)
					hit.CreatedAt = &entry.CreatedAt
					hit.Source = entry.Source
					hit.ClientID = entry.ClientID
					hit.Kind = entry.Kind
					hit.ProjectSlug = entry.Slug
					hit.ProjectName = entry.ProjectName
				}
			}
		} else if project, found := names[ranked.ProjectSlug]; found {
			hit.Kind = "project"
			updated := project.UpdatedAt
			hit.CreatedAt = &updated
		}
		if (input.Project != "" && hit.ProjectSlug != input.Project) || (input.Kind != "" && hit.Kind != input.Kind) {
			continue
		}
		hits = append(hits, hit)
		if len(hits) == input.Limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "degraded": result.Degraded})
}

type clientSummary struct {
	store.OAuthClient
	ActiveAccessTokens int `json:"active_access_tokens"`
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	limit := defaultClients
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxClients {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and "+strconv.Itoa(maxClients))
			return
		}
		limit = parsed
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
		offset = parsed
	}
	ctx := r.Context()
	clients, err := s.db.ListClientsPage(ctx, limit+1, offset)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	hasMore := len(clients) > limit
	if hasMore {
		clients = clients[:limit]
	}
	ids := make([]string, len(clients))
	for i, client := range clients {
		ids[i] = client.ClientID
	}
	tokens, err := s.db.ActiveTokenCountsForClients(ctx, ids)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	summaries := make([]clientSummary, len(clients))
	for i, client := range clients {
		summaries[i] = clientSummary{OAuthClient: client, ActiveAccessTokens: tokens[client.ClientID]}
	}
	response := map[string]any{"clients": summaries}
	if hasMore {
		response["next_offset"] = offset + len(clients)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) revokeClient(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ClientID string `json:"client_id"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	if input.ClientID == "" {
		writeError(w, http.StatusBadRequest, "client_id is required")
		return
	}
	ctx := r.Context()
	if _, err := s.db.GetClient(ctx, input.ClientID); err != nil {
		if store.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "client not found")
			return
		}
		s.internalError(w, r, err)
		return
	}
	revoked, err := s.db.Revoke(ctx, input.ClientID, false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": revoked})
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("admin: %s %s: %v", r.Method, r.URL.Path, err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request must contain exactly one JSON object")
	}
	return nil
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON body")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
