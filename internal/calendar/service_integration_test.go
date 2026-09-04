//go:build integration

package calendar

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/testdb"
)

type fakeNextcloud struct {
	mu      sync.Mutex
	server  *httptest.Server
	events  map[string]string
	etags   map[string]string
	created bool
	updated bool
	deleted bool
	revoked bool
}

const (
	testCalendarPath = "/remote.php/dav/calendars/alex/work/"
	testEventPath    = testCalendarPath + "planning.ics"
)

func newFakeNextcloud(t *testing.T) *fakeNextcloud {
	t.Helper()
	fake := &fakeNextcloud{
		events: map[string]string{testEventPath: calendarDocument("planning@nextcloud", "Planning", "20260905T090000Z", "20260905T100000Z")},
		etags:  map[string]string{testEventPath: "v1"},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeNextcloud) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/index.php/login/v2" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"poll":{"token":"poll-token","endpoint":%q},"login":%q}`, f.server.URL+"/login/v2/poll", f.server.URL+"/login/approve")
		return
	}
	if r.URL.Path == "/login/v2/poll" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"server":%q,"loginName":"alex","appPassword":"app-secret"}`, f.server.URL)
		return
	}
	username, password, ok := r.BasicAuth()
	if !ok || username != "alex" || password != "app-secret" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/ocs/v2.php/core/apppassword" && r.Method == http.MethodDelete {
		f.revoked = true
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch r.Method {
	case "PROPFIND":
		f.propfind(w, r)
	case "REPORT":
		f.report(w)
	case http.MethodGet:
		f.get(w, r.URL.Path)
	case http.MethodPut:
		f.put(w, r)
	case http.MethodDelete:
		f.delete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (f *fakeNextcloud) propfind(w http.ResponseWriter, r *http.Request) {
	var body string
	switch r.URL.Path {
	case "/remote.php/dav", "/remote.php/dav/":
		body = davResponse(r.URL.Path, `<d:current-user-principal><d:href>/remote.php/dav/principals/users/alex/</d:href></d:current-user-principal>`)
	case "/remote.php/dav/principals/users/alex/":
		body = davResponse(r.URL.Path, `<c:calendar-home-set><d:href>/remote.php/dav/calendars/alex/</d:href></c:calendar-home-set>`)
	case "/remote.php/dav/calendars/alex/":
		body = davResponse(testCalendarPath, `<d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>Work</d:displayname><c:calendar-description>Team schedule</c:calendar-description><c:supported-calendar-component-set><c:comp name="VEVENT"/></c:supported-calendar-component-set>`)
	case testCalendarPath:
		body = davResponse(testCalendarPath, `<d:sync-token>https://nextcloud.test/sync/1</d:sync-token>`)
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = io.WriteString(w, body)
}

func (f *fakeNextcloud) report(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var responses strings.Builder
	for path, document := range f.events {
		fmt.Fprintf(&responses, `<d:response><d:href>%s</d:href><d:propstat><d:prop><d:getetag>"%s"</d:getetag><c:calendar-data>%s</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, path, f.etags[path], html.EscapeString(document))
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprintf(w, `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">%s</d:multistatus>`, responses.String())
}

func (f *fakeNextcloud) get(w http.ResponseWriter, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	document, ok := f.events[path]
	if !ok {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("ETag", `"`+f.etags[path]+`"`)
	_, _ = io.WriteString(w, document)
}

func (f *fakeNextcloud) put(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if r.Header.Get("If-None-Match") == "*" {
		if _, exists := f.events[r.URL.Path]; exists {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		f.created = true
		f.events[r.URL.Path] = string(body)
		f.etags[r.URL.Path] = "v2"
		w.WriteHeader(http.StatusCreated)
		return
	}
	if r.Header.Get("If-Match") != `"`+f.etags[r.URL.Path]+`"` {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	f.updated = true
	f.events[r.URL.Path] = string(body)
	f.etags[r.URL.Path] = "v3"
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeNextcloud) delete(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("If-Match") != `"`+f.etags[r.URL.Path]+`"` {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	f.deleted = true
	delete(f.events, r.URL.Path)
	delete(f.etags, r.URL.Path)
	w.WriteHeader(http.StatusNoContent)
}

func davResponse(path, props string) string {
	return fmt.Sprintf(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>%s</d:href><d:propstat><d:prop>%s</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`, path, props)
}

func calendarDocument(uid, title, start, end string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Nextcloud//Test//EN\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20260904T120000Z\r\nDTSTART:" + start + "\r\nDTEND:" + end + "\r\nSUMMARY:" + title + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

func TestNextcloudLoginDiscoveryAndEventLifecycle(t *testing.T) {
	db, ctx := testdb.Open(t)
	fake := newFakeNextcloud(t)
	service, err := NewService(db, strings.Repeat("k", 32), fake.server.Client())
	if err != nil {
		t.Fatal(err)
	}

	flow, err := service.StartLogin(ctx, fake.server.URL)
	if err != nil || flow.LoginURL == "" {
		t.Fatalf("start login = %#v, %v", flow, err)
	}
	connection, err := service.PollLogin(ctx, flow.ID)
	if err != nil || !connection.Connected || connection.Username != "alex" {
		t.Fatalf("poll login = %#v, %v", connection, err)
	}
	account, err := db.GetCalendarAccount(ctx)
	if err != nil || strings.Contains(string(account.PasswordCiphertext), "app-secret") {
		t.Fatalf("stored account leaks credential: %#v, %v", account, err)
	}

	calendars, err := service.Calendars(ctx)
	if err != nil || len(calendars) != 1 || calendars[0].Name != "Work" {
		t.Fatalf("calendars = %#v, %v", calendars, err)
	}
	if err := service.SelectCalendars(ctx, []string{calendars[0].ID}); err != nil {
		t.Fatal(err)
	}
	events, err := service.ListEvents(ctx, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), "")
	if err != nil || len(events) != 1 || events[0].Title != "Planning" || events[0].ETag != `"v1"` {
		t.Fatalf("events = %#v, %v", events, err)
	}

	created, err := service.CreateEvent(ctx, calendars[0].ID, EventInput{Title: "Review", Start: "2026-09-06T09:00:00Z", End: "2026-09-06T10:00:00Z"})
	if err != nil || created.Title != "Review" || !fake.created {
		t.Fatalf("created event = %#v, %v", created, err)
	}
	updated, err := service.UpdateEvent(ctx, created.ID, created.ETag, EventInput{Title: "Final review", Start: "2026-09-06T09:00:00Z", End: "2026-09-06T10:30:00Z"})
	if err != nil || updated.Title != "Final review" || !fake.updated {
		t.Fatalf("updated event = %#v, %v", updated, err)
	}
	if _, err := service.UpdateEvent(ctx, created.ID, created.ETag, EventInput{Title: "Stale", Start: "2026-09-06T09:00:00Z", End: "2026-09-06T10:30:00Z"}); err != ErrConflict {
		t.Fatalf("stale update error = %v", err)
	}
	if err := service.DeleteEvent(ctx, updated.ID, updated.ETag); err != nil || !fake.deleted {
		t.Fatalf("delete = %v", err)
	}
	if err := service.Disconnect(ctx); err != nil || !fake.revoked {
		t.Fatalf("disconnect = %v revoked=%v", err, fake.revoked)
	}
}
