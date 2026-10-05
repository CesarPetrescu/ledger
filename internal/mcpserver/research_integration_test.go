//go:build integration

package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rawStatus posts a tools/list call and returns the HTTP status, for endpoints that must refuse a token
// before MCP is reached.
func rawStatus(t *testing.T, endpoint, token string) int {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

func callTool[T any](t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) T {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil || result.IsError {
		t.Fatalf("tool call = %#v, %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var out T
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("output %s: %v", encoded, err)
	}
	return out
}

// A run token works on /mcp/research for its own task only, and nowhere else; agents' tokens and the
// dispatcher's token do not work there; agents cannot see research threads; only research:dispatch
// opens /mcp/dispatch.
func TestResearchTokensAreConfinedToTheirOwnTask(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	addAccess(t, db, ctx, "agent-token", []string{"ledger:read", "ledger:write"})
	addAccess(t, db, ctx, "dispatch-token", []string{"research:dispatch"})
	server := httptest.NewServer(HTTPHandler(NewServer(db, "http://unused"), db, "https://ledger.example.com"))
	defer server.Close()

	agent := connectMCP(t, server.URL+"/mcp", "agent-token", "claude-code")
	created := map[string]store.ResearchTask{}
	for _, title := range []string{"Mine", "Someone else's"} {
		task := callTool[store.ResearchTask](t, agent, "create_research_task", map[string]any{
			"project_slug": "atlas", "title": title, "objective": "Compare vector databases", "acceptance": []string{"Three options"}, "budget": map[string]any{"minutes": 20},
		})
		if task.State != "ready" || task.Spec.Budget.Minutes != 20 {
			t.Fatalf("created = %#v", task)
		}
		created[title] = task
	}
	listed := callTool[map[string]any](t, agent, "list_handoffs", map[string]any{"archive": "all", "include_all": true})
	if handoffs := listed["handoffs"].([]any); len(handoffs) != 0 {
		t.Fatalf("agent sees research handoffs: %#v", handoffs)
	}
	if hidden, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "get_handoff", Arguments: map[string]any{"id": "1"}}); err != nil || !hidden.IsError {
		t.Fatalf("agent get_handoff on research = %#v, %v", hidden, err)
	}

	if status := rawStatus(t, server.URL+"/mcp/dispatch", "agent-token"); status != http.StatusForbidden {
		t.Fatalf("agent token on /mcp/dispatch = %d", status)
	}
	dispatcher := connectMCP(t, server.URL+"/mcp/dispatch", "dispatch-token", "Adastrion dispatcher")
	claim := callTool[dispatchClaim](t, dispatcher, "claim_research_task", map[string]any{"lease_seconds": 120})
	if !claim.Claimed || claim.Task.ID != created["Mine"].ID || claim.Token == "" || claim.Endpoint != "https://ledger.example.com/mcp/research" {
		t.Fatalf("claim = %#v", claim)
	}

	for endpoint, token := range map[string]string{"/mcp": claim.Token, "/mcp/dispatch": claim.Token, "/mcp/research": "agent-token"} {
		if status := rawStatus(t, server.URL+endpoint, token); status != http.StatusUnauthorized {
			t.Errorf("%s with the wrong kind of token = %d, want 401", endpoint, status)
		}
	}
	if status := rawStatus(t, server.URL+"/mcp/research", "dispatch-token"); status != http.StatusUnauthorized {
		t.Errorf("dispatcher token on /mcp/research = %d", status)
	}

	run := connectMCP(t, server.URL+"/mcp/research", claim.Token, "researcher")
	tools, err := run.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "ask_owner,checkpoint,get_task,heartbeat,submit" {
		t.Fatalf("research tools = %v", names)
	}
	pack := callTool[store.ResearchContext](t, run, "get_task", map[string]any{})
	if pack.Task.ID != created["Mine"].ID || pack.Task.Attempt != 1 || pack.Task.Title != "Mine" {
		t.Fatalf("get_task = %#v", pack.Task)
	}
	callTool[leaseOutput](t, run, "checkpoint", map[string]any{"state": "two sources read"})
	callTool[leaseOutput](t, dispatcher, "renew_research_lease", map[string]any{"task_id": strconv.FormatInt(claim.Task.ID, 10), "attempt": 1})
	submitted := callTool[submitOutput](t, run, "submit", map[string]any{"deliverable": "# Result", "files": []map[string]any{{"filename": "notes.txt", "content_base64": "aGk="}}})
	if submitted.Phase != "review" {
		t.Fatalf("submit = %#v", submitted)
	}
	// The run is over: its token is dead, and the dispatcher's renewal says so.
	if status := rawStatus(t, server.URL+"/mcp/research", claim.Token); status != http.StatusUnauthorized {
		t.Fatalf("token after submit = %d", status)
	}
	lost, err := dispatcher.CallTool(ctx, &mcp.CallToolParams{Name: "renew_research_lease", Arguments: map[string]any{"task_id": strconv.FormatInt(claim.Task.ID, 10), "attempt": 1}})
	if err != nil || !lost.IsError || !strings.Contains(lost.Content[0].(*mcp.TextContent).Text, "lease_lost") {
		t.Fatalf("renew after submit = %#v, %v", lost, err)
	}
	ended := callTool[store.ResearchTask](t, dispatcher, "end_research_run", map[string]any{"task_id": strconv.FormatInt(claim.Task.ID, 10), "attempt": 1, "error": "exit 0"})
	if ended.Phase != "review" || ended.Failures != 0 {
		t.Fatalf("end after submit = %#v", ended)
	}
	var files int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM handoff_file f JOIN handoff_message m ON m.id=f.message_id WHERE m.handoff_id=$1`, created["Mine"].ID).Scan(&files); err != nil || files != 1 {
		t.Fatalf("submitted files = %d, %v", files, err)
	}
}
