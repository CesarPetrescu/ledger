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
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	calendarapi "github.com/cesarpetrescu/ledger/internal/calendar"
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
	maxHandoffJSON = 1 << 20
)

type Config struct {
	PublicURL         string
	PasswordHash      string
	InternalProxyCIDR string
	IndexURL          string
	Calendar          *calendarapi.Service
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
	calendar *calendarapi.Service
}

type sessionKey struct{}

func NewServer(config Config, db *store.DB) *Server {
	if !strings.HasPrefix(config.PasswordHash, "$argon2id$") {
		panic("LEDGER_ADMIN_PASSWORD_HASH must be an Argon2id PHC string")
	}
	s := &Server{config: config, origin: publicOrigin(config.PublicURL), db: db, index: retrieval.NewClient(config.IndexURL), mux: http.NewServeMux(), requests: oauth.NewRateLimiter(), failures: oauth.NewRateLimiter(), events: newEventStream(db), calendar: config.Calendar}
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
	s.mux.HandleFunc("GET /admin/api/projects/{slug}/files", s.listProjectFiles)
	s.mux.HandleFunc("GET /admin/api/entries", s.listEntries)
	s.mux.HandleFunc("GET /admin/api/entries.csv", s.exportEntries)
	s.mux.HandleFunc("POST /admin/api/entries/{id}/resolve", s.resolveTodo)
	s.mux.HandleFunc("POST /admin/api/entries/{id}/reopen", s.reopenTodo)
	s.mux.HandleFunc("GET /admin/api/entries/{id}/related", s.relatedEntries)
	s.mux.HandleFunc("GET /admin/api/table/projects", s.projectSummaries)
	s.mux.HandleFunc("GET /admin/api/handoffs", s.listHandoffs)
	s.mux.HandleFunc("POST /admin/api/handoffs", s.createHandoff)
	s.mux.HandleFunc("GET /admin/api/handoffs/{id}", s.getHandoff)
	s.mux.HandleFunc("PUT /admin/api/handoffs/{id}", s.putHandoff)
	s.mux.HandleFunc("GET /admin/api/handoffs/{id}/export", s.exportHandoff)
	s.mux.HandleFunc("POST /admin/api/handoffs/{id}/messages", s.appendHandoffMessage)
	s.mux.HandleFunc("POST /admin/api/handoff-messages/{id}/actions", s.updateHandoffMessage)
	s.mux.HandleFunc("POST /admin/api/handoff-messages/{id}/files", s.uploadHandoffFile)
	s.mux.HandleFunc("GET /admin/api/handoff-files/{id}", s.downloadHandoffFile)
	s.mux.HandleFunc("DELETE /admin/api/handoff-files/{id}", s.deleteHandoffFile)
	s.mux.HandleFunc("POST /admin/api/search", s.search)
	s.mux.HandleFunc("GET /admin/api/calendar/connection", s.calendarConnection)
	s.mux.HandleFunc("POST /admin/api/calendar/connect", s.startCalendarLogin)
	s.mux.HandleFunc("POST /admin/api/calendar/connect/{id}/poll", s.pollCalendarLogin)
	s.mux.HandleFunc("DELETE /admin/api/calendar/connection", s.disconnectCalendar)
	s.mux.HandleFunc("GET /admin/api/calendar/calendars", s.listCalendars)
	s.mux.HandleFunc("PUT /admin/api/calendar/calendars", s.selectCalendars)
	s.mux.HandleFunc("GET /admin/api/calendar/events", s.listCalendarEvents)
	s.mux.HandleFunc("POST /admin/api/calendar/events", s.createCalendarEvent)
	s.mux.HandleFunc("GET /admin/api/calendar/events/{id}", s.getCalendarEvent)
	s.mux.HandleFunc("PUT /admin/api/calendar/events/{id}", s.updateCalendarEvent)
	s.mux.HandleFunc("DELETE /admin/api/calendar/events/{id}", s.deleteCalendarEvent)
	s.mux.HandleFunc("POST /admin/api/oauth/device", s.deviceRequest)
	s.mux.HandleFunc("GET /admin/api/oauth/clients", s.listClients)
	s.mux.HandleFunc("POST /admin/api/oauth/revoke", s.revokeClient)
	s.mux.HandleFunc("GET /admin/api/events", func(w http.ResponseWriter, r *http.Request) { s.events.serve(s.origin, w, r) })
	return s
}

