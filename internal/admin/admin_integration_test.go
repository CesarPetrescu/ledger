//go:build integration

package admin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/oauth"
	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
	"github.com/coder/websocket"
)

type session struct {
	cookie string
	csrf   string
}

func newIntegrationServer(t *testing.T, db *store.DB, indexURL string) *Server {
	t.Helper()
	hash, err := oauth.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(Config{PublicURL: testPublicURL, PasswordHash: hash, InternalProxyCIDR: "172.31.255.2/32", IndexURL: indexURL}, db)
}

func login(t *testing.T, server http.Handler, password string, existing string) (*httptest.ResponseRecorder, session) {
	t.Helper()
	headers := map[string]string{"Origin": testPublicURL}
	if existing != "" {
		headers["Cookie"] = existing
	}
	res := request(t, server, http.MethodPost, "/admin/api/login", `{"password":"`+password+`"}`, headers)
	var body struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == sessionCookie && cookie.Value != "" {
			return res, session{cookie: cookie.Name + "=" + cookie.Value, csrf: body.CSRF}
		}
	}
	return res, session{}
}

func authed(s session, mutating bool) map[string]string {
	headers := map[string]string{"Cookie": s.cookie}
	if mutating {
		headers["Origin"] = testPublicURL
		headers["X-CSRF-Token"] = s.csrf
	}
	return headers
}

func TestCommittedChangesReachAuthenticatedWebSockets(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, session := login(t, server, "correct horse", "")
	listenerCtx, stopListener := context.WithCancel(ctx)
	defer stopListener()
	go func() { _ = server.RunEvents(listenerCtx) }()

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/admin/api/events"
	connection, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {testPublicURL}, "Cookie": {session.cookie}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writesDone := make(chan struct{})
	defer close(writesDone)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-writesDone:
				return
			case <-ticker.C:
				_, _ = db.UpsertProject(ctx, store.Project{Slug: "live-test", Name: "Live test", Tier: "focus"})
			}
		}
	}()

	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, payload, err := connection.Read(readCtx)
	if err != nil || !strings.Contains(string(payload), `"entity":"*"`) {
		t.Fatalf("live event = %q, %v", payload, err)
	}
	if _, err := db.RevokeAdminSessions(ctx); err != nil {
		t.Fatal(err)
	}
	revokedCtx, cancelRevoked := context.WithTimeout(ctx, 5*time.Second)
	defer cancelRevoked()
	for {
		if _, _, err := connection.Read(revokedCtx); err != nil {
			if revokedCtx.Err() != nil {
				t.Fatal("revoked session kept receiving live events")
			}
			break
		}
	}
}

