//go:build integration

package store_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

// A project links many repositories, up to the limit. An agent removes only the links it added, and
// the links go to Trash and come back with their project.
func TestProjectReposLinkUnlinkAndSurviveTrash(t *testing.T) {
	db, ctx := testdb.Open(t)
	for _, slug := range []string{"atlas", "other"} {
		if _, err := db.UpsertProject(ctx, store.Project{Slug: slug, Name: slug, Tier: "focus"}); err != nil {
			t.Fatal(err)
		}
	}
	link := func(slug, url, path, client string) (store.ProjectRepo, error) {
		return db.LinkRepo(ctx, store.NewRepo{ProjectSlug: slug, URL: url, Path: path, Role: "service", Source: client + "-name", ClientID: client})
	}
	api, err := link("atlas", "https://github.com/acme/atlas", "", "claude")
	if err != nil || api.AddedBy != "claude-name" || api.Repo != "acme/atlas" {
		t.Fatalf("link = %+v, %v", api, err)
	}
	// The same repository is fine in another project, or for another folder of a monorepo.
	if _, err := link("other", "git@github.com:acme/atlas.git", "", "claude"); err != nil {
		t.Fatalf("same repo in another project: %v", err)
	}
	if _, err := link("atlas", "git@github.com:acme/atlas.git", "services/web", "codex"); err != nil {
		t.Fatalf("another folder: %v", err)
	}
	if _, err := link("atlas", "git@github.com:ACME/Atlas.git", "", "codex"); !errors.Is(err, store.ErrRepoExists) {
		t.Fatalf("duplicate = %v", err)
	}
	for _, folder := range []string{"services/./web", "/services//web/", "services/x/../web"} {
		if _, err := link("atlas", "https://github.com/acme/atlas", folder, "codex"); !errors.Is(err, store.ErrRepoExists) {
			t.Fatalf("folder %q = %v", folder, err)
		}
	}
	if _, err := link("missing", "https://github.com/acme/x", "", "codex"); !store.IsNotFound(err) {
		t.Fatalf("unknown project = %v", err)
	}
	for _, outside := range []string{"../outside", "a/../../outside", "..", "..\\outside", "services\\web"} {
		if _, err := link("atlas", "https://github.com/acme/x", outside, "codex"); err == nil {
			t.Fatalf("path %q outside the repository was accepted", outside)
		}
	}
	for i := 2; i < store.MaxProjectRepos; i++ {
		if _, err := link("atlas", fmt.Sprintf("https://github.com/acme/repo-%d", i), "", "codex"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := link("atlas", "https://github.com/acme/one-too-many", "", "codex"); !errors.Is(err, store.ErrRepoLimit) {
		t.Fatalf("over the limit = %v", err)
	}
	var apiID int64
	fmt.Sscan(api.ID, &apiID)
	if _, err := db.UnlinkRepo(ctx, apiID, "codex", false); !errors.Is(err, store.ErrRepoForbidden) {
		t.Fatalf("another agent's unlink = %v", err)
	}
	if _, err := db.UnlinkRepo(ctx, apiID, "claude", false); err != nil {
		t.Fatalf("own unlink = %v", err)
	}

	// What a sync saw does not travel through Trash: it belongs to the token that saw it.
	if _, err := db.Pool.Exec(ctx, `UPDATE project_repo SET synced_at=now(),head_message='synced with the old token' WHERE project_slug='atlas'`); err != nil {
		t.Fatal(err)
	}
	before, _ := db.ListRepos(ctx, "atlas")
	trashID, _, err := db.TrashProject(ctx, "atlas")
	if err != nil {
		t.Fatal(err)
	}
	if gone, _ := db.ListRepos(ctx, "atlas"); len(gone) != 0 {
		t.Fatalf("links outlived their project: %d", len(gone))
	}
	if err := db.RestoreTrash(ctx, trashID); err != nil {
		t.Fatal(err)
	}
	after, _ := db.ListRepos(ctx, "atlas")
	if len(after) != len(before) || after[0].ID != before[0].ID || after[0].AddedBy != before[0].AddedBy || after[0].Path != "services/web" {
		t.Fatalf("restored %d of %d links: %+v", len(after), len(before), after[0])
	}
	for _, repo := range after {
		if repo.Sync != nil {
			t.Fatalf("restored synced activity: %+v", repo.Sync)
		}
	}
	all, _ := db.ListRepos(ctx, "")
	if len(all) != len(after)+1 {
		t.Fatalf("all repos = %d", len(all))
	}
}

// An unlink in flight while the project is deleted wins or loses as a whole: the link it removed never
// comes back from Trash.
func TestTrashWaitsForAnUnlinkInFlight(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	gone, err := db.LinkRepo(ctx, store.NewRepo{ProjectSlug: "atlas", URL: "https://github.com/acme/gone", Source: "c", ClientID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.LinkRepo(ctx, store.NewRepo{ProjectSlug: "atlas", URL: "https://github.com/acme/kept", Source: "c", ClientID: "c"}); err != nil {
		t.Fatal(err)
	}
	unlink, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unlink.Exec(ctx, `DELETE FROM project_repo WHERE id=$1::bigint`, gone.ID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		id  int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		id, _, err := db.TrashProject(ctx, "atlas")
		done <- result{id, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("Trash did not wait for the unlink: %+v", r)
	case <-time.After(500 * time.Millisecond):
	}
	if err := unlink.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if err := db.RestoreTrash(ctx, r.id); err != nil {
		t.Fatal(err)
	}
	repos, _ := db.ListRepos(ctx, "atlas")
	if len(repos) != 1 || repos[0].Repo != "acme/kept" {
		t.Fatalf("restored links = %+v", repos)
	}
}

// A failed check reports the problem and keeps the last good snapshot.
func TestFailedSyncKeepsTheLastGoodSnapshot(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	repo, err := db.LinkRepo(ctx, store.NewRepo{ProjectSlug: "atlas", URL: "https://github.com/acme/atlas", Source: "c", ClientID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetGitHubSync(ctx, []byte("ciphertext"), "…abcd", func() (string, error) { return "owner", nil }); err != nil {
		t.Fatal(err)
	}
	state, _ := db.GitHubSync(ctx)
	open := 3
	if err := db.SaveSyncRound(ctx, state.SavedAt, []store.RepoSyncResult{{ID: repo.ID, Sync: store.RepoSync{HeadSHA: "abc", HeadMessage: "Fix", OpenPRs: &open, LatestRelease: "v1"}}}, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSyncRound(ctx, state.SavedAt, []store.RepoSyncResult{{ID: repo.ID, Sync: store.RepoSync{Error: "GitHub answered HTTP 502"}}}, ""); err != nil {
		t.Fatal(err)
	}
	repos, _ := db.ListRepos(ctx, "atlas")
	got := repos[0].Sync
	if got == nil || got.Error != "GitHub answered HTTP 502" || got.HeadSHA != "abc" || got.LatestRelease != "v1" || got.OpenPRs == nil || *got.OpenPRs != 3 {
		t.Fatalf("after a failed check = %+v", got)
	}
	// The next good check clears the error.
	if err := db.SaveSyncRound(ctx, state.SavedAt, []store.RepoSyncResult{{ID: repo.ID, Sync: store.RepoSync{HeadSHA: "def"}}}, ""); err != nil {
		t.Fatal(err)
	}
	repos, _ = db.ListRepos(ctx, "atlas")
	if got := repos[0].Sync; got.Error != "" || got.HeadSHA != "def" {
		t.Fatalf("after recovering = %+v", got)
	}
	// A new token starts afresh: a failed check with it cannot show what the old token saw.
	if err := db.SetGitHubSync(ctx, []byte("other ciphertext"), "…wxyz", func() (string, error) { return "owner", nil }); err != nil {
		t.Fatal(err)
	}
	replaced, _ := db.GitHubSync(ctx)
	if err := db.SaveSyncRound(ctx, replaced.SavedAt, []store.RepoSyncResult{{ID: repo.ID, Sync: store.RepoSync{Error: "not found, or the token cannot read this repository"}}}, ""); err != nil {
		t.Fatal(err)
	}
	repos, _ = db.ListRepos(ctx, "atlas")
	if got := repos[0].Sync; got == nil || got.HeadSHA != "" || got.Error == "" {
		t.Fatalf("the old token's snapshot survived a replacement: %+v", got)
	}
}