// RunEvents streams committed database changes to connected operator consoles.
func (s *Server) RunEvents(ctx context.Context) error { return s.events.run(ctx) }

func (s *Server) RunCalendarSync(ctx context.Context) error {
	if s.calendar == nil {
		return nil
	}
	return s.calendar.RunSyncWatch(ctx)
}

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

// entryFilter reads the shared query parameters of the entry table and its CSV export.
func entryFilter(query url.Values) (store.EntryFilter, error) {
	f := store.EntryFilter{ProjectSlug: query.Get("project"), Kind: query.Get("kind"), Source: query.Get("source"), Tag: query.Get("tag"), Status: query.Get("status"), Query: strings.TrimSpace(query.Get("q"))}
	if f.ProjectSlug != "" {
		if err := store.ValidateProjectSlug(f.ProjectSlug); err != nil {
			return f, err
		}
	}
	if f.Kind != "" && !slices.Contains(store.EntryKinds, f.Kind) {
		return f, errors.New("kind must be one of " + strings.Join(store.EntryKinds, ", "))
	}
	if err := store.ValidateContextHeader("source", f.Source, false); err != nil || utf8.RuneCountInString(f.Source) > 200 {
		return f, errors.New("source must be at most 200 characters on one line")
	}
	if utf8.RuneCountInString(f.Tag) > 30 || strings.ContainsAny(f.Tag, "\r\n") {
		return f, errors.New("tag must be at most 30 characters on one line")
	}
	if f.Status != "" && f.Status != "open" && f.Status != "done" {
		return f, errors.New("status must be open or done")
	}
	if utf8.RuneCountInString(f.Query) > maxSearchRunes {
		return f, errors.New("q must be at most 1000 characters")
	}
	return f, nil
}

func (s *Server) listEntries(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter, err := entryFilter(query)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := defaultEntries
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxEntries {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and "+strconv.Itoa(maxEntries))
			return
		}
	}
	if raw := query.Get("before"); raw != "" {
		before, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 1 {
			writeError(w, http.StatusBadRequest, "before must be a positive entry ID")
			return
		}
		filter.Before = &before
	}
	filter.Limit = limit + 1
	entries, err := s.db.ListEntries(r.Context(), filter)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sources, err := s.db.EntrySources(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	tags, err := s.db.EntryTags(r.Context(), 50)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	payload := map[string]any{"sources": sources, "tags": tags}
	if len(entries) > limit {
		entries = entries[:limit]
		payload["next_before"] = strconv.FormatInt(entries[limit-1].ID, 10)
	}
	rows := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, tableEntryResponse(entry))
	}
	payload["entries"] = rows
	writeJSON(w, http.StatusOK, payload)
}

// exportEntries downloads every entry matching the table filters as CSV for
// spreadsheets. Times are UTC in a format Excel, LibreOffice, and Sheets parse.
func (s *Server) exportEntries(w http.ResponseWriter, r *http.Request) {
	filter, err := entryFilter(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// ponytail: whole export is buffered in memory; stream rows if entries reach the hundreds of thousands.
	entries, err := s.db.ListEntries(r.Context(), filter)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "ledger-entries-" + time.Now().UTC().Format("20060102") + ".csv"}))
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "\ufeff") // BOM so Excel reads UTF-8
	out := csv.NewWriter(w)
	_ = out.Write([]string{"Time (UTC)", "Project", "Project slug", "Kind", "Agent", "Title", "Tags", "Priority", "Todo state", "Text", "Entry ID", "Client ID"})
	for _, e := range entries {
		var title, tags, priority, state string
		if e.Meta != nil {
			title, tags, priority = e.Meta.Title, strings.Join(e.Meta.Tags, ", "), e.Meta.Priority
		}
		if e.Kind == "todo" {
			state = "open"
			if e.ResolvedBy != nil {
				state = "done"
			}
		} else {
			priority = ""
		}
		_ = out.Write([]string{e.CreatedAt.UTC().Format("2006-01-02 15:04:05"), spreadsheetText(e.ProjectName), e.Slug, e.Kind, spreadsheetText(e.Source), spreadsheetText(title), spreadsheetText(tags), priority, state, spreadsheetText(e.Body), strconv.FormatInt(e.ID, 10), spreadsheetText(e.ClientID)})
	}
	out.Flush()
}