func TestLoginIssuesHardenedCookieAndSessionLifecycle(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")

	if res, _ := login(t, server, "wrong", ""); res.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", res.Code)
	}
	res, s := login(t, server, "correct horse", "")
	if res.Code != http.StatusOK || s.cookie == "" || s.csrf == "" {
		t.Fatalf("login = %d %s", res.Code, res.Body.String())
	}
	assertSecurityHeaders(t, res)
	cookie := res.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/admin" || cookie.MaxAge <= 0 || cookie.MaxAge > int(store.AdminSessionTTL/time.Second) {
		t.Fatalf("session cookie attributes = %#v", cookie)
	}
	raw := strings.TrimPrefix(s.cookie, sessionCookie+"=")
	hash := sha256.Sum256([]byte(raw))
	var hashed, plaintext int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM admin_session WHERE hash=$1`, hash[:]).Scan(&hashed); err != nil || hashed != 1 {
		t.Fatalf("hashed session rows = %d, %v", hashed, err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM admin_session WHERE encode(hash,'hex')=$1 OR csrf_token=$1`, raw).Scan(&plaintext); err != nil || plaintext != 0 {
		t.Fatalf("raw session id found at rest: %d, %v", plaintext, err)
	}
	if strings.Contains(res.Body.String(), raw) {
		t.Fatal("login response body echoes the session identifier")
	}

	status := request(t, server, http.MethodGet, "/admin/api/session", "", authed(s, false))
	var bootstrap struct {
		Authenticated bool   `json:"authenticated"`
		CSRF          string `json:"csrf_token"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &bootstrap); err != nil || status.Code != http.StatusOK || !bootstrap.Authenticated || bootstrap.CSRF != s.csrf {
		t.Fatalf("session bootstrap = %d %s", status.Code, status.Body.String())
	}

	// Login while holding a live session rotates it.
	res2, s2 := login(t, server, "correct horse", s.cookie)
	if res2.Code != http.StatusOK || s2.cookie == s.cookie || s2.csrf == s.csrf {
		t.Fatalf("rotated login = %d, same cookie=%v", res2.Code, s2.cookie == s.cookie)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/session", "", authed(s, false)); res.Code != http.StatusUnauthorized {
		t.Fatalf("pre-rotation session still valid: %d", res.Code)
	}
	if count, err := db.CountActiveAdminSessions(ctx); err != nil || count != 1 {
		t.Fatalf("active sessions after rotation = %d, %v", count, err)
	}

	// CSRF and Origin are enforced on every state-changing endpoint.
	for name, headers := range map[string]map[string]string{
		"no csrf":       {"Cookie": s2.cookie, "Origin": testPublicURL},
		"wrong csrf":    {"Cookie": s2.cookie, "Origin": testPublicURL, "X-CSRF-Token": "nope"},
		"no origin":     {"Cookie": s2.cookie, "X-CSRF-Token": s2.csrf},
		"wrong origin":  {"Cookie": s2.cookie, "Origin": "https://evil.example", "X-CSRF-Token": s2.csrf},
		"stale csrf":    {"Cookie": s2.cookie, "Origin": testPublicURL, "X-CSRF-Token": s.csrf},
		"csrf in query": {"Cookie": s2.cookie, "Origin": testPublicURL},
	} {
		path := "/admin/api/logout"
		if name == "csrf in query" {
			path += "?csrf_token=" + s2.csrf
		}
		if res := request(t, server, http.MethodPost, path, "", headers); res.Code != http.StatusForbidden {
			t.Errorf("logout %s = %d, want 403", name, res.Code)
		}
	}
	if res := request(t, server, http.MethodGet, "/admin/api/logout", "", authed(s2, false)); res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET logout = %d, want 405", res.Code)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/session", "", authed(s2, false)); res.Code != http.StatusOK {
		t.Fatalf("GET logout mutated the session: %d", res.Code)
	}

	out := request(t, server, http.MethodPost, "/admin/api/logout", "", authed(s2, true))
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %s", out.Code, out.Body.String())
	}
	cleared := false
	for _, cookie := range out.Result().Cookies() {
		if cookie.Name == sessionCookie && cookie.Value == "" && cookie.MaxAge < 0 && cookie.Path == "/admin" {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("logout did not clear the cookie: %v", out.Result().Cookies())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/session", "", authed(s2, false)); res.Code != http.StatusUnauthorized {
		t.Fatalf("session usable after logout: %d", res.Code)
	}
	if count, err := db.CountActiveAdminSessions(ctx); err != nil || count != 0 {
		t.Fatalf("sessions after logout = %d, %v", count, err)
	}
}

func TestExpiredAndRevokedSessionsAreRejected(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.Pool.Exec(ctx, `UPDATE admin_session SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	res := request(t, server, http.MethodGet, "/admin/api/overview", "", authed(s, false))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expired session = %d", res.Code)
	}
	_, s = login(t, server, "correct horse", "")
	if _, err := db.RevokeAdminSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/overview", "", authed(s, false)); res.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session = %d", res.Code)
	}
}

