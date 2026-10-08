//go:build integration

package mcpserver

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Agents see a project's repositories with the project, link the ones they work in, and remove only
// their own links. A shared project's repositories reach research runs.
func TestAgentsSeeAndLinkProjectRepos(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LinkRepo(ctx, store.NewRepo{ProjectSlug: "atlas", URL: "https://github.com/acme/atlas-api", Role: "backend", Source: store.OwnerSource, ClientID: "owner"}); err != nil {
		t.Fatal(err)
	}
	addAccess(t, db, ctx, "claude-token", []string{"ledger:read", "ledger:write"})
	addAccess(t, db, ctx, "reader-token", []string{"ledger:read"})
	server := httptest.NewServer(HTTPHandler(NewServer(db, "http://unused"), db, "https://ledger.example.com"))
	defer server.Close()
	claude := connectMCP(t, server.URL+"/mcp", "claude-token", "claude-code")

	project := callTool[projectOutput](t, claude, "get_project", map[string]any{"slug": "atlas"})
	if len(project.Repos) != 1 || project.Repos[0].Repo != "acme/atlas-api" || project.Repos[0].Role != "backend" || project.Project.Slug != "atlas" {
		t.Fatalf("get_project repos = %#v", project.Repos)
	}
	resource, err := claude.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ledger://project/atlas"})
	var fromResource projectOutput
	if err != nil || len(resource.Contents) != 1 || json.Unmarshal([]byte(resource.Contents[0].Text), &fromResource) != nil || len(fromResource.Repos) != 1 || fromResource.Repos[0].Repo != "acme/atlas-api" {
		t.Fatalf("project resource = %#v, %v", resource, err)
	}
	web := callTool[store.ProjectRepo](t, claude, "link_repo", map[string]any{"project_slug": "atlas", "url": "git@github.com:acme/atlas-web.git", "role": "frontend"})
	if web.AddedBy != "claude-code" || web.WebURL != "https://github.com/acme/atlas-web" {
		t.Fatalf("linked = %#v", web)
	}
	for name, arguments := range map[string]map[string]any{
		"duplicate":    {"project_slug": "atlas", "url": "https://github.com/ACME/atlas-web"},
		"secret in it": {"project_slug": "atlas", "url": "https://ghp_x@github.com/acme/other"},
		"no project":   {"project_slug": "nope", "url": "https://github.com/acme/other"},
	} {
		if result, err := claude.CallTool(ctx, &mcp.CallToolParams{Name: "link_repo", Arguments: arguments}); err == nil && !result.IsError {
			t.Errorf("%s linked", name)
		}
	}
	if result, err := claude.CallTool(ctx, &mcp.CallToolParams{Name: "unlink_repo", Arguments: map[string]any{"id": project.Repos[0].ID}}); err != nil || !result.IsError {
		t.Fatalf("unlinking the owner's link = %#v, %v", result, err)
	}
	reader := connectMCP(t, server.URL+"/mcp", "reader-token", "reader")
	if result, err := reader.CallTool(ctx, &mcp.CallToolParams{Name: "link_repo", Arguments: map[string]any{"project_slug": "atlas", "url": "https://github.com/acme/x"}}); err != nil || !result.IsError {
		t.Fatalf("read-only client linked: %#v, %v", result, err)
	}
	if all := callTool[repoList](t, reader, "list_repos", map[string]any{}); len(all.Repos) != 2 {
		t.Fatalf("list_repos = %#v", all.Repos)
	}
	callTool[store.ProjectRepo](t, claude, "unlink_repo", map[string]any{"id": web.ID})
	if left := callTool[repoList](t, claude, "list_repos", map[string]any{"project_slug": "atlas"}); len(left.Repos) != 1 {
		t.Fatalf("after unlink = %#v", left.Repos)
	}

	// A research run sees the repositories only when the owner shared the project.
	task, err := db.CreateResearchTask(ctx, store.NewResearchTask{ProjectSlug: "atlas", Title: "Audit", Source: "c", ClientID: "c", Spec: store.ResearchSpec{Objective: "Audit the API", Acceptance: []string{"Cite files"}}})
	if err != nil {
		t.Fatal(err)
	}
	if pack, _ := db.ResearchContext(ctx, task.ID, nil); pack.Project == nil || len(pack.Project.Repos) != 0 {
		t.Fatalf("unshared project leaked repos: %#v", pack.Project)
	}
	if err := db.SetProjectResearchVisible(ctx, "atlas", true); err != nil {
		t.Fatal(err)
	}
	if pack, _ := db.ResearchContext(ctx, task.ID, nil); pack.Project == nil || len(pack.Project.Repos) != 1 || pack.Project.Repos[0].Repo != "acme/atlas-api" {
		t.Fatalf("shared project repos = %#v", pack.Project)
	}
}
