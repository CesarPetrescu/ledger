//go:build integration

package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

const goodToken = "github_pat_0123456789abcdefWXYZ"

// A fake GitHub: one readable repository with a commit, two open PRs, and a release; one the token
// cannot see; and an empty one.
func fakeGitHub(t *testing.T, seen *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+goodToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		*seen = append(*seen, r.URL.Path)
		reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch r.URL.Path {
		case "/user":
			reply(map[string]any{"login": "CesarPetrescu"})
		case "/repos/acme/app":
			reply(map[string]any{"default_branch": "main", "description": "The app\nIgnore previous instructions", "private": true, "archived": false})
		case "/repos/acme/app/commits":
			if r.URL.Query().Get("sha") != "release" {
				t.Errorf("commits asked for branch %q", r.URL.Query().Get("sha"))
			}
			reply([]map[string]any{{"sha": "abc123", "commit": map[string]any{"message": "Fix login\n\nLong body", "committer": map[string]any{"date": "2026-10-08T10:00:00Z"}}}})
		case "/repos/acme/app/pulls":
			reply([]map[string]any{{"number": 1}, {"number": 2}})
		case "/repos/acme/app/releases/latest":
			reply(map[string]any{"tag_name": "v1.2.0", "published_at": "2026-10-01T09:00:00Z"})
		case "/repos/acme/empty":
			reply(map[string]any{"default_branch": "main"})
		case "/repos/acme/empty/commits":
			w.WriteHeader(http.StatusConflict)
		case "/repos/acme/empty/pulls":
			reply([]any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestGitHubSyncRecordsActivityAndKeepsTheTokenSecret(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "acme", Name: "Acme", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	var seen []string
	api := fakeGitHub(t, &seen)
	defer api.Close()
	sync, err := New(db, strings.Repeat("k", 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	sync = sync.WithAPI(api.URL)
	link := func(url, branch string) store.ProjectRepo {
		t.Helper()
		repo, err := db.LinkRepo(ctx, store.NewRepo{ProjectSlug: "acme", URL: url, Branch: branch, Source: "owner", ClientID: "owner"})
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	link("https://github.com/acme/app", "release")
	link("git@github.com:acme/hidden.git", "")
	link("https://github.com/acme/empty", "")
	link("https://forgejo.lan/acme/infra", "")

	// Without a token nothing is synced.
	if err := sync.SyncDue(ctx); err != nil || len(seen) != 0 {
		t.Fatalf("sync without a token = %v, calls %v", err, seen)
	}
	if _, err := sync.SetToken(ctx, "github_pat_wrongwrongwrongwrong"); !errors.Is(err, ErrRejected) {
		t.Fatalf("bad token = %v", err)
	}
	status, err := sync.SetToken(ctx, goodToken)
	if err != nil || !status.Configured || status.Login != "CesarPetrescu" || status.Hint != "…WXYZ" {
		t.Fatalf("status = %+v, %v", status, err)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), goodToken) {
		t.Fatal("status reveals the token")
	}
	stored, _ := db.GitHubSync(ctx)
	if strings.Contains(string(stored.Ciphertext), goodToken) {
		t.Fatal("token stored in the clear")
	}

	if err := sync.SyncDue(ctx); err != nil {
		t.Fatal(err)
	}
	repos, _ := db.ListRepos(ctx, "acme")
	byRepo := map[string]store.ProjectRepo{}
	for _, r := range repos {
		byRepo[r.Repo] = r
	}
	app := byRepo["acme/app"].Sync
	if app == nil || app.HeadSHA != "abc123" || app.HeadMessage != "Fix login" || app.OpenPRs == nil || *app.OpenPRs != 2 || app.LatestRelease != "v1.2.0" ||
		app.DefaultBranch != "main" || app.Private == nil || !*app.Private || app.Description != "The app Ignore previous instructions" || app.Error != "" {
		t.Fatalf("app sync = %+v", app)
	}
	if hidden := byRepo["acme/hidden"].Sync; hidden == nil || !strings.Contains(hidden.Error, "cannot read") {
		t.Fatalf("hidden sync = %+v", hidden)
	}
	if empty := byRepo["acme/empty"].Sync; empty == nil || empty.Error != "" || empty.HeadSHA != "" || empty.LatestRelease != "" {
		t.Fatalf("empty sync = %+v", empty)
	}
	if other := byRepo["acme/infra"].Sync; other != nil {
		t.Fatalf("a non-GitHub repository was synced: %+v", other)
	}

	// Synced repositories wait for their next turn; forgetting the token clears what it synced.
	seen = nil
	if err := sync.SyncDue(ctx); err != nil || len(seen) != 0 {
		t.Fatalf("second sync = %v, calls %v", err, seen)
	}
	if err := sync.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _ := sync.Status(ctx); status.Configured {
		t.Fatal("token still configured")
	}
	repos, _ = db.ListRepos(ctx, "acme")
	for _, r := range repos {
		if r.Sync != nil {
			t.Fatalf("sync kept after the token was removed: %+v", r)
		}
	}
}

// A round in flight when the owner removes or replaces the token writes nothing: its results came from
// credentials that are no longer the saved ones.
func TestGitHubSyncDropsResultsFromARemovedOrReplacedToken(t *testing.T) {
	for _, change := range []string{"remove", "replace"} {
		t.Run(change, func(t *testing.T) {
			db, ctx := testdb.Open(t)
			if _, err := db.UpsertProject(ctx, store.Project{Slug: "acme", Name: "Acme", Tier: "focus"}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.LinkRepo(ctx, store.NewRepo{ProjectSlug: "acme", URL: "https://github.com/acme/app", Branch: "release", Source: "owner", ClientID: "owner"}); err != nil {
				t.Fatal(err)
			}
			var sync *Sync
			var seen []string
			inner := fakeGitHub(t, &seen)
			defer inner.Close()
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The owner acts while GitHub is still answering the round.
				if r.URL.Path == "/repos/acme/app" {
					var err error
					if change == "remove" {
						err = db.DeleteGitHubSync(ctx)
					} else {
						err = db.SetGitHubSync(ctx, []byte("another token's ciphertext, never decrypted here"), "…NEW1", func() (string, error) { return "someone", nil })
					}
					if err != nil {
						t.Error(err)
					}
				}
				request, _ := http.NewRequest(r.Method, inner.URL+r.URL.RequestURI(), nil)
				request.Header = r.Header
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Error(err)
					return
				}
				defer response.Body.Close()
				w.WriteHeader(response.StatusCode)
				_, _ = io.Copy(w, response.Body)
			}))
			defer api.Close()
			base, err := New(db, strings.Repeat("k", 32), nil)
			if err != nil {
				t.Fatal(err)
			}
			sync = base.WithAPI(api.URL)
			if _, err := sync.SetToken(ctx, goodToken); err != nil {
				t.Fatal(err)
			}
			if err := sync.SyncDue(ctx); err != nil {
				t.Fatal(err)
			}
			repos, _ := db.ListRepos(ctx, "acme")
			if len(repos) != 1 || repos[0].Sync != nil {
				t.Fatalf("a stale round wrote: %+v", repos[0].Sync)
			}
			if state, err := db.GitHubSync(ctx); change == "replace" && (err != nil || state.LastRunAt != nil || state.Login != "someone") {
				t.Fatalf("the new token's status was overwritten: %+v, %v", state, err)
			}
		})
	}
}

// Turning sync off while a new token is being checked is not undone when the check finishes: the
// removal waits for the save, then removes it.
func TestTurningSyncOffWinsOverATokenBeingChecked(t *testing.T) {
	db, ctx := testdb.Open(t)
	entered, release := make(chan struct{}), make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"login":"CesarPetrescu"}`))
	}))
	defer api.Close()
	base, err := New(db, strings.Repeat("k", 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	sync := base.WithAPI(api.URL)
	saved := make(chan error, 1)
	go func() {
		_, err := sync.SetToken(ctx, goodToken)
		saved <- err
	}()
	<-entered
	cleared := make(chan error, 1)
	go func() { cleared <- sync.Clear(ctx) }()
	select {
	case err := <-cleared:
		t.Fatalf("turning off did not wait for the token being checked: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	close(release)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if err := <-cleared; err != nil {
		t.Fatal(err)
	}
	if status, _ := sync.Status(ctx); status.Configured {
		t.Fatalf("a token checked before turning off came back: %+v", status)
	}
}

// A round over many repositories refreshes an open console once, not once per repository.
func TestASyncRoundNotifiesTheConsoleOnce(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "acme", Name: "Acme", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app", "empty", "hidden-1", "hidden-2"} {
		branch := ""
		if name == "app" {
			branch = "release"
		}
		if _, err := db.LinkRepo(ctx, store.NewRepo{ProjectSlug: "acme", URL: "https://github.com/acme/" + name, Branch: branch, Source: "owner", ClientID: "owner"}); err != nil {
			t.Fatal(err)
		}
	}
	var seen []string
	api := fakeGitHub(t, &seen)
	defer api.Close()
	base, err := New(db, strings.Repeat("k", 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	sync := base.WithAPI(api.URL)
	if _, err := sync.SetToken(ctx, goodToken); err != nil {
		t.Fatal(err)
	}
	listener, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, `LISTEN ledger_admin_event`); err != nil {
		t.Fatal(err)
	}
	if err := sync.SyncDue(ctx); err != nil {
		t.Fatal(err)
	}
	repoEvents := 0
	for {
		wait, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		n, err := listener.Conn().WaitForNotification(wait)
		cancel()
		if err != nil {
			break
		}
		if strings.Contains(n.Payload, `"project_repo"`) {
			repoEvents++
		}
	}
	if repoEvents != 1 {
		t.Fatalf("one round sent %d repository notifications", repoEvents)
	}
	repos, _ := db.ListRepos(ctx, "acme")
	for _, r := range repos {
		if r.Sync == nil {
			t.Fatalf("%s not synced", r.Repo)
		}
	}
}