func TestProjectsEntriesOverviewAndClientsThroughTheAPI(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")

	for name, body := range map[string]string{
		"missing name":  `{"tier":"focus"}`,
		"bad tier":      `{"name":"Atlas","tier":"urgent"}`,
		"bad hours":     `{"name":"Atlas","tier":"focus","hours_wk":200}`,
		"unknown field": `{"name":"Atlas","tier":"focus","slug":"other"}`,
		"newline name":  `{"name":"Atlas\nforged","tier":"focus"}`,
	} {
		if res := request(t, server, http.MethodPut, "/admin/api/projects/atlas", body, authed(s, true)); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, res.Code, res.Body.String())
		}
	}
	if res := request(t, server, http.MethodPut, "/admin/api/projects/Bad_Slug", `{"name":"Atlas","tier":"focus"}`, authed(s, true)); res.Code != http.StatusBadRequest {
		t.Fatalf("invalid slug = %d", res.Code)
	}
	created := request(t, server, http.MethodPut, "/admin/api/projects/atlas", `{"name":"Atlas","tier":"focus","hours_wk":8,"goal":"Ship v1","deadline":"Friday","stack":"Go"}`, authed(s, true))
	if created.Code != http.StatusOK {
		t.Fatalf("create project = %d %s", created.Code, created.Body.String())
	}
	var project store.Project
	if err := json.Unmarshal(created.Body.Bytes(), &project); err != nil || project.Slug != "atlas" || project.Goal != "Ship v1" {
		t.Fatalf("created project = %s, %v", created.Body.String(), err)
	}
	if res := request(t, server, http.MethodPut, "/admin/api/projects/beacon", `{"name":"Beacon","tier":"park"}`, authed(s, true)); res.Code != http.StatusOK {
		t.Fatalf("second project = %d", res.Code)
	}

	if res := request(t, server, http.MethodPost, "/admin/api/projects/atlas/entries", `{"kind":"memo","body":"x"}`, authed(s, true)); res.Code != http.StatusBadRequest {
		t.Fatalf("bad kind = %d", res.Code)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/projects/missing/entries", `{"kind":"note","body":"x"}`, authed(s, true)); res.Code != http.StatusNotFound {
		t.Fatalf("entry for missing project = %d %s", res.Code, res.Body.String())
	}
	appended := request(t, server, http.MethodPost, "/admin/api/projects/atlas/entries", `{"kind":"decision","body":"Folosim PostgreSQL."}`, authed(s, true))
	if appended.Code != http.StatusCreated {
		t.Fatalf("append = %d %s", appended.Code, appended.Body.String())
	}
	var entry struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Source   string `json:"source"`
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(appended.Body.Bytes(), &entry); err != nil || entry.Kind != "decision" || entry.Source != "ledger-admin" || !strings.HasPrefix(entry.ClientID, "admin-session-") || len(entry.ClientID) != len("admin-session-")+12 {
		t.Fatalf("appended entry = %s, %v", appended.Body.String(), err)
	}
	raw := strings.TrimPrefix(s.cookie, sessionCookie+"=")
	if strings.Contains(entry.ClientID, raw[:8]) {
		t.Fatal("entry attribution leaks the session identifier")
	}
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if res := request(t, server, method, "/admin/api/projects/atlas/entries", `{}`, authed(s, true)); res.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s entries = %d, want 405 (entries are append-only)", method, res.Code)
		}
	}

	detail := request(t, server, http.MethodGet, "/admin/api/projects/atlas?entries=5", "", authed(s, false))
	var withEntries struct {
		Project store.Project `json:"project"`
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &withEntries); err != nil || detail.Code != http.StatusOK || len(withEntries.Entries) != 1 || withEntries.Project.Name != "Atlas" {
		t.Fatalf("detail = %d %s", detail.Code, detail.Body.String())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/projects/atlas?entries=9999", "", authed(s, false)); res.Code != http.StatusBadRequest {
		t.Fatalf("entries above the cap = %d", res.Code)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/projects/missing", "", authed(s, false)); res.Code != http.StatusNotFound {
		t.Fatalf("missing project = %d", res.Code)
	}
	list := request(t, server, http.MethodGet, "/admin/api/projects?tier=park", "", authed(s, false))
	var listed struct {
		Projects []store.Project `json:"projects"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed.Projects) != 1 || listed.Projects[0].Slug != "beacon" {
		t.Fatalf("filtered list = %s", list.Body.String())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/projects?tier=urgent", "", authed(s, false)); res.Code != http.StatusBadRequest {
		t.Fatalf("invalid tier filter = %d", res.Code)
	}

	if _, err := db.PutClient(ctx, store.OAuthClient{ClientID: "client-a", Kind: "dcr", Name: "Agent A", RedirectURIs: []string{"http://127.0.0.1/cb"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO oauth_token(hash,kind,client_id,scope,family,expires_at) VALUES(decode(repeat('01',32),'hex'),'access','client-a','ledger:read','00000000-0000-4000-8000-000000000001',now()+interval '10 minutes')`); err != nil {
		t.Fatal(err)
	}
	overview := request(t, server, http.MethodGet, "/admin/api/overview", "", authed(s, false))
	var summary struct {
		Counts   store.AdminCounts `json:"counts"`
		Projects []store.Project   `json:"projects"`
		Recent   []struct {
			ID          string `json:"id"`
			ProjectName string `json:"project_name"`
		} `json:"recent_entries"`
	}
	if err := json.Unmarshal(overview.Body.Bytes(), &summary); err != nil || overview.Code != http.StatusOK {
		t.Fatalf("overview = %d %s", overview.Code, overview.Body.String())
	}
	if want := (store.AdminCounts{Projects: 2, Entries: 1, Clients: 1, ActiveTokens: 1, ActiveSessions: 1}); summary.Counts != want || len(summary.Projects) != 2 || len(summary.Recent) != 1 || summary.Recent[0].ID == "" || summary.Recent[0].ProjectName != "Atlas" {
		t.Fatalf("overview = %s", overview.Body.String())
	}

	clients := request(t, server, http.MethodGet, "/admin/api/oauth/clients", "", authed(s, false))
	if clients.Code != http.StatusOK || !strings.Contains(clients.Body.String(), `"client_name":"Agent A"`) || !strings.Contains(clients.Body.String(), `"active_access_tokens":1`) {
		t.Fatalf("clients = %d %s", clients.Code, clients.Body.String())
	}
	for _, forbidden := range []string{"hash", "0101010101", "secret", "refresh"} {
		if strings.Contains(strings.ToLower(clients.Body.String()), forbidden) {
			t.Fatalf("clients response leaks %q: %s", forbidden, clients.Body.String())
		}
	}
	if res := request(t, server, http.MethodPost, "/admin/api/oauth/revoke", `{"client_id":"missing"}`, authed(s, true)); res.Code != http.StatusNotFound {
		t.Fatalf("revoke unknown client = %d", res.Code)
	}
	revoked := request(t, server, http.MethodPost, "/admin/api/oauth/revoke", `{"client_id":"client-a"}`, authed(s, true))
	if revoked.Code != http.StatusOK || revoked.Body.String() != `{"revoked":1}`+"\n" {
		t.Fatalf("revoke = %d %s", revoked.Code, revoked.Body.String())
	}
	if _, _, err := db.LookupAccess(ctx, "unused"); err == nil {
		t.Fatal("unexpected token lookup success")
	}
	var live int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_token WHERE NOT revoked`).Scan(&live); err != nil || live != 0 {
		t.Fatalf("live tokens after revoke = %d, %v", live, err)
	}
}

func TestClientListIsBoundedAndPaginated(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	for _, id := range []string{"client-a", "client-b", "client-c"} {
		if _, err := db.PutClient(ctx, store.OAuthClient{ClientID: id, Kind: "dcr", Name: id, RedirectURIs: []string{"http://127.0.0.1/cb"}}); err != nil {
			t.Fatal(err)
		}
	}

	var first struct {
		Clients    []clientSummary `json:"clients"`
		NextOffset *int            `json:"next_offset"`
	}
	res := request(t, server, http.MethodGet, "/admin/api/oauth/clients?limit=2&offset=0", "", authed(s, false))
	if err := json.Unmarshal(res.Body.Bytes(), &first); err != nil || res.Code != http.StatusOK || len(first.Clients) != 2 || first.NextOffset == nil || *first.NextOffset != 2 {
		t.Fatalf("first client page = %d %s", res.Code, res.Body.String())
	}
	var second struct {
		Clients    []clientSummary `json:"clients"`
		NextOffset *int            `json:"next_offset"`
	}
	res = request(t, server, http.MethodGet, "/admin/api/oauth/clients?limit=2&offset=2", "", authed(s, false))
	if err := json.Unmarshal(res.Body.Bytes(), &second); err != nil || res.Code != http.StatusOK || len(second.Clients) != 1 || second.Clients[0].ClientID != "client-c" || second.NextOffset != nil {
		t.Fatalf("second client page = %d %s", res.Code, res.Body.String())
	}
	for _, path := range []string{"/admin/api/oauth/clients?limit=101", "/admin/api/oauth/clients?limit=0", "/admin/api/oauth/clients?offset=-1"} {
		if res := request(t, server, http.MethodGet, path, "", authed(s, false)); res.Code != http.StatusBadRequest {
			t.Errorf("invalid pagination %s = %d", path, res.Code)
		}
	}
}

func TestProjectTimelineIsCursorPaginated(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `SELECT setval(pg_get_serial_sequence('entry','id'), 9007199254740992)`); err != nil {
		t.Fatal(err)
	}
	entries := make([]store.Entry, 3)
	for i := range entries {
		entry, err := db.AppendEntry(ctx, "atlas", "note", "entry "+strconv.Itoa(i+1), "agent", "client-1")
		if err != nil {
			t.Fatal(err)
		}
		entries[i] = entry
	}
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "beacon", Name: "Beacon", Tier: "park"}); err != nil {
		t.Fatal(err)
	}
	foreign, err := db.AppendEntry(ctx, "beacon", "note", "foreign cursor", "agent", "client-1")
	if err != nil {
		t.Fatal(err)
	}

	var first struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
		NextBefore *string `json:"next_before"`
	}
	res := request(t, server, http.MethodGet, "/admin/api/projects/atlas?entries=2", "", authed(s, false))
	if err := json.Unmarshal(res.Body.Bytes(), &first); err != nil || res.Code != http.StatusOK || len(first.Entries) != 2 || first.Entries[0].ID != strconv.FormatInt(entries[2].ID, 10) || first.Entries[1].ID != strconv.FormatInt(entries[1].ID, 10) || first.NextBefore == nil || *first.NextBefore != strconv.FormatInt(entries[1].ID, 10) {
		t.Fatalf("first timeline page = %d %s", res.Code, res.Body.String())
	}

	var second struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
		NextBefore *string `json:"next_before"`
	}
	res = request(t, server, http.MethodGet, "/admin/api/projects/atlas?entries=2&before="+*first.NextBefore, "", authed(s, false))
	if err := json.Unmarshal(res.Body.Bytes(), &second); err != nil || res.Code != http.StatusOK || len(second.Entries) != 1 || second.Entries[0].ID != strconv.FormatInt(entries[0].ID, 10) || second.NextBefore != nil {
		t.Fatalf("second timeline page = %d %s", res.Code, res.Body.String())
	}
	for _, before := range []string{"0", "1", strconv.FormatInt(foreign.ID, 10)} {
		if res := request(t, server, http.MethodGet, "/admin/api/projects/atlas?before="+before, "", authed(s, false)); res.Code != http.StatusBadRequest {
			t.Errorf("invalid timeline cursor %s = %d %s", before, res.Code, res.Body.String())
		}
	}
}

func TestSearchAddsProvenanceFiltersAndDegradesGracefully(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	entry, err := db.AppendEntry(ctx, "atlas", "decision", "Folosim PostgreSQL.", "agent", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	note, err := db.AppendEntry(ctx, "atlas", "note", "Nota.", "agent", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	var requested struct {
		Query string `json:"q"`
		Limit int    `json:"limit"`
	}
	index := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&requested)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hits": []map[string]any{
				{"ref": "entry:" + itoa(entry.ID), "kind": "decision", "snippet": "Folosim PostgreSQL.", "project_slug": "atlas", "score": 0.9},
				{"ref": "entry:" + itoa(note.ID), "kind": "note", "snippet": "Nota.", "project_slug": "atlas", "score": 0.5},
				{"ref": "project:atlas", "kind": "project", "snippet": "Atlas", "project_slug": "atlas", "score": 0.4},
			},
			"degraded": []string{"vector"},
		})
	}))
	defer index.Close()
	server := newIntegrationServer(t, db, index.URL)
	_, s := login(t, server, "correct horse", "")

	for name, body := range map[string]string{
		"empty query":   `{"q":"  "}`,
		"limit too big": `{"q":"x","limit":31}`,
		"bad kind":      `{"q":"x","kind":"memo"}`,
		"bad project":   `{"q":"x","project":"Bad Slug"}`,
		"unknown field": `{"q":"x","offset":3}`,
	} {
		if res := request(t, server, http.MethodPost, "/admin/api/search", body, authed(s, true)); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", name, res.Code)
		}
	}
	res := request(t, server, http.MethodPost, "/admin/api/search", `{"q":"postgres","limit":2}`, authed(s, true))
	var result struct {
		Hits []struct {
			Ref         string  `json:"ref"`
			Kind        string  `json:"kind"`
			ProjectName string  `json:"project_name"`
			Source      string  `json:"source"`
			CreatedAt   string  `json:"created_at"`
			Score       float64 `json:"score"`
		} `json:"hits"`
		Degraded []string `json:"degraded"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || res.Code != http.StatusOK {
		t.Fatalf("search = %d %s", res.Code, res.Body.String())
	}
	if requested.Limit != 2 || len(result.Hits) != 2 || result.Hits[0].ProjectName != "Atlas" || result.Hits[0].Source != "agent" || result.Hits[0].CreatedAt == "" || result.Degraded[0] != "vector" {
		t.Fatalf("search result = %s (index limit %d)", res.Body.String(), requested.Limit)
	}
	filtered := request(t, server, http.MethodPost, "/admin/api/search", `{"q":"postgres","limit":1,"kind":"note","project":"atlas"}`, authed(s, true))
	if err := json.Unmarshal(filtered.Body.Bytes(), &result); err != nil || len(result.Hits) != 1 || result.Hits[0].Kind != "note" || requested.Limit != 30 {
		t.Fatalf("filtered search = %s (index limit %d)", filtered.Body.String(), requested.Limit)
	}
	index.Close()
	down := request(t, server, http.MethodPost, "/admin/api/search", `{"q":"postgres"}`, authed(s, true))
	if down.Code != http.StatusServiceUnavailable || !strings.Contains(down.Body.String(), "search unavailable") || strings.Contains(down.Body.String(), "127.0.0.1") {
		t.Fatalf("index outage = %d %s", down.Code, down.Body.String())
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
