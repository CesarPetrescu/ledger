//go:build integration

package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