func tableEntryResponse(entry store.EntryWithProject) map[string]any {
	item := entryResponse(entry.Entry)
	item["project_name"] = entry.ProjectName
	if entry.Meta != nil {
		item["meta"] = entry.Meta
	}
	if entry.DuplicateOf != nil {
		item["duplicate_of"] = strconv.FormatInt(*entry.DuplicateOf, 10)
	}
	if entry.ResolvedBy != nil {
		item["resolved_by"] = map[string]any{"entry_id": strconv.FormatInt(entry.ResolvedBy.EntryID, 10), "origin": entry.ResolvedBy.Origin, "created_at": entry.ResolvedBy.CreatedAt}
	}
	return item
}

func pathEntryID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid entry id")
		return 0, false
	}
	return id, true
}

func (s *Server) resolveTodo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathEntryID(w, r)
	if !ok {
		return
	}
	entry, err := s.db.ResolveTodo(r.Context(), id, writeSource, clientIdentifier(sessionFrom(r)))
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, entryResponse(entry))
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, "entry not found")
	case errors.Is(err, store.ErrNotTodo):
		writeError(w, http.StatusBadRequest, "only todos can be marked done")
	case errors.Is(err, store.ErrAlreadyResolved):
		writeError(w, http.StatusConflict, "todo is already done")
	default:
		s.internalError(w, r, err)
	}
}

func (s *Server) reopenTodo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathEntryID(w, r)
	if !ok {
		return
	}
	entry, err := s.db.ReopenTodo(r.Context(), id, writeSource, clientIdentifier(sessionFrom(r)))
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, entryResponse(entry))
	case errors.Is(err, store.ErrNotResolved):
		writeError(w, http.StatusConflict, "todo is not done")
	default:
		s.internalError(w, r, err)
	}
}

// relatedMinSimilarity hides weak matches; calibrated on Qwen3-Embedding-8B.
const relatedMinSimilarity = 0.62

func (s *Server) relatedEntries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathEntryID(w, r)
	if !ok {
		return
	}
	related, err := s.db.RelatedEntries(r.Context(), id, relatedMinSimilarity, 3)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	rows := make([]map[string]any, 0, len(related))
	for _, entry := range related {
		item := tableEntryResponse(entry.EntryWithProject)
		item["similarity"] = entry.Similarity
		rows = append(rows, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"related": rows})
}

func (s *Server) projectSummaries(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.ProjectSummaries(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	progress, err := s.db.MetaProgress(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects, "metadata": progress})
}

