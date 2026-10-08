//go:build integration

package store_test

import (
	"errors"
	"fmt"
	"testing"

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
	for _, outside := range []string{"../outside", "a/../../outside", ".."} {
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
