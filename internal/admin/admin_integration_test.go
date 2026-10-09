//go:build integration

package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/github"
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

func TestHandoffAdminFlowUploadsPublishesAndListsProjectFiles(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "ledger", Name: "Ledger", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, signedIn := login(t, server, "correct horse", "")
	created := request(t, server, http.MethodPost, "/admin/api/handoffs", `{"title":"Backend handoff","description":"For the next agent","scope":"ledger","project_slug":"ledger","body":"Continue the implementation.","target":"Claude","draft":true}`, authed(signedIn, true))
	if created.Code != http.StatusCreated {
		t.Fatalf("create handoff = %d %s", created.Code, created.Body.String())
	}
	var payload struct {
		Handoff struct {
			ID string `json:"id"`
		} `json:"handoff"`
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil || payload.Handoff.ID == "" || len(payload.Messages) != 1 {
		t.Fatalf("created handoff = %s, %v", created.Body.String(), err)
	}

	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("attachment body"))
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/admin/api/handoff-messages/"+payload.Messages[0].ID+"/files", &upload)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Cookie", signedIn.cookie)
	req.Header.Set("Origin", testPublicURL)
	req.Header.Set("X-CSRF-Token", signedIn.csrf)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", res.Code, res.Body.String())
	}
	var file struct {
		ID        string `json:"id"`
		HandoffID string `json:"handoff_id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &file); err != nil || file.ID == "" || file.HandoffID != payload.Handoff.ID {
		t.Fatalf("uploaded file = %s, %v", res.Body.String(), err)
	}

	published := request(t, server, http.MethodPost, "/admin/api/handoff-messages/"+payload.Messages[0].ID+"/actions", `{"action":"publish"}`, authed(signedIn, true))
	var publishedPayload struct {
		WorkState string `json:"work_state"`
		Files     []struct {
			Filename  string `json:"filename"`
			HandoffID string `json:"handoff_id"`
		} `json:"files"`
	}
	if err := json.Unmarshal(published.Body.Bytes(), &publishedPayload); err != nil || published.Code != http.StatusOK || publishedPayload.WorkState != "ready" || len(publishedPayload.Files) != 1 || publishedPayload.Files[0].Filename != "notes.txt" || publishedPayload.Files[0].HandoffID != payload.Handoff.ID {
		t.Fatalf("publish = %d %s", published.Code, published.Body.String())
	}
	missing := request(t, server, http.MethodPost, "/admin/api/handoffs/999999/messages", `{"body":"missing parent"}`, authed(signedIn, true))
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "handoff item not found") {
		t.Fatalf("append missing handoff = %d %s", missing.Code, missing.Body.String())
	}
	listed := request(t, server, http.MethodGet, "/admin/api/handoffs?q=backend", "", authed(signedIn, false))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"title":"Backend handoff"`) {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	if invalid := request(t, server, http.MethodGet, "/admin/api/handoffs?target="+strings.Repeat("t", 101), "", authed(signedIn, false)); invalid.Code != http.StatusBadRequest {
		t.Fatalf("long target filter = %d %s", invalid.Code, invalid.Body.String())
	}
	projectFiles := request(t, server, http.MethodGet, "/admin/api/projects/ledger/files", "", authed(signedIn, false))
	if projectFiles.Code != http.StatusOK || !strings.Contains(projectFiles.Body.String(), `"filename":"notes.txt"`) {
		t.Fatalf("project files = %d %s", projectFiles.Code, projectFiles.Body.String())
	}
	download := request(t, server, http.MethodGet, "/admin/api/handoff-files/"+file.ID, "", authed(signedIn, false))
	if download.Code != http.StatusOK || download.Body.String() != "attachment body" || !strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("download = %d %q %#v", download.Code, download.Body.String(), download.Header())
	}
}