// spreadsheetText stops agent-written text from being evaluated as a formula
// when the CSV is opened in a spreadsheet (CSV injection).
func spreadsheetText(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
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

func (s *Server) calendarConnection(w http.ResponseWriter, r *http.Request) {
	if !s.requireCalendar(w) {
		return
	}
	connection, err := s.calendar.Connection(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, connection)
}

func (s *Server) startCalendarLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireCalendar(w) {
		return
	}
	var input struct {
		ServerURL string `json:"server_url"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	flow, err := s.calendar.StartLogin(r.Context(), input.ServerURL)
	if err != nil {
		s.calendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, flow)
}

func (s *Server) pollCalendarLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireCalendar(w) {
		return
	}
	connection, err := s.calendar.PollLogin(r.Context(), r.PathValue("id"))
	if errors.Is(err, calendarapi.ErrPending) {
		writeJSON(w, http.StatusAccepted, map[string]bool{"pending": true})
		return
	}
	if err != nil {
		s.calendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, connection)
}

func (s *Server) disconnectCalendar(w http.ResponseWriter, r *http.Request) {
	if !s.requireCalendar(w) {
		return
	}
	if err := s.calendar.Disconnect(r.Context()); err != nil {
		s.calendarError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listCalendars(w http.ResponseWriter, r *http.Request) {
	if !s.requireCalendar(w) {
		return
	}
	calendars, err := s.calendar.Calendars(r.Context())
	if err != nil {
		s.calendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"calendars": calendars})
}

func (s *Server) selectCalendars(w http.ResponseWriter, r *http.Request) {
	if !s.requireCalendar(w) {
		return
	}
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := s.calendar.SelectCalendars(r.Context(), input.IDs); err != nil {
		s.calendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"selected": len(input.IDs)})
}

func (s *Server) listCalendarEvents(w http.ResponseWriter, r *http.Request) {
	if !s.requireCalendar(w) {
		return
	}
	start, startErr := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
	end, endErr := time.Parse(time.RFC3339, r.URL.Query().Get("end"))
	if startErr != nil || endErr != nil {
		writeError(w, http.StatusBadRequest, "start and end must be RFC 3339 timestamps")
		return
	}
	events, err := s.calendar.ListEvents(r.Context(), start, end, r.URL.Query().Get("calendar"))
	if err != nil {
		s.calendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) getCalendarEvent(w http.ResponseWriter, r *http.Request) {
	if !s.requireCalendar(w) {
		return
	}
	event, err := s.calendar.GetEvent(r.Context(), r.PathValue("id"))
	if err != nil {
		s.calendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) createCalendarEvent(w http.ResponseWriter, r *http.Request) {
	if !s.requireCalendar(w) {
		return
	}
	var input struct {
		CalendarID string `json:"calendar_id"`
		calendarapi.EventInput
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	event, err := s.calendar.CreateEvent(r.Context(), input.CalendarID, input.EventInput)
	if err != nil {
		s.calendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, event)
}

func (s *Server) updateCalendarEvent(w http.ResponseWriter, r *http.Request) {
	if !s.requireCalendar(w) {
		return
	}
	var input struct {
		ETag string `json:"etag"`
		calendarapi.EventInput
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	event, err := s.calendar.UpdateEvent(r.Context(), r.PathValue("id"), input.ETag, input.EventInput)
	if err != nil {
		s.calendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) deleteCalendarEvent(w http.ResponseWriter, r *http.Request) {
	if !s.requireCalendar(w) {
		return
	}
	var input struct {
		ETag string `json:"etag"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := s.calendar.DeleteEvent(r.Context(), r.PathValue("id"), input.ETag); err != nil {
		s.calendarError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) requireCalendar(w http.ResponseWriter) bool {
	if s.calendar == nil {
		writeError(w, http.StatusServiceUnavailable, "calendar integration unavailable")
		return false
	}
	return true
}

func (s *Server) calendarError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, calendarapi.ErrNotConnected):
		writeError(w, http.StatusConflict, "connect Nextcloud first")
	case errors.Is(err, calendarapi.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, calendarapi.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case strings.Contains(err.Error(), "required"), strings.Contains(err.Error(), "invalid"), strings.Contains(err.Error(), "unknown"), strings.Contains(err.Error(), "selected"), strings.Contains(err.Error(), "must"), strings.Contains(err.Error(), "expired"):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		log.Printf("admin: calendar %s %s: %v", r.Method, r.URL.Path, err)
		writeError(w, http.StatusBadGateway, "Nextcloud calendar is unavailable")
	}
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
	if input.Kind != "" && !slices.Contains(store.EntryKinds, input.Kind) && input.Kind != "project" && input.Kind != "entry" {
		writeError(w, http.StatusBadRequest, "kind must be project, entry, or one of "+strings.Join(store.EntryKinds, ", "))
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
		kindMismatch := input.Kind != "" && hit.Kind != input.Kind
		if input.Kind == "entry" {
			kindMismatch = hit.Kind == "project"
		}
		if (input.Project != "" && hit.ProjectSlug != input.Project) || kindMismatch {
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
	store.ClientTokenCounts
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
		summaries[i] = clientSummary{OAuthClient: client, ClientTokenCounts: tokens[client.ClientID]}
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