func TestHandoffExportIncludesMoreThanTenThousandMessages(t *testing.T) {
	db, ctx := testdb.Open(t)
	detail, err := db.CreateHandoff(ctx,
		store.Handoff{Title: "Large handoff", Source: "agent", ClientID: "client"},
		store.HandoffMessage{Body: "oldest-message-must-survive", WorkState: "ready", Source: "agent", ClientID: "client"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO handoff_message(handoff_id,body,work_state,source,client_id,status_updated_source,status_updated_client_id)
SELECT $1,'message-'||n,'ready','agent','client','agent','client' FROM generate_series(1,10000) n`, detail.Handoff.ID); err != nil {
		t.Fatal(err)
	}
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, signedIn := login(t, server, "correct horse", "")
	exported := request(t, server, http.MethodGet, "/admin/api/handoffs/"+strconv.FormatInt(detail.Handoff.ID, 10)+"/export", "", authed(signedIn, false))
	if exported.Code != http.StatusOK || !strings.Contains(exported.Body.String(), "oldest-message-must-survive") || !strings.Contains(exported.Body.String(), "message-10000") {
		t.Fatalf("export = %d, bytes=%d", exported.Code, exported.Body.Len())
	}
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
	if clients.Code != http.StatusOK || !strings.Contains(clients.Body.String(), `"client_name":"Agent A"`) || !strings.Contains(clients.Body.String(), `"active_access_tokens":1`) || !strings.Contains(clients.Body.String(), `"active_refresh_tokens":0`) {
		t.Fatalf("clients = %d %s", clients.Code, clients.Body.String())
	}
	for _, forbidden := range []string{"hash", "0101010101", "secret", `"refresh_token":`} {
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

func TestEntryTableFiltersPagesAndExportsCSV(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	for _, p := range []store.Project{{Slug: "atlas", Name: "Atlas", Tier: "focus"}, {Slug: "beacon", Name: "Beacon", Tier: "park"}} {
		if _, err := db.UpsertProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []struct{ slug, kind, body, source string }{
		{"atlas", "decision", "Use Postgres", "claude-code"},
		{"beacon", "todo", "=HYPERLINK(\"http://evil\")", "codex"},
		{"atlas", "note", "Ship the TABLE view", "codex"},
	} {
		if _, err := db.AppendEntry(ctx, e.slug, e.kind, e.body, e.source, "client-1"); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Entries []struct {
			ID          string `json:"id"`
			Body        string `json:"body"`
			ProjectName string `json:"project_name"`
		} `json:"entries"`
		Sources    []string `json:"sources"`
		NextBefore *string  `json:"next_before"`
	}
	get := func(path string) page {
		t.Helper()
		res := request(t, server, http.MethodGet, path, "", authed(s, false))
		var body page
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || res.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, res.Code, res.Body.String())
		}
		return body
	}
	first := get("/admin/api/entries?limit=2")
	if len(first.Entries) != 2 || first.Entries[0].Body != "Ship the TABLE view" || first.Entries[1].ProjectName != "Beacon" || first.NextBefore == nil || strings.Join(first.Sources, ",") != "claude-code,codex" {
		t.Fatalf("first page = %+v", first)
	}
	if rest := get("/admin/api/entries?limit=2&before=" + *first.NextBefore); len(rest.Entries) != 1 || rest.Entries[0].Body != "Use Postgres" || rest.NextBefore != nil {
		t.Fatalf("second page = %+v", rest)
	}
	for path, want := range map[string]string{
		"/admin/api/entries?project=atlas&source=codex": "Ship the TABLE view",
		"/admin/api/entries?kind=decision":              "Use Postgres",
		"/admin/api/entries?q=table":                    "Ship the TABLE view",
	} {
		if got := get(path); len(got.Entries) != 1 || got.Entries[0].Body != want {
			t.Errorf("%s = %+v", path, got)
		}
	}
	for _, path := range []string{"/admin/api/entries?kind=idea", "/admin/api/entries?project=Bad%20Slug", "/admin/api/entries?limit=0", "/admin/api/entries?before=x", "/admin/api/entries.csv?source=a%0Ab"} {
		if res := request(t, server, http.MethodGet, path, "", authed(s, false)); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", path, res.Code)
		}
	}

	res := request(t, server, http.MethodGet, "/admin/api/entries.csv?project=beacon", "", authed(s, false))
	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/csv") || !strings.Contains(res.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("csv = %d %v", res.Code, res.Header())
	}
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(res.Body.String(), "\ufeff")), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "Time (UTC),Project,") || !strings.Contains(lines[1], `,Beacon,beacon,todo,codex,,,,,,,,,,,open,"'=HYPERLINK(""http://evil"")",`) {
		t.Fatalf("csv body = %q", res.Body.String())
	}
}

func TestTodoResolutionAndProjectSummaries(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	todo, err := db.AppendEntry(ctx, "atlas", "todo", "Add CSV export", "codex", "c")
	if err != nil {
		t.Fatal(err)
	}
	note, err := db.AppendEntry(ctx, "atlas", "note", "Kickoff", "claude-code", "c")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveEntryMeta(ctx, todo.ID, store.EntryMeta{Title: "Add CSV export", Tags: []string{"export"}, Priority: "high"}); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(todo.ID, 10)
	if res := request(t, server, http.MethodPost, "/admin/api/entries/"+id+"/resolve", "", authed(s, false)); res.Code != http.StatusForbidden {
		t.Fatalf("resolve without CSRF = %d", res.Code)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/entries/"+strconv.FormatInt(note.ID, 10)+"/resolve", "", authed(s, true)); res.Code != http.StatusBadRequest {
		t.Fatalf("resolve note = %d", res.Code)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/entries/999999/resolve", "", authed(s, true)); res.Code != http.StatusNotFound {
		t.Fatalf("resolve missing = %d", res.Code)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/entries/"+id+"/reopen", "", authed(s, true)); res.Code != http.StatusConflict {
		t.Fatalf("reopen open todo = %d", res.Code)
	}
	summary := request(t, server, http.MethodGet, "/admin/api/table/projects", "", authed(s, false))
	if summary.Code != http.StatusOK || !strings.Contains(summary.Body.String(), `"open_todos":1`) || !strings.Contains(summary.Body.String(), `"week_agents":["claude-code","codex"]`) || !strings.Contains(summary.Body.String(), `"metadata":{"total":2,"ready":1,"failed":0,"active":false,"configured":false}`) {
		t.Fatalf("summary = %d %s", summary.Code, summary.Body.String())
	}

	resolved := request(t, server, http.MethodPost, "/admin/api/entries/"+id+"/resolve", "", authed(s, true))
	if resolved.Code != http.StatusCreated || !strings.Contains(resolved.Body.String(), `"body":"Done: Add CSV export"`) || !strings.Contains(resolved.Body.String(), `"kind":"status"`) {
		t.Fatalf("resolve = %d %s", resolved.Code, resolved.Body.String())
	}
	if res := request(t, server, http.MethodPost, "/admin/api/entries/"+id+"/resolve", "", authed(s, true)); res.Code != http.StatusConflict {
		t.Fatalf("resolve twice = %d", res.Code)
	}
	done := request(t, server, http.MethodGet, "/admin/api/entries?status=done&tag=export", "", authed(s, false))
	if done.Code != http.StatusOK || !strings.Contains(done.Body.String(), `"resolved_by":{"created_at":`) || !strings.Contains(done.Body.String(), `"origin":"owner"`) || !strings.Contains(done.Body.String(), `"tags":["export"]`) || !strings.Contains(done.Body.String(), `"priority":"high"`) {
		t.Fatalf("done todos = %s", done.Body.String())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/entries?status=later", "", authed(s, false)); res.Code != http.StatusBadRequest {
		t.Fatalf("bad status = %d", res.Code)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/entries/"+id+"/reopen", "", authed(s, true)); res.Code != http.StatusCreated || !strings.Contains(res.Body.String(), `"body":"Reopened: Add CSV export"`) {
		t.Fatalf("reopen = %d %s", res.Code, res.Body.String())
	}
	// The reversal is on the timeline, so the latest status no longer says "Done".
	if summary := request(t, server, http.MethodGet, "/admin/api/table/projects", "", authed(s, false)); !strings.Contains(summary.Body.String(), `"status_title":"Reopened: Add CSV export"`) {
		t.Fatalf("summary after reopen = %s", summary.Body.String())
	}
	if res := request(t, server, http.MethodPost, "/admin/api/entries/"+strconv.FormatInt(note.ID, 10)+"/reopen", "", authed(s, true)); res.Code != http.StatusConflict {
		t.Fatalf("reopen a note = %d", res.Code)
	}
	open := request(t, server, http.MethodGet, "/admin/api/entries?status=open", "", authed(s, false))
	if !strings.Contains(open.Body.String(), `"id":"`+id+`"`) || strings.Contains(open.Body.String(), "resolved_by") {
		t.Fatalf("reopened = %s", open.Body.String())
	}

	// Two sessions marking the same todo done at once create one completion.
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, _, err := db.ResolveTodo(ctx, todo.ID, "ledger-admin", "c")
			results <- err
		}()
	}
	first, second := <-results, <-results
	if (first == nil) == (second == nil) || !errors.Is(errors.Join(first, second), store.ErrAlreadyResolved) {
		t.Fatalf("concurrent resolve = %v, %v", first, second)
	}
	// A model guess for an already-closed todo keeps the rest of its metadata.
	if err := db.SaveEntryMeta(ctx, note.ID, store.EntryMeta{Title: "Kickoff", Resolves: &todo.ID}); err != nil {
		t.Fatalf("model resolution of a closed todo = %v", err)
	}
	var resolvers int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM entry_meta WHERE resolves=$1`, todo.ID).Scan(&resolvers); err != nil || resolvers != 1 {
		t.Fatalf("resolvers = %d %v", resolvers, err)
	}

	if err := db.Heartbeat(ctx, store.ExtractorHeartbeat); err != nil {
		t.Fatal(err)
	}
	if summary := request(t, server, http.MethodGet, "/admin/api/table/projects", "", authed(s, false)); !strings.Contains(summary.Body.String(), `"active":true`) {
		t.Fatalf("summary after heartbeat = %s", summary.Body.String())
	}
}

func TestRelatedEntriesDuplicatesAndDigestsThroughTheAPI(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	axis := func(values ...float32) []float32 { v := make([]float32, 8); copy(v, values); return v }
	ids := map[string]int64{}
	for _, e := range []struct {
		name, kind string
		vector     []float32
		failed     bool
	}{
		{"first", "status", axis(1), false},
		{"repeat", "status", axis(0.99, 0.1), false},
		// Closest to "repeat" but must link to the root, never form a chain.
		{"echo", "status", axis(0.97, 0.2), false},
		// Extraction failed, yet the repeat link must still reach the table.
		{"untitled", "status", axis(0.98, 0.15), true},
		{"cousin", "note", axis(0.7, 0.7), false},
		// Identical todos stay separate: each is resolved on its own.
		{"todo-a", "todo", axis(0, 0, 0, 1), false},
		{"todo-b", "todo", axis(0, 0, 0, 1), false},
		{"stranger", "note", axis(0, 0, 1), false},
	} {
		entry, err := db.AppendEntry(ctx, "atlas", e.kind, e.name, "codex", "c")
		if err != nil {
			t.Fatal(err)
		}
		ids[e.name] = entry.ID
		if e.failed {
			for range store.MetaMaxAttempts {
				if err := db.RecordMetaFailure(ctx, entry.ID, "m", "bad JSON"); err != nil {
					t.Fatal(err)
				}
			}
		} else if err := db.SaveEntryMeta(ctx, entry.ID, store.EntryMeta{Title: e.name}); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveEntryEmbedding(ctx, entry.ID, "embed", e.vector); err != nil {
			t.Fatal(err)
		}
	}
	link := func(threshold float64) int {
		t.Helper()
		checked := 0
		for {
			n, err := db.LinkDuplicates(ctx, "embed", threshold)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				return checked
			}
			checked++
		}
	}
	links := func() map[int64]int64 {
		t.Helper()
		rows, err := db.Pool.Query(ctx, `SELECT entry_id,duplicate_of FROM entry_meta WHERE duplicate_of IS NOT NULL`)
		if err != nil {
			t.Fatal(err)
		}
		out := map[int64]int64{}
		for rows.Next() {
			var id, of int64
			_ = rows.Scan(&id, &of)
			out[id] = of
		}
		return out
	}
	if n := link(0.9); n != 6 {
		t.Fatalf("checked %d entries", n)
	}
	if got, want := links(), map[int64]int64{ids["repeat"]: ids["first"], ids["echo"]: ids["first"], ids["untitled"]: ids["first"]}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("links = %v, want %v (all pointing at the root)", got, want)
	}
	if n := link(0.9); n != 0 {
		t.Fatalf("unchanged threshold rechecked %d entries", n)
	}
	// A stricter threshold rechecks everything: "repeat" (0.9950 to "first")
	// becomes a root of its own, and only "untitled" (0.9987 to it) folds.
	if n := link(0.995); n != 6 {
		t.Fatalf("threshold change rechecked %d entries", n)
	}
	if got := links(); len(got) != 1 || got[ids["untitled"]] != ids["repeat"] {
		t.Fatalf("links at 0.995 = %v", got)
	}
	if link(0.9) != 6 {
		t.Fatal("restoring the threshold did not recheck")
	}
	if err := db.SaveDigest(ctx, "atlas", "Shipped things.", 4, ids["stranger"], "m"); err != nil {
		t.Fatal(err)
	}
	entries := request(t, server, http.MethodGet, "/admin/api/entries", "", authed(s, false))
	if strings.Count(entries.Body.String(), `"duplicate_of":"`+strconv.FormatInt(ids["first"], 10)+`"`) != 3 || !strings.Contains(entries.Body.String(), `"body":"untitled","client_id":"c","context":"","created_at"`) {
		t.Fatalf("entries = %s", entries.Body.String())
	}
	related := request(t, server, http.MethodGet, "/admin/api/entries/"+strconv.FormatInt(ids["cousin"], 10)+"/related", "", authed(s, false))
	var body struct {
		Related []struct {
			Body       string  `json:"body"`
			Similarity float64 `json:"similarity"`
		} `json:"related"`
	}
	if err := json.Unmarshal(related.Body.Bytes(), &body); err != nil || related.Code != http.StatusOK || len(body.Related) != 1 || body.Related[0].Body != "first" || body.Related[0].Similarity < 0.7 {
		t.Fatalf("related = %d %s", related.Code, related.Body.String())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/entries/"+strconv.FormatInt(ids["first"], 10)+"/related", "", authed(s, false)); strings.Contains(res.Body.String(), `"body":"repeat"`) {
		t.Fatalf("own duplicate listed as related: %s", res.Body.String())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/entries/x/related", "", authed(s, false)); res.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d", res.Code)
	}
	summary := request(t, server, http.MethodGet, "/admin/api/table/projects", "", authed(s, false))
	if !strings.Contains(summary.Body.String(), `"digest":"Shipped things."`) || !strings.Contains(summary.Body.String(), `"digest_at":`) {
		t.Fatalf("summary = %s", summary.Body.String())
	}
	digests := func() int {
		var n int
		_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM project_digest`).Scan(&n)
		return n
	}
	// Rechecking with a threshold that yields the same links keeps the digest.
	if link(0.95); digests() != 1 {
		t.Fatal("digest dropped although no link changed")
	}
	// Changed links feed a different digest, so it is regenerated.
	if link(0.995); digests() != 0 {
		t.Fatal("digest kept after links changed")
	}
}

func TestFocusFieldsOwnerTriageAndInbox(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "site", Name: "Site", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	add := func(kind, body string, meta store.EntryMeta) int64 {
		t.Helper()
		e, err := db.AppendEntry(ctx, "site", kind, body, "codex", "c")
		if err != nil {
			t.Fatal(err)
		}
		if meta.Title == "" {
			meta.Title = body
		}
		if err := db.SaveEntryMeta(ctx, e.ID, meta); err != nil {
			t.Fatal(err)
		}
		return e.ID
	}
	soon := time.Now().AddDate(0, 0, 3).Format(time.DateOnly)
	later := time.Now().AddDate(0, 1, 0).Format(time.DateOnly)
	lowTodo := add("todo", "Tidy docs", store.EntryMeta{Priority: "low", Size: "S"})
	askTodo := add("todo", "Verify claims", store.EntryMeta{Priority: "high", Ask: "Confirm the claims"})
	highTodo := add("todo", "Fix login", store.EntryMeta{Priority: "high", Size: "M", Due: later})
	dueTodo := add("todo", "Send invoice", store.EntryMeta{Priority: "normal", Size: "S", Due: soon})
	ask := add("note", "Pricing question", store.EntryMeta{Importance: "important", Ask: "Confirm the pricing claims", Gist: "Two claims unverified"})
	add("status", "Checkpoint", store.EntryMeta{Importance: "routine", State: "done"})
	blocked := add("status", "Stuck on legal", store.EntryMeta{Importance: "important", State: "blocked", Blocker: "Waiting on legal", NextStep: "Email legal"})
	news := add("note", "Ollama release", store.EntryMeta{Link: "https://example.com/r", SourceName: "GitHub", Why: "Faster on Macs"})

	type row struct {
		ID    string `json:"id"`
		Meta  map[string]any
		Owner store.OwnerState `json:"owner"`
	}
	list := func(path string) []row {
		t.Helper()
		res := request(t, server, http.MethodGet, path, "", authed(s, false))
		var body struct{ Entries []row }
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || res.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, res.Code, res.Body.String())
		}
		return body.Entries
	}
	ids := func(rows []row) string {
		out := []string{}
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return strings.Join(out, ",")
	}
	id := func(v int64) string { return strconv.FormatInt(v, 10) }

	if got := ids(list("/admin/api/entries?needs=you")); got != id(ask)+","+id(askTodo) {
		t.Fatalf("needs=you = %s", got)
	}
	if got := ids(list("/admin/api/entries?state=blocked")); got != id(blocked) {
		t.Fatalf("state=blocked = %s", got)
	}
	if rows := list("/admin/api/entries?hide_routine=1"); len(rows) != 7 {
		t.Fatalf("hide_routine kept %d rows", len(rows))
	}
	if rows := list("/admin/api/entries?state=blocked"); rows[0].Meta["blocker"] != "Waiting on legal" || rows[0].Meta["next_step"] != "Email legal" || rows[0].Meta["importance"] != "important" {
		t.Fatalf("blocked meta = %v", rows[0].Meta)
	}
	if rows := list("/admin/api/entries?reading=unread"); ids(rows) != id(news) || rows[0].Meta["link"] != "https://example.com/r" || rows[0].Meta["source"] != "GitHub" {
		t.Fatalf("reading=unread = %v", rows)
	}

	owner := func(entry int64, body string, want int) store.OwnerState {
		t.Helper()
		res := request(t, server, http.MethodPost, "/admin/api/entries/"+id(entry)+"/owner", body, authed(s, true))
		var state store.OwnerState
		_ = json.Unmarshal(res.Body.Bytes(), &state)
		if res.Code != want {
			t.Fatalf("owner %s = %d %s", body, res.Code, res.Body.String())
		}
		return state
	}
	if res := request(t, server, http.MethodPost, "/admin/api/entries/"+id(news)+"/owner", `{"read":true}`, authed(s, false)); res.Code != http.StatusForbidden {
		t.Fatalf("owner without CSRF = %d", res.Code)
	}
	owner(news, `{}`, http.StatusBadRequest)
	owner(news, `{"snooze_days":31}`, http.StatusBadRequest)
	owner(999999, `{"read":true}`, http.StatusNotFound)
	if st := owner(news, `{"read":true,"starred":true}`, http.StatusOK); !st.Read || !st.Starred || st.Handled {
		t.Fatalf("read+star = %#v", st)
	}
	// Patches change only what they name.
	if st := owner(news, `{"starred":false}`, http.StatusOK); !st.Read || st.Starred {
		t.Fatalf("unstar = %#v", st)
	}
	if got := ids(list("/admin/api/entries?reading=unread")); got != "" {
		t.Fatalf("read entry still unread: %s", got)
	}
	if got := ids(list("/admin/api/entries?reading=all")); got != id(news) {
		t.Fatalf("reading=all = %s", got)
	}

	// The inbox: asks, then todos by urgency (due soon, then priority, then age).
	inbox := func() (asks, todos []row, total int, projects []store.ProjectSummary) {
		t.Helper()
		res := request(t, server, http.MethodGet, "/admin/api/inbox", "", authed(s, false))
		var body struct {
			NeedsYou   []row                  `json:"needs_you"`
			Todos      []row                  `json:"todos"`
			TodosTotal int                    `json:"todos_total"`
			Projects   []store.ProjectSummary `json:"projects"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || res.Code != http.StatusOK {
			t.Fatalf("inbox = %d %s", res.Code, res.Body.String())
		}
		return body.NeedsYou, body.Todos, body.TodosTotal, body.Projects
	}
	asks, todos, total, projects := inbox()
	// The asking todo appears once, under asks, not again under todos.
	if ids(asks) != id(ask)+","+id(askTodo) || ids(todos) != strings.Join([]string{id(dueTodo), id(highTodo), id(lowTodo)}, ",") || total != 3 {
		t.Fatalf("inbox asks=%s todos=%s total=%d", ids(asks), ids(todos), total)
	}
	if projects[0].NeedsYou != 2 || projects[0].StatusState != "blocked" {
		t.Fatalf("project health = %#v", projects[0])
	}
	// Snoozing hides an ask or todo; handling clears the ask for good.
	// A snoozed ask leaves both the inbox and the project's count.
	owner(ask, `{"snooze_days":1}`, http.StatusOK)
	if asks, _, _, projects := inbox(); ids(asks) != id(askTodo) || projects[0].NeedsYou != 1 {
		t.Fatalf("after snoozing an ask: asks=%s needs=%d", ids(asks), projects[0].NeedsYou)
	}
	// Console bookkeeping statuses ("Done: …") do not erase blocked health.
	if _, _, err := db.ResolveTodo(ctx, lowTodo, "ledger-admin", "c"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, projects := inbox(); projects[0].StatusState != "blocked" || projects[0].StatusDetail != "Waiting on legal" {
		t.Fatalf("health after a Done entry = %q %q", projects[0].StatusState, projects[0].StatusDetail)
	}
	if _, _, err := db.ReopenTodo(ctx, lowTodo, "ledger-admin", "c"); err != nil {
		t.Fatal(err)
	}
	owner(dueTodo, `{"snooze_days":2}`, http.StatusOK)
	owner(ask, `{"handled":true}`, http.StatusOK)
	owner(askTodo, `{"handled":true}`, http.StatusOK)
	asks, todos, total, projects = inbox()
	// Once handled, the todo returns to the todo list, highest priority first.
	if len(asks) != 0 || ids(todos) != id(askTodo)+","+id(highTodo)+","+id(lowTodo) || total != 3 || projects[0].NeedsYou != 0 {
		t.Fatalf("after triage asks=%s todos=%s total=%d needs=%d", ids(asks), ids(todos), total, projects[0].NeedsYou)
	}
	if st := owner(dueTodo, `{"snooze_days":0}`, http.StatusOK); st.SnoozedUntil != "" {
		t.Fatalf("unsnooze = %#v", st)
	}

	res := request(t, server, http.MethodGet, "/admin/api/entries.csv?state=blocked", "", authed(s, false))
	if !strings.Contains(res.Body.String(), "Gist,Importance,Asks you,Status,Next step,Due,Link") || !strings.Contains(res.Body.String(), ",important,,blocked,Email legal,,,") {
		t.Fatalf("csv = %s", res.Body.String())
	}
	for _, bad := range []string{"hide_routine=2", "needs=me", "reading=later", "state=stuck"} {
		if res := request(t, server, http.MethodGet, "/admin/api/entries?"+bad, "", authed(s, false)); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, res.Code)
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
	entriesOnly := request(t, server, http.MethodPost, "/admin/api/search", `{"q":"postgres","limit":3,"kind":"entry"}`, authed(s, true))
	if err := json.Unmarshal(entriesOnly.Body.Bytes(), &result); err != nil || len(result.Hits) != 2 || result.Hits[0].Kind == "project" || result.Hits[1].Kind == "project" || requested.Limit != 30 {
		t.Fatalf("entry search = %s (index limit %d)", entriesOnly.Body.String(), requested.Limit)
	}
	index.Close()
	down := request(t, server, http.MethodPost, "/admin/api/search", `{"q":"postgres"}`, authed(s, true))
	if down.Code != http.StatusServiceUnavailable || !strings.Contains(down.Body.String(), "search unavailable") || strings.Contains(down.Body.String(), "127.0.0.1") {
		t.Fatalf("index outage = %d %s", down.Code, down.Body.String())
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func TestUndoEachActionAndRestoreFromTrash(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	for _, p := range []store.Project{{Slug: "atlas", Name: "Atlas", Tier: "focus"}, {Slug: "beacon", Name: "Beacon", Tier: "park"}} {
		if _, err := db.UpsertProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	todo, _ := db.AppendEntry(ctx, "atlas", "todo", "Add export", "codex", "c")
	note, _ := db.AppendEntry(ctx, "atlas", "note", "Kickoff", "codex", "c")
	if err := db.SaveEntryMeta(ctx, note.ID, store.EntryMeta{Title: "Kickoff title", Importance: "important"}); err != nil {
		t.Fatal(err)
	}
	id := func(v int64) string { return strconv.FormatInt(v, 10) }
	call := func(method, path, body string, want int) map[string]any {
		t.Helper()
		res := request(t, server, method, path, body, authed(s, method != http.MethodGet))
		if res.Code != want {
			t.Fatalf("%s %s = %d %s", method, path, res.Code, res.Body.String())
		}
		out := map[string]any{}
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		return out
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	resolved := func() bool { return count(`SELECT count(*) FROM entry_meta WHERE resolves=$1`, todo.ID) == 1 }
	undo := func(action any, want int) {
		t.Helper()
		call(http.MethodPost, "/admin/api/actions/"+action.(string)+"/undo", "", want)
	}

	// Mark done, undo: the todo reopens and the Done entry is gone entirely.
	entries := count(`SELECT count(*) FROM entry`)
	done := call(http.MethodPost, "/admin/api/entries/"+id(todo.ID)+"/resolve", "", http.StatusCreated)
	if !resolved() || count(`SELECT count(*) FROM entry`) != entries+1 {
		t.Fatal("resolve did not close the todo")
	}
	undo(done["action_id"], http.StatusOK)
	if resolved() || count(`SELECT count(*) FROM entry`) != entries {
		t.Fatal("undoing done left the todo closed or the Done entry behind")
	}
	undo(done["action_id"], http.StatusConflict)

	// Reopen, undo: the same Done entry closes the todo again.
	call(http.MethodPost, "/admin/api/entries/"+id(todo.ID)+"/resolve", "", http.StatusCreated)
	reopened := call(http.MethodPost, "/admin/api/entries/"+id(todo.ID)+"/reopen", "", http.StatusCreated)
	undo(reopened["action_id"], http.StatusOK)
	if !resolved() || count(`SELECT count(*) FROM entry WHERE body LIKE 'Reopened:%'`) != 0 {
		t.Fatal("undoing reopen did not restore the resolution")
	}

	// Two triage actions undo independently; a stale undo is refused.
	first := call(http.MethodPost, "/admin/api/entries/"+id(note.ID)+"/owner", `{"read":true,"starred":true}`, http.StatusOK)
	second := call(http.MethodPost, "/admin/api/entries/"+id(note.ID)+"/owner", `{"starred":false}`, http.StatusOK)
	undo(first["action_id"], http.StatusConflict)
	undo(second["action_id"], http.StatusOK)
	if count(`SELECT count(*) FROM entry_owner_state WHERE entry_id=$1 AND starred AND read_at IS NOT NULL`, note.ID) != 1 {
		t.Fatal("undoing unstar did not restore the star")
	}
	undo(first["action_id"], http.StatusOK)
	if count(`SELECT count(*) FROM entry_owner_state WHERE entry_id=$1`, note.ID) != 0 {
		t.Fatal("undoing the first triage did not restore the untouched state")
	}
	actions := call(http.MethodGet, "/admin/api/actions", "", http.StatusOK)["actions"].([]any)
	// Newest first: resolve, resolve, reopen, then the two triage actions.
	if len(actions) != 5 || actions[0].(map[string]any)["label"] != "Unstarred: Kickoff title" || actions[1].(map[string]any)["label"] != "Marked read, Starred: Kickoff title" ||
		actions[1].(map[string]any)["undoable"] != false || actions[4].(map[string]any)["label"] != "Marked done: Add export" {
		t.Fatalf("actions = %v", actions)
	}

	// Delete an entry: it leaves every list and returns whole on restore.
	call(http.MethodPost, "/admin/api/entries/"+id(note.ID)+"/owner", `{"starred":true}`, http.StatusOK)
	trashed := call(http.MethodDelete, "/admin/api/entries/"+id(note.ID), "", http.StatusOK)
	if count(`SELECT count(*) FROM entry WHERE id=$1`, note.ID) != 0 || count(`SELECT count(*) FROM chunk_dirty WHERE ref=$1`, "entry:"+id(note.ID)) != 1 {
		t.Fatal("trashed entry still live or not queued for index cleanup")
	}
	items := call(http.MethodGet, "/admin/api/trash", "", http.StatusOK)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["label"] != "Kickoff title" {
		t.Fatalf("trash = %v", items)
	}
	call(http.MethodPost, "/admin/api/trash/"+trashed["trash_id"].(string)+"/restore", "", http.StatusOK)
	if count(`SELECT count(*) FROM entry e JOIN entry_meta m ON m.entry_id=e.id JOIN entry_owner_state o ON o.entry_id=e.id
		WHERE e.id=$1 AND m.title='Kickoff title' AND m.importance='important' AND o.starred`, note.ID) != 1 {
		t.Fatal("restored entry lost its metadata or triage state")
	}
	undo(trashed["action_id"], http.StatusConflict)

	// Deleting a done todo drops the link; restoring it closes it again.
	todoTrash := call(http.MethodDelete, "/admin/api/entries/"+id(todo.ID), "", http.StatusOK)
	undo(todoTrash["action_id"], http.StatusOK)
	if !resolved() {
		t.Fatal("restored todo is not closed by its Done entry")
	}

	// Deleting a project needs the slug repeated, and moves everything.
	h, err := db.CreateHandoff(ctx, store.Handoff{ProjectSlug: "atlas", Title: "Plan", Description: "d", Scope: "s", Source: "codex", ClientID: "c"}, store.HandoffMessage{Body: "b", WorkState: "ready", Source: "codex", ClientID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	preview := call(http.MethodGet, "/admin/api/projects/atlas/deletion", "", http.StatusOK)
	if preview["name"] != "Atlas" || preview["entries"] != float64(3) || preview["handoffs"] != float64(1) {
		t.Fatalf("preview = %v", preview)
	}
	call(http.MethodDelete, "/admin/api/projects/atlas", `{"confirm":"atlas-typo"}`, http.StatusBadRequest)
	gone := call(http.MethodDelete, "/admin/api/projects/atlas", `{"confirm":"atlas"}`, http.StatusOK)
	if count(`SELECT count(*) FROM entry WHERE slug='atlas'`) != 0 || count(`SELECT count(*) FROM handoff WHERE id=$1 AND project_slug IS NULL`, h.Handoff.ID) != 1 {
		t.Fatal("project delete left entries or kept the handoff link")
	}
	undo(gone["action_id"], http.StatusOK)
	if count(`SELECT count(*) FROM entry WHERE slug='atlas'`) != 3 || !resolved() || count(`SELECT count(*) FROM handoff WHERE id=$1 AND project_slug='atlas'`, h.Handoff.ID) != 1 {
		t.Fatal("project restore lost entries, resolutions, or the handoff link")
	}

	// An entry cannot come back into a deleted project.
	orphan := call(http.MethodDelete, "/admin/api/entries/"+id(note.ID), "", http.StatusOK)
	call(http.MethodDelete, "/admin/api/projects/atlas", `{"confirm":"atlas"}`, http.StatusOK)
	call(http.MethodPost, "/admin/api/trash/"+orphan["trash_id"].(string)+"/restore", "", http.StatusConflict)

	// Past retention, trash is purged for good.
	if _, err := db.Pool.Exec(ctx, `UPDATE trash SET deleted_at=now()-interval '31 days' WHERE id=$1`, orphan["trash_id"]); err != nil {
		t.Fatal(err)
	}
	if n, err := db.PurgeTrash(ctx); err != nil || n != 1 {
		t.Fatalf("purge = %d %v", n, err)
	}
	if n, _ := db.PurgeTrash(ctx); n != 0 {
		t.Fatal("second purge removed more")
	}
	if len(call(http.MethodGet, "/admin/api/trash", "", http.StatusOK)["items"].([]any)) != 1 {
		t.Fatal("unexpired project should remain in trash")
	}
	// A permanently deleted item no longer offers Undo.
	undoable := func(actionID any) any {
		for _, a := range call(http.MethodGet, "/admin/api/actions", "", http.StatusOK)["actions"].([]any) {
			if a.(map[string]any)["id"] == actionID {
				return a.(map[string]any)["undoable"]
			}
		}
		return nil
	}
	if undoable(orphan["action_id"]) != false {
		t.Fatal("purged deletion still offers undo")
	}
}

func TestUndoRefusesTriageRedoneSince(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	note, _ := db.AppendEntry(ctx, "atlas", "note", "Kickoff", "codex", "c")
	path := "/admin/api/entries/" + strconv.FormatInt(note.ID, 10) + "/owner"
	act := func(body string) string {
		res := request(t, server, http.MethodPost, path, body, authed(s, true))
		out := map[string]any{}
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		return out["action_id"].(string)
	}
	// Read, unread, read again: the state matches the first read in kind but
	// not in time, so undoing the first read must not clobber the later one.
	first := act(`{"read":true}`)
	act(`{"read":false}`)
	act(`{"read":true}`)
	if res := request(t, server, http.MethodPost, "/admin/api/actions/"+first+"/undo", "", authed(s, true)); res.Code != http.StatusConflict {
		t.Fatalf("stale undo = %d %s", res.Code, res.Body.String())
	}
	// Star, unstar, star: same value, but a later change all the same.
	star := act(`{"starred":true}`)
	act(`{"starred":false}`)
	act(`{"starred":true}`)
	if res := request(t, server, http.MethodPost, "/admin/api/actions/"+star+"/undo", "", authed(s, true)); res.Code != http.StatusConflict {
		t.Fatalf("ABA undo = %d %s", res.Code, res.Body.String())
	}
	// Actions past the undo window are purged.
	if _, err := db.Pool.Exec(ctx, `UPDATE owner_action SET created_at=now()-interval '8 days' WHERE id=$1`, star); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PurgeTrash(ctx); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM owner_action WHERE id=$1`, star).Scan(&left); err != nil || left != 0 {
		t.Fatalf("expired action kept: %d %v", left, err)
	}
	// Triage of a deleted entry cannot be undone until it is restored.
	unread := act(`{"read":false}`)
	trashID, _, err := db.TrashEntry(ctx, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/actions/"+unread+"/undo", "", authed(s, true)); res.Code != http.StatusConflict {
		t.Fatalf("undo on deleted entry = %d %s", res.Code, res.Body.String())
	}
	if err := db.RestoreTrash(ctx, trashID); err != nil {
		t.Fatal(err)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/actions/"+unread+"/undo", "", authed(s, true)); res.Code != http.StatusOK {
		t.Fatalf("undo after restore = %d %s", res.Code, res.Body.String())
	}

	// Undoing a reopen of a todo deleted since is a conflict, not a failure.
	todo, _ := db.AppendEntry(ctx, "atlas", "todo", "Temporary todo", "codex", "c")
	if _, _, err := db.ResolveTodo(ctx, todo.ID, "ledger-admin", "c"); err != nil {
		t.Fatal(err)
	}
	_, reopenID, err := db.ReopenTodo(ctx, todo.ID, "ledger-admin", "c")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.TrashEntry(ctx, todo.ID); err != nil {
		t.Fatal(err)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/actions/"+strconv.FormatInt(reopenID, 10)+"/undo", "", authed(s, true)); res.Code != http.StatusConflict {
		t.Fatalf("undo reopen of deleted todo = %d %s", res.Code, res.Body.String())
	}

	// An idempotent write stays idempotent after its entry is restored.
	once, err := db.AppendEntryOnce(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Glass capture", Source: "glass", ClientID: "c"}, "request-0001")
	if err != nil {
		t.Fatal(err)
	}
	onceTrash, _, err := db.TrashEntry(ctx, once.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendEntryOnce(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Glass capture", Source: "glass", ClientID: "c"}, "request-0001"); !errors.Is(err, store.ErrEntryTrashed) {
		t.Fatalf("retry while trashed = %v", err)
	}
	if err := db.RestoreTrash(ctx, onceTrash); err != nil {
		t.Fatal(err)
	}
	if retry, err := db.AppendEntryOnce(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Glass capture", Source: "glass", ClientID: "c"}, "request-0001"); err != nil || retry.ID != once.ID {
		t.Fatalf("retry after restore = %v %v, want entry %d", retry.ID, err, once.ID)
	}
}

func TestTrashKeepsResolutionsAndProjectIdentity(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	resolved := func(todo int64) bool {
		var n int
		_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM entry_meta WHERE resolves=$1`, todo).Scan(&n)
		return n == 1
	}
	// Todo and the entry that closed it, trashed separately, restored in either order.
	for _, c := range []struct{ trashTodoFirst, todoFirst bool }{{true, true}, {true, false}, {false, true}, {false, false}} {
		todoFirst := c.todoFirst
		todo, _ := db.AppendEntry(ctx, "atlas", "todo", "Add export", "codex", "c")
		done, _, err := db.ResolveTodo(ctx, todo.ID, "ledger-admin", "c")
		if err != nil {
			t.Fatal(err)
		}
		var todoTrash, doneTrash int64
		trash := []struct {
			id  int64
			out *int64
		}{{todo.ID, &todoTrash}, {done.ID, &doneTrash}}
		if !c.trashTodoFirst {
			trash[0], trash[1] = trash[1], trash[0]
		}
		for _, item := range trash {
			if *item.out, _, err = db.TrashEntry(ctx, item.id); err != nil {
				t.Fatal(err)
			}
		}
		order := []int64{todoTrash, doneTrash}
		if !todoFirst {
			order = []int64{doneTrash, todoTrash}
		}
		for _, id := range order {
			if err := db.RestoreTrash(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
		if !resolved(todo.ID) {
			t.Fatalf("todo reopened after restoring both (%+v)", c)
		}
	}
	// An entry does not go back into a different project with its slug.
	note, _ := db.AppendEntry(ctx, "atlas", "note", "Kickoff", "codex", "c")
	noteTrash, _, err := db.TrashEntry(ctx, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.TrashProject(ctx, "atlas"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "New Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	if err := db.RestoreTrash(ctx, noteTrash); !errors.Is(err, store.ErrProjectGone) {
		t.Fatalf("restore into replacement project = %v", err)
	}
}

func TestOwnerLabelsSurviveReextractionAndTeachTheExtractor(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	entry, _ := db.AppendEntry(ctx, "atlas", "note", "Pricing claims on the site are unverified", "codex", "c")
	path := "/admin/api/entries/" + strconv.FormatInt(entry.ID, 10) + "/labels"
	call := func(body string, want int) {
		t.Helper()
		if res := request(t, server, http.MethodPost, path, body, authed(s, true)); res.Code != want {
			t.Fatalf("POST %s = %d %s", body, res.Code, res.Body.String())
		}
	}
	meta := func() store.EntryMeta {
		t.Helper()
		rows, err := db.ListEntries(ctx, store.EntryFilter{ProjectSlug: "atlas"})
		if err != nil || len(rows) != 1 || rows[0].Meta == nil {
			t.Fatalf("rows=%v err=%v", rows, err)
		}
		return *rows[0].Meta
	}
	call(`{"set":{"title":"Too early"}}`, http.StatusConflict)
	model := store.EntryMeta{Title: "Pricing note", Importance: "routine", Tags: []string{"web"}, Unsure: []string{"importance", "ask"}}
	if err := db.SaveEntryMeta(ctx, entry.ID, model); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{}`, `{"set":{"model":"x"}}`, `{"set":{"importance":"urgent"}}`, `{"set":{"title":" "}}`,
		`{"set":{"tags":["a","b","c","d","e"]}}`, `{"set":{"due":"soon"}}`, `{"reset":["title"],"set":{"title":"x"}}`} {
		call(bad, http.StatusBadRequest)
	}
	repeat, _ := db.AppendEntry(ctx, "atlas", "note", "Pricing claims still unverified", "codex", "c")
	if err := db.SaveEntryMeta(ctx, repeat.ID, store.EntryMeta{Title: "Pricing again"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE entry_meta SET duplicate_of=$1,duplicate_checked=true WHERE entry_id=$2`, entry.ID, repeat.ID); err != nil {
		t.Fatal(err)
	}
	call(`{"set":{"title":"Pricing note"}}`, http.StatusOK)
	var rechecked bool
	if err := db.Pool.QueryRow(ctx, `SELECT duplicate_of IS NULL AND NOT duplicate_checked FROM entry_meta WHERE entry_id=$1`, repeat.ID).Scan(&rechecked); err != nil || !rechecked {
		t.Fatalf("repeat of edited root not requeued: %v", err)
	}
	// A re-extraction of the root requeues its repeats the same way.
	if _, err := db.Pool.Exec(ctx, `UPDATE entry_meta SET duplicate_of=$1,duplicate_checked=true WHERE entry_id=$2`, entry.ID, repeat.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveEntryMeta(ctx, entry.ID, model); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT duplicate_of IS NULL AND NOT duplicate_checked FROM entry_meta WHERE entry_id=$1`, repeat.ID).Scan(&rechecked); err != nil || !rechecked {
		t.Fatalf("repeat of re-extracted root not requeued: %v", err)
	}
	if _, _, err := db.TrashEntry(ctx, repeat.ID); err != nil {
		t.Fatal(err)
	}
	call(`{"reset":["title"]}`, http.StatusOK)
	call(`{"set":{"importance":"important","ask":"Confirm the pricing","tags":["Pricing","web"],"category":"Marketing Site"}}`, http.StatusOK)
	got := meta()
	if got.Importance != "important" || got.Ask != "Confirm the pricing" || !reflect.DeepEqual(got.Tags, []string{"pricing", "web"}) || got.Category != "marketing site" ||
		!reflect.DeepEqual(got.Edited, []string{"ask", "category", "importance", "tags"}) || len(got.Unsure) != 0 || got.Title != "Pricing note" {
		t.Fatalf("after edit: %#v", got)
	}
	// A later extraction keeps the owner's corrections.
	model.Title = "Pricing claims unverified"
	if err := db.SaveEntryMeta(ctx, entry.ID, model); err != nil {
		t.Fatal(err)
	}
	if got := meta(); got.Importance != "important" || got.Ask != "Confirm the pricing" || got.Title != "Pricing claims unverified" {
		t.Fatalf("after re-extraction: %#v", got)
	}
	examples, err := db.LabelExamples(ctx, "atlas", "note", 0, 4)
	if err != nil || len(examples) != 1 || !strings.Contains(string(examples[0].Corrected), `"importance": "important"`) {
		t.Fatalf("examples=%s err=%v", examples, err)
	}
	if categories, err := db.ProjectCategories(ctx, "atlas", 5); err != nil || !reflect.DeepEqual(categories, []string{"marketing site"}) {
		t.Fatalf("categories=%v err=%v", categories, err)
	}
	// Corrections survive Trash and restore.
	trashID, _, err := db.TrashEntry(ctx, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RestoreTrash(ctx, trashID); err != nil {
		t.Fatal(err)
	}
	if got := meta(); got.Importance != "important" || len(got.Edited) != 4 {
		t.Fatalf("after restore: %#v", got)
	}
	// Reset restores the model's own values at once, with no re-extraction.
	call(`{"reset":["importance","ask","tags","category"]}`, http.StatusOK)
	var overrides int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM entry_meta_override`).Scan(&overrides); err != nil || overrides != 0 {
		t.Fatalf("overrides=%d err=%v", overrides, err)
	}
	if got := meta(); len(got.Edited) != 0 || got.Importance != "routine" || got.Ask != "" || got.Category != "" || !reflect.DeepEqual(got.Tags, []string{"web"}) ||
		!reflect.DeepEqual(got.Unsure, []string{"importance", "ask"}) {
		t.Fatalf("after reset: %#v", got)
	}
	// Owner tags follow tag merges.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO tag_vocab(tag,canonical) VALUES('pricing',NULL),('prices','pricing')`); err != nil {
		t.Fatal(err)
	}
	call(`{"set":{"tags":["prices","web"]}}`, http.StatusOK)
	if got := meta(); !reflect.DeepEqual(got.Tags, []string{"pricing", "web"}) {
		t.Fatalf("alias kept: %v", got.Tags)
	}
	// The console's own bookkeeping entries have no AI labels to correct.
	todo, _ := db.AppendEntry(ctx, "atlas", "todo", "Add export", "codex", "c")
	done, _, err := db.ResolveTodo(ctx, todo.ID, "ledger-admin", "c")
	if err != nil {
		t.Fatal(err)
	}
	path = "/admin/api/entries/" + strconv.FormatInt(done.ID, 10) + "/labels"
	call(`{"set":{"title":"x"}}`, http.StatusConflict)
	// Items trashed before these columns existed still restore.
	legacy, _, err := db.TrashEntry(ctx, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE trash SET payload=jsonb_set(payload,'{meta}',(payload->'meta')-'category'-'details'-'unsure'-'edited') WHERE id=$1`, legacy); err != nil {
		t.Fatal(err)
	}
	if err := db.RestoreTrash(ctx, legacy); err != nil {
		t.Fatalf("legacy restore: %v", err)
	}
}

func TestEntryPageReturnsEntryAndItsRepeats(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	root, _ := db.AppendEntry(ctx, "atlas", "note", "Pricing claims are unverified", "codex", "c")
	repeat, _ := db.AppendEntry(ctx, "atlas", "note", "Pricing claims still unverified", "claude-code", "c")
	for id, title := range map[int64]string{root.ID: "Pricing unverified", repeat.ID: "Pricing still unverified"} {
		if err := db.SaveEntryMeta(ctx, id, store.EntryMeta{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE entry_meta SET duplicate_of=$1,duplicate_checked=true WHERE entry_id=$2`, root.ID, repeat.ID); err != nil {
		t.Fatal(err)
	}
	get := func(path string, want int) map[string]any {
		t.Helper()
		res := request(t, server, http.MethodGet, path, "", authed(s, false))
		if res.Code != want {
			t.Fatalf("GET %s = %d %s", path, res.Code, res.Body.String())
		}
		out := map[string]any{}
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		return out
	}
	id := func(v int64) string { return strconv.FormatInt(v, 10) }
	page := get("/admin/api/entries/"+id(root.ID), http.StatusOK)
	repeats, _ := page["repeats"].([]any)
	if page["project_name"] != "Atlas" || page["meta"].(map[string]any)["title"] != "Pricing unverified" || len(repeats) != 1 || repeats[0].(map[string]any)["id"] != id(repeat.ID) || page["repeats_total"] != float64(1) {
		t.Fatalf("root page = %v", page)
	}
	if page := get("/admin/api/entries/"+id(repeat.ID), http.StatusOK); page["duplicate_of"] != id(root.ID) || len(page["repeats"].([]any)) != 0 {
		t.Fatalf("repeat page = %v", page)
	}
	get("/admin/api/entries/999999", http.StatusNotFound)
	get("/admin/api/entries/abc", http.StatusBadRequest)
}

func TestAgentsSummarizeWhatEachAgentDid(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	for _, p := range []store.Project{{Slug: "atlas", Name: "Atlas", Tier: "focus"}, {Slug: "beacon", Name: "Beacon", Tier: "park"}} {
		if _, err := db.UpsertProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	ask, _ := db.AppendEntry(ctx, "atlas", "note", "Please confirm the pricing", "codex", "c")
	if err := db.SaveEntryMeta(ctx, ask.ID, store.EntryMeta{Title: "Pricing question", Ask: "Confirm the pricing"}); err != nil {
		t.Fatal(err)
	}
	_, _ = db.AppendEntry(ctx, "beacon", "status", "Shipped the phone build", "codex", "c")
	old, _ := db.AppendEntry(ctx, "beacon", "note", "An old note", "claude-code", "c")
	if _, err := db.Pool.Exec(ctx, `UPDATE entry SET created_at=now()-interval '30 days' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	_, _ = db.AppendEntry(ctx, "atlas", "note", "Written by the owner", "ledger-admin", "c")
	h, err := db.CreateHandoff(ctx, store.Handoff{ProjectSlug: "atlas", Title: "Plan", Description: "d", Scope: "s", Source: "codex", ClientID: "c"}, store.HandoffMessage{Body: "b", WorkState: "ready", Source: "codex", ClientID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE handoff_message SET work_state='in_progress',claimed_at=now(),claimed_source='claude-code',claimed_client_id='c' WHERE handoff_id=$1`, h.Handoff.ID); err != nil {
		t.Fatal(err)
	}
	res := request(t, server, http.MethodGet, "/admin/api/agents", "", authed(s, false))
	var out struct {
		Agents []struct {
			Name        string `json:"name"`
			WeekEntries int    `json:"week_entries"`
			Entries     int    `json:"entries"`
			OpenAsks    int    `json:"open_asks"`
			Handoffs    int    `json:"handoffs"`
			Projects    []struct{ Slug string }
			Latest      []map[string]any
		}
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &out) != nil || len(out.Agents) != 2 {
		t.Fatalf("agents = %d %s", res.Code, res.Body.String())
	}
	// Holding a handoff just now makes claude-code the most recently active.
	codex, claude := out.Agents[1], out.Agents[0]
	if codex.Name != "codex" || codex.WeekEntries != 2 || codex.OpenAsks != 1 || len(codex.Projects) != 2 || len(codex.Latest) != 2 || codex.Handoffs != 0 {
		t.Fatalf("codex = %+v", codex)
	}
	if claude.Name != "claude-code" || claude.WeekEntries != 0 || claude.Entries != 1 || len(claude.Projects) != 0 || claude.Handoffs != 1 {
		t.Fatalf("claude = %+v", claude)
	}
	// Two active messages in one handoff are one handoff; holding it counts as activity.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO handoff_message(handoff_id,body,work_state,source,client_id,claimed_at,claimed_source,claimed_client_id,status_updated_source,status_updated_client_id)
VALUES($1,'more','in_progress','codex','c',now(),'claude-code','c','claude-code','c')`, h.Handoff.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE entry SET created_at=now()-interval '40 days' WHERE source='codex'`); err != nil {
		t.Fatal(err)
	}
	agents, err := db.Agents(ctx, "ledger-admin")
	if err != nil || agents[0].Name != "claude-code" || agents[0].Handoffs != 1 || agents[0].LastActive == nil || time.Since(*agents[0].LastActive) > time.Hour {
		t.Fatalf("handoff activity: %+v %v", agents, err)
	}
	// Handling the ask clears it from the agent's count.
	if _, _, err := db.SetOwnerState(ctx, ask.ID, store.OwnerPatch{Handled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	agents, err = db.Agents(ctx, "ledger-admin")
	if err != nil || agents[len(agents)-1].Name != "codex" || agents[len(agents)-1].OpenAsks != 0 {
		t.Fatalf("after handling: %+v %v", agents, err)
	}
}

func TestAgentsLatestPrefersRealWorkOverRoutine(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	write := func(body, importance string) {
		t.Helper()
		e, err := db.AppendEntry(ctx, "atlas", "status", body, "codex", "c")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SaveEntryMeta(ctx, e.ID, store.EntryMeta{Title: body, Importance: importance}); err != nil {
			t.Fatal(err)
		}
	}
	write("Chose pgvector", "important")
	for _, body := range []string{"Checkpoint 1", "Checkpoint 2", "Checkpoint 3"} {
		write(body, "routine")
	}
	latest := func() []string {
		t.Helper()
		res := request(t, server, http.MethodGet, "/admin/api/agents", "", authed(s, false))
		var out struct {
			Agents []struct {
				Latest []struct{ Body string }
			}
		}
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &out) != nil || len(out.Agents) != 1 {
			t.Fatalf("agents = %d %s", res.Code, res.Body.String())
		}
		bodies := []string{}
		for _, entry := range out.Agents[0].Latest {
			bodies = append(bodies, entry.Body)
		}
		return bodies
	}
	// Routine checkpoints only fill the places left, newest first, and never push the decision out.
	if got := latest(); !reflect.DeepEqual(got, []string{"Checkpoint 3", "Checkpoint 2", "Chose pgvector"}) {
		t.Fatalf("latest = %v", got)
	}
	for _, body := range []string{"Shipped search", "Fixed the export", "Wrote the docs"} {
		write(body, "useful")
	}
	write("Checkpoint 4", "routine")
	if got := latest(); !reflect.DeepEqual(got, []string{"Wrote the docs", "Fixed the export", "Shipped search"}) {
		t.Fatalf("latest with enough real work = %v", got)
	}
}

func TestHandoffListsCarryResearchStatus(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	research := func(title string, draft bool) {
		t.Helper()
		if _, err := db.CreateResearchTask(ctx, store.NewResearchTask{ProjectSlug: "atlas", Title: title, Draft: draft, Source: writeSource, ClientID: "c",
			Spec: store.ResearchSpec{Objective: "Compare vector databases", Acceptance: []string{"Three options"}}}); err != nil {
			t.Fatal(err)
		}
	}
	research("Queued study", false)
	research("Draft study", true)
	if _, err := db.CreateHandoff(ctx, store.Handoff{ProjectSlug: "atlas", Title: "Plan", Description: "d", Scope: "s", Source: "codex", ClientID: "c"}, store.HandoffMessage{Body: "b", WorkState: "ready", Source: "codex", ClientID: "c"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/api/handoffs", "/admin/api/handoffs?project=atlas"} {
		res := request(t, server, http.MethodGet, path, "", authed(s, false))
		var out struct {
			Handoffs []map[string]any
		}
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &out) != nil || len(out.Handoffs) != 3 {
			t.Fatalf("%s = %d %s", path, res.Code, res.Body.String())
		}
		statuses := map[string]any{}
		for _, h := range out.Handoffs {
			status, ok := h["research_status"]
			if h["kind"] == "general" && ok {
				t.Fatalf("%s: general handoff has a research status: %v", path, h)
			}
			statuses[h["title"].(string)] = status
		}
		if statuses["Queued study"] != "queued" || statuses["Draft study"] != "draft" || statuses["Plan"] != nil {
			t.Fatalf("%s: research statuses = %v", path, statuses)
		}
	}
}

func TestLabellingStatusSaysWhyItIsPaused(t *testing.T) {
	db, ctx := testdb.Open(t)
	progress := func() store.MetaProgress {
		t.Helper()
		p, err := db.MetaProgress(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := progress(); p.Configured || p.Active || p.Problem != "" {
		t.Fatalf("no extractor yet: %+v", p)
	}
	if err := db.Heartbeat(ctx, store.ExtractorHeartbeat); err != nil {
		t.Fatal(err)
	}
	if err := db.SetWorkerProblem(ctx, store.ExtractorHeartbeat, "can't reach the AI model"); err != nil {
		t.Fatal(err)
	}
	if p := progress(); !p.Configured || !p.Active || p.Problem != "can't reach the AI model" {
		t.Fatalf("model down: %+v", p)
	}
	if err := db.SetWorkerProblem(ctx, store.ExtractorHeartbeat, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE worker_heartbeat SET seen_at=now()-interval '10 minutes'`); err != nil {
		t.Fatal(err)
	}
	if p := progress(); !p.Configured || p.Active || p.Problem != "" {
		t.Fatalf("stopped: %+v", p)
	}
}

func ptr[T any](v T) *T { return &v }

func TestMarkAllReadIsOneUndoableAction(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	for _, p := range []store.Project{{Slug: "atlas", Name: "Atlas", Tier: "focus"}, {Slug: "beacon", Name: "Beacon", Tier: "park"}} {
		if _, err := db.UpsertProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	news := func(slug, title string) int64 {
		e, _ := db.AppendEntry(ctx, slug, "note", title, "claude-code", "c")
		if err := db.SaveEntryMeta(ctx, e.ID, store.EntryMeta{Title: title, Link: "https://example.com/" + strconv.FormatInt(e.ID, 10)}); err != nil {
			t.Fatal(err)
		}
		return e.ID
	}
	first, second, other := news("atlas", "One"), news("atlas", "Two"), news("beacon", "Three")
	already := news("atlas", "Already read")
	if _, _, err := db.SetOwnerState(ctx, already, store.OwnerPatch{Read: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	readCount := func() int {
		var n int
		_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM entry_owner_state WHERE read_at IS NOT NULL`).Scan(&n)
		return n
	}
	res := request(t, server, http.MethodPost, "/admin/api/reading/read-all?project=atlas", "", authed(s, true))
	var out struct {
		Count    int    `json:"count"`
		ActionID string `json:"action_id"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &out) != nil || out.Count != 2 || out.ActionID == "" || readCount() != 3 {
		t.Fatalf("read all = %d %s read=%d", res.Code, res.Body.String(), readCount())
	}
	var label string
	if err := db.Pool.QueryRow(ctx, `SELECT label FROM owner_action WHERE id=$1`, out.ActionID).Scan(&label); err != nil || label != "Marked 2 read" {
		t.Fatalf("label = %q %v", label, err)
	}
	// Nothing left to mark: no write and no action.
	if res := request(t, server, http.MethodPost, "/admin/api/reading/read-all?project=atlas", "", authed(s, true)); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"count":0`) || strings.Contains(res.Body.String(), "action_id") {
		t.Fatalf("second read all = %d %s", res.Code, res.Body.String())
	}
	// One of them changes afterwards: undo restores the other and leaves it.
	if _, _, err := db.SetOwnerState(ctx, second, store.OwnerPatch{Starred: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/actions/"+out.ActionID+"/undo", "", authed(s, true)); res.Code != http.StatusOK {
		t.Fatalf("undo = %d %s", res.Code, res.Body.String())
	}
	var firstRead, secondRead, otherRead bool
	if err := db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM entry_owner_state WHERE entry_id=$1 AND read_at IS NOT NULL),
 EXISTS (SELECT 1 FROM entry_owner_state WHERE entry_id=$2 AND read_at IS NOT NULL),
 EXISTS (SELECT 1 FROM entry_owner_state WHERE entry_id=$3 AND read_at IS NOT NULL)`, first, second, other).Scan(&firstRead, &secondRead, &otherRead); err != nil {
		t.Fatal(err)
	}
	if firstRead || !secondRead || otherRead {
		t.Fatalf("after undo: first=%v second=%v other=%v", firstRead, secondRead, otherRead)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/actions/"+out.ActionID+"/undo", "", authed(s, true)); res.Code != http.StatusConflict {
		t.Fatalf("second undo = %d %s", res.Code, res.Body.String())
	}
}

func TestEntriesCanLeaveOutSnoozedOnes(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	awake, _ := db.AppendEntry(ctx, "atlas", "todo", "Due soon", "codex", "c")
	snoozed, _ := db.AppendEntry(ctx, "atlas", "todo", "Snoozed", "codex", "c")
	if _, _, err := db.SetOwnerState(ctx, snoozed.ID, store.OwnerPatch{SnoozeDays: ptr(3)}); err != nil {
		t.Fatal(err)
	}
	res := request(t, server, http.MethodGet, "/admin/api/entries?kind=todo&status=open&awake=1", "", authed(s, false))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"id":"`+strconv.FormatInt(awake.ID, 10)+`"`) || strings.Contains(res.Body.String(), `"id":"`+strconv.FormatInt(snoozed.ID, 10)+`"`) {
		t.Fatalf("awake = %d %s", res.Code, res.Body.String())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/entries?awake=2", "", authed(s, false)); res.Code != http.StatusBadRequest {
		t.Fatalf("bad awake = %d", res.Code)
	}
}

func TestEntriesCanKeepOnlyOverdueOnes(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	todo := func(body, due string) string {
		e, err := db.AppendEntry(ctx, "atlas", "todo", body, "codex", "c")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SaveEntryMeta(ctx, e.ID, store.EntryMeta{Title: body, Due: due}); err != nil {
			t.Fatal(err)
		}
		return `"id":"` + strconv.FormatInt(e.ID, 10) + `"`
	}
	today := time.Now()
	late := todo("Late", today.AddDate(0, 0, -1).Format(time.DateOnly))
	dueToday := todo("Today", today.Format(time.DateOnly))
	undated := todo("Undated", "")
	res := request(t, server, http.MethodGet, "/admin/api/entries?kind=todo&status=open&due_before="+today.Format(time.DateOnly), "", authed(s, false))
	if body := res.Body.String(); res.Code != http.StatusOK || !strings.Contains(body, late) || strings.Contains(body, dueToday) || strings.Contains(body, undated) {
		t.Fatalf("overdue = %d %s", res.Code, body)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/entries?due_before=soon", "", authed(s, false)); res.Code != http.StatusBadRequest {
		t.Fatalf("bad due_before = %d", res.Code)
	}
	// A calendar's range: due from today up to (not including) tomorrow.
	res = request(t, server, http.MethodGet, "/admin/api/entries?kind=todo&due_from="+today.Format(time.DateOnly)+"&due_before="+today.AddDate(0, 0, 1).Format(time.DateOnly), "", authed(s, false))
	if body := res.Body.String(); res.Code != http.StatusOK || strings.Contains(body, late) || !strings.Contains(body, dueToday) || strings.Contains(body, undated) {
		t.Fatalf("range = %d %s", res.Code, body)
	}
	lateID, _ := strconv.ParseInt(strings.Trim(strings.TrimPrefix(late, `"id":`), `"`), 10, 64)
	if _, _, err := db.SetOwnerState(ctx, lateID, store.OwnerPatch{SnoozeDays: ptr(2)}); err != nil {
		t.Fatal(err)
	}
	// By wake date, whether or not the day has come: today's view has none, the day after tomorrow's has it.
	for from, want := range map[int]bool{0: false, 2: true} {
		res = request(t, server, http.MethodGet, "/admin/api/entries?wakes_from="+today.AddDate(0, 0, from).Format(time.DateOnly)+"&wakes_before="+today.AddDate(0, 0, from+1).Format(time.DateOnly), "", authed(s, false))
		if strings.Contains(res.Body.String(), late) != want {
			t.Fatalf("wakes on +%d = %s", from, res.Body.String())
		}
	}
}

// The owner's research switch is its own endpoint: saving the project form leaves it alone, and research
// handoffs show their run status to the owner.
func TestOwnerSharesAProjectWithResearchAndSeesRunStatus(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if res := request(t, server, http.MethodPut, "/admin/api/projects/atlas", `{"name":"Atlas","tier":"focus","hours_wk":8}`, authed(s, true)); res.Code != http.StatusOK {
		t.Fatalf("create project = %d %s", res.Code, res.Body.String())
	}
	if res := request(t, server, http.MethodPut, "/admin/api/projects/atlas/research", `{"visible":true}`, map[string]string{"Cookie": s.cookie}); res.Code != http.StatusForbidden {
		t.Fatalf("switch without CSRF = %d", res.Code)
	}
	if res := request(t, server, http.MethodPut, "/admin/api/projects/atlas/research", `{}`, authed(s, true)); res.Code != http.StatusBadRequest {
		t.Fatalf("switch without visible = %d", res.Code)
	}
	if res := request(t, server, http.MethodPut, "/admin/api/projects/missing/research", `{"visible":true}`, authed(s, true)); res.Code != http.StatusNotFound {
		t.Fatalf("switch on a missing project = %d", res.Code)
	}
	if res := request(t, server, http.MethodPut, "/admin/api/projects/atlas/research", `{"visible":true}`, authed(s, true)); res.Code != http.StatusOK {
		t.Fatalf("switch = %d %s", res.Code, res.Body.String())
	}
	if res := request(t, server, http.MethodPut, "/admin/api/projects/atlas", `{"name":"Atlas","tier":"focus","hours_wk":9}`, authed(s, true)); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"research_visible":true`) {
		t.Fatalf("form save reset the switch: %d %s", res.Code, res.Body.String())
	}
	for _, path := range []string{"/admin/api/projects/atlas", "/admin/api/projects", "/admin/api/overview"} {
		if res := request(t, server, http.MethodGet, path, "", authed(s, false)); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"research_visible":true`) {
			t.Errorf("%s lacks the switch: %d %s", path, res.Code, res.Body.String())
		}
	}

	task, err := db.CreateResearchTask(ctx, store.NewResearchTask{ProjectSlug: "atlas", Title: "Survey", Source: "claude", ClientID: "c", Spec: store.ResearchSpec{Objective: "Compare", Acceptance: []string{"Cited"}}})
	if err != nil {
		t.Fatal(err)
	}
	res := request(t, server, http.MethodGet, "/admin/api/handoffs/"+strconv.FormatInt(task.ID, 10), "", authed(s, false))
	var detail struct {
		Handoff  struct{ Kind string } `json:"handoff"`
		Research struct {
			MessageID   string `json:"message_id"`
			State       string `json:"state"`
			MaxAttempts int    `json:"max_attempts"`
		} `json:"research"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &detail); err != nil || detail.Handoff.Kind != "research" || detail.Research.MessageID != strconv.FormatInt(task.MessageID, 10) || detail.Research.State != "ready" || detail.Research.MaxAttempts != 3 {
		t.Fatalf("research detail = %s, %v", res.Body.String(), err)
	}
}

// The owner creates an API key, sees its secret once, and revokes it; the key then stops working.
func TestOwnerManagesAPIKeys(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if res := request(t, server, http.MethodPost, "/admin/api/api-keys", `{"name":"Adastrion Core"}`, map[string]string{"Cookie": s.cookie}); res.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF = %d", res.Code)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/api-keys", `{"name":""}`, authed(s, true)); res.Code != http.StatusBadRequest {
		t.Fatalf("create without name = %d", res.Code)
	}
	created := request(t, server, http.MethodPost, "/admin/api/api-keys", `{"name":"Adastrion Core"}`, authed(s, true))
	var payload struct {
		Key struct {
			ID     int64    `json:"id"`
			Prefix string   `json:"prefix"`
			Scopes []string `json:"scopes"`
		} `json:"key"`
		Secret string `json:"secret"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &payload) != nil || !strings.HasPrefix(payload.Secret, payload.Key.Prefix) || len(payload.Key.Scopes) != 1 || payload.Key.Scopes[0] != "research:dispatch" {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	listed := request(t, server, http.MethodGet, "/admin/api/api-keys", "", authed(s, false))
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), payload.Secret) || strings.Contains(listed.Body.String(), "hash") || !strings.Contains(listed.Body.String(), "Adastrion Core") {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	if _, err := db.LookupAPIKey(ctx, payload.Secret); err != nil {
		t.Fatalf("new key does not work: %v", err)
	}
	revoked := request(t, server, http.MethodDelete, "/admin/api/api-keys/"+strconv.FormatInt(payload.Key.ID, 10), "", authed(s, true))
	if revoked.Code != http.StatusOK || !strings.Contains(revoked.Body.String(), "revoked_at") {
		t.Fatalf("revoke = %d %s", revoked.Code, revoked.Body.String())
	}
	if _, err := db.LookupAPIKey(ctx, payload.Secret); !store.IsNotFound(err) {
		t.Fatalf("revoked key still works: %v", err)
	}
	if res := request(t, server, http.MethodDelete, "/admin/api/api-keys/999999", "", authed(s, true)); res.Code != http.StatusNotFound {
		t.Fatalf("revoke missing = %d", res.Code)
	}
}

// The console creates a research task with files: a draft, files uploaded to its brief, then Queue.
// The run finds the files on the brief.
func TestConsoleCreatesResearchWithFiles(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, signedIn := login(t, server, "correct horse", "")
	if bad := request(t, server, http.MethodPost, "/admin/api/research", `{"title":"No checks","objective":"x","acceptance":[]}`, authed(signedIn, true)); bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid spec = %d %s", bad.Code, bad.Body.String())
	}
	if missing := request(t, server, http.MethodPost, "/admin/api/research", `{"title":"x","objective":"x","acceptance":["y"],"project_slug":"nope"}`, authed(signedIn, true)); missing.Code != http.StatusBadRequest {
		t.Fatalf("unknown project = %d %s", missing.Code, missing.Body.String())
	}
	created := request(t, server, http.MethodPost, "/admin/api/research", `{"title":"Benchmark","objective":"Rerun the attached benchmark","acceptance":["Return a CSV"],"deliverable":"dataset","draft":true}`, authed(signedIn, true))
	var task struct {
		HandoffID string `json:"handoff_id"`
		MessageID string `json:"message_id"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &task); err != nil || created.Code != http.StatusCreated || task.State != "draft" {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, _ := writer.CreateFormFile("file", "bench.csv")
	_, _ = part.Write([]byte("engine,tokens_per_s\n"))
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/admin/api/handoff-messages/"+task.MessageID+"/files", &upload)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Cookie", signedIn.cookie)
	req.Header.Set("Origin", testPublicURL)
	req.Header.Set("X-CSRF-Token", signedIn.csrf)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", res.Code, res.Body.String())
	}
	if queued := request(t, server, http.MethodPost, "/admin/api/handoff-messages/"+task.MessageID+"/actions", `{"action":"publish"}`, authed(signedIn, true)); queued.Code != http.StatusOK {
		t.Fatalf("queue = %d %s", queued.Code, queued.Body.String())
	}
	claim, err := db.ClaimResearchTask(ctx, 60, "Adastrion", "dispatcher")
	if err != nil || claim == nil || strconv.FormatInt(claim.Task.ID, 10) != task.HandoffID || claim.Task.Spec.Deliverable != "dataset" {
		t.Fatalf("claim = %#v, %v", claim, err)
	}
	pack, err := db.ResearchContext(ctx, claim.Task.ID, nil)
	if err != nil || len(pack.Files) != 1 || pack.Files[0].Filename != "bench.csv" {
		t.Fatalf("brief files = %#v, %v", pack.Files, err)
	}
}

// The owner links several repositories to a project, refusing duplicates and URLs that carry secrets,
// and sets the GitHub sync token, which no response ever repeats.
func TestConsoleLinksReposAndKeepsTheSyncTokenSecret(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	const token = "github_pat_consoleTEST0123456789abcd"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"login":"CesarPetrescu"}`))
	}))
	defer api.Close()
	sync, err := github.New(db, strings.Repeat("k", 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := oauth.HashPassword("correct horse")
	server := NewServer(Config{PublicURL: testPublicURL, PasswordHash: hash, InternalProxyCIDR: "172.31.255.2/32", IndexURL: "http://127.0.0.1:1", GitHub: sync.WithAPI(api.URL)}, db)
	_, signedIn := login(t, server, "correct horse", "")
	mutate := authed(signedIn, true)

	// In order: the duplicate must come after the original.
	for _, step := range []struct {
		body string
		want int
	}{
		{`{"url":"https://github.com/CesarPetrescu/atlas-api","role":"backend","branch":"main"}`, http.StatusCreated},
		{`{"url":"git@github.com:CesarPetrescu/atlas-web.git","role":"frontend"}`, http.StatusCreated},
		{`{"url":"git@github.com:cesarpetrescu/atlas-api.git"}`, http.StatusConflict},
		{`{"url":"https://ghp_leak@github.com/CesarPetrescu/atlas-api"}`, http.StatusBadRequest},
		{`{"url":"not a url"}`, http.StatusBadRequest},
	} {
		if res := request(t, server, http.MethodPost, "/admin/api/projects/atlas/repos", step.body, mutate); res.Code != step.want {
			t.Errorf("%s = %d %s", step.body, res.Code, res.Body.String())
		}
	}
	if res := request(t, server, http.MethodPost, "/admin/api/projects/nope/repos", `{"url":"https://github.com/a/b"}`, mutate); res.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d %s", res.Code, res.Body.String())
	}
	listed := request(t, server, http.MethodGet, "/admin/api/projects/atlas/repos", "", authed(signedIn, false))
	var list struct {
		Repos []store.ProjectRepo `json:"repos"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil || len(list.Repos) != 2 || list.Repos[0].Role != "backend" || list.Repos[0].AddedBy != store.OwnerSource || list.Repos[1].WebURL != "https://github.com/CesarPetrescu/atlas-web" {
		t.Fatalf("list = %s", listed.Body.String())
	}

	if res := request(t, server, http.MethodPut, "/admin/api/github-sync", `{"token":"github_pat_wrongwrongwrongwrong"}`, mutate); res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "rejected") {
		t.Fatalf("bad token = %d %s", res.Code, res.Body.String())
	}
	saved := request(t, server, http.MethodPut, "/admin/api/github-sync", `{"token":"`+token+`"}`, mutate)
	status := request(t, server, http.MethodGet, "/admin/api/github-sync", "", authed(signedIn, false))
	for _, res := range []*httptest.ResponseRecorder{saved, status} {
		if res.Code != http.StatusOK || strings.Contains(res.Body.String(), token) || !strings.Contains(res.Body.String(), `"login":"CesarPetrescu"`) || !strings.Contains(res.Body.String(), `"hint":"…abcd"`) {
			t.Fatalf("token status = %d %s", res.Code, res.Body.String())
		}
	}
	if res := request(t, server, http.MethodDelete, "/admin/api/repos/"+list.Repos[1].ID, "", mutate); res.Code != http.StatusOK {
		t.Fatalf("unlink = %d %s", res.Code, res.Body.String())
	}
	if res := request(t, server, http.MethodDelete, "/admin/api/github-sync", "", mutate); res.Code != http.StatusNoContent {
		t.Fatalf("forget token = %d", res.Code)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/github-sync", "", authed(signedIn, false)); !strings.Contains(res.Body.String(), `"configured":false`) {
		t.Fatalf("status after forgetting = %s", res.Body.String())
	}
}
