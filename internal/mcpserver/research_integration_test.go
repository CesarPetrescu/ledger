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
	created := map[string]researchTaskOutput{}
	for _, title := range []string{"Mine", "Someone else's"} {
		task := callTool[researchTaskOutput](t, agent, "create_research_task", map[string]any{
			"project_slug": "atlas", "title": title, "objective": "Compare vector databases", "acceptance": []string{"Three options"},
		})
		if task.State != "ready" || task.Spec.ExecutionMode != "until_done" {
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

	// The owner attaches a file to each task's thread; a run may read its own task's file only.
	attach := func(task researchTaskOutput, name string) string {
		id, _ := strconv.ParseInt(task.ID, 10, 64)
		reply, err := db.AppendHandoffMessage(ctx, store.HandoffMessage{HandoffID: id, Body: "See " + name, WorkState: "draft", Source: store.OwnerSource, ClientID: "owner"}, true)
		if err != nil {
			t.Fatal(err)
		}
		file, err := db.AddHandoffFile(ctx, reply.ID, name, "text/csv", []byte(name+" data"), "owner", true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.UpdateHandoffMessage(ctx, reply.ID, "publish", "", store.OwnerSource, "owner", true); err != nil {
			t.Fatal(err)
		}
		return strconv.FormatInt(file.ID, 10)
	}
	mineFile, otherFile := attach(created["Mine"], "mine.csv"), attach(created["Someone else's"], "other.csv")

	if status := rawStatus(t, server.URL+"/mcp/dispatch", "agent-token"); status != http.StatusForbidden {
		t.Fatalf("agent token on /mcp/dispatch = %d", status)
	}
	dispatcher := connectMCP(t, server.URL+"/mcp/dispatch", "dispatch-token", "Adastrion dispatcher")
	claim := callTool[dispatchClaim](t, dispatcher, "claim_research_task", map[string]any{"lease_seconds": 120})
	if !claim.Claimed || claim.Task.ID != created["Mine"].ID || claim.Token == "" || claim.Endpoint != "https://ledger.example.com/mcp/research" ||
		claim.Reason != "first_run" || claim.Chat == nil || !strings.Contains(claim.Chat.Opening, "Call get_task first") || claim.Available == nil {
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
	if strings.Join(names, ",") != "ask_owner,checkpoint,get_task,heartbeat,read_file,submit" {
		t.Fatalf("research tools = %v", names)
	}
	pack := callTool[researchContextOutput](t, run, "get_task", map[string]any{})
	if pack.Task.ID != created["Mine"].ID || pack.Task.Attempt != 1 || pack.Task.Title != "Mine" {
		t.Fatalf("get_task = %#v", pack.Task)
	}
	var listedFile string
	for _, note := range pack.Thread {
		for _, file := range note.Files {
			listedFile = file.ID
		}
	}
	if listedFile != mineFile {
		t.Fatalf("thread files = %#v, want %s", pack.Thread, mineFile)
	}
	read, err := run.CallTool(ctx, &mcp.CallToolParams{Name: "read_file", Arguments: map[string]any{"file_id": mineFile}})
	if err != nil || read.IsError || string(read.Content[0].(*mcp.EmbeddedResource).Resource.Blob) != "mine.csv data" {
		t.Fatalf("read own file = %#v, %v", read, err)
	}
	if other, err := run.CallTool(ctx, &mcp.CallToolParams{Name: "read_file", Arguments: map[string]any{"file_id": otherFile}}); err != nil || !other.IsError {
		t.Fatalf("read another task's file = %#v, %v", other, err)
	}
	callTool[leaseOutput](t, run, "checkpoint", map[string]any{"state": "two sources read"})
	callTool[leaseOutput](t, dispatcher, "renew_research_lease", map[string]any{"task_id": claim.Task.ID, "attempt": 1})
	submitted := callTool[submitOutput](t, run, "submit", map[string]any{"deliverable": "# Result", "files": []map[string]any{{"filename": "notes.txt", "content_base64": "aGk="}}})
	if submitted.Phase != "review" {
		t.Fatalf("submit = %#v", submitted)
	}
	// The run is over: its token is dead, and the dispatcher's renewal says so.
	if status := rawStatus(t, server.URL+"/mcp/research", claim.Token); status != http.StatusUnauthorized {
		t.Fatalf("token after submit = %d", status)
	}
	lost, err := dispatcher.CallTool(ctx, &mcp.CallToolParams{Name: "renew_research_lease", Arguments: map[string]any{"task_id": claim.Task.ID, "attempt": 1}})
	if err != nil || !lost.IsError || !strings.Contains(lost.Content[0].(*mcp.TextContent).Text, "lease_lost") {
		t.Fatalf("renew after submit = %#v, %v", lost, err)
	}
	ended := callTool[researchTaskOutput](t, dispatcher, "end_research_run", map[string]any{"task_id": claim.Task.ID, "attempt": 1, "error": "exit 0"})
	if ended.Phase != "review" || ended.Failures != 0 {
		t.Fatalf("end after submit = %#v", ended)
	}
	var files int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM handoff_file f JOIN handoff_message m ON m.id=f.message_id WHERE m.handoff_id=$1::bigint`, created["Mine"].ID).Scan(&files); err != nil || files != 2 {
		t.Fatalf("submitted files = %d, %v", files, err)
	}
}

// Research runs until done: create_research_task has no budget input, refuses stale budget fields and
// their aliases in the handler (creating nothing), and every output carries execution_mode until_done.
func TestResearchCreationIsUntilDoneOnly(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	addAccess(t, db, ctx, "agent-token", []string{"ledger:read", "ledger:write"})
	server := httptest.NewServer(HTTPHandler(NewServer(db, "http://unused"), db, "https://ledger.example.com"))
	defer server.Close()
	agent := connectMCP(t, server.URL+"/mcp", "agent-token", "claude-code")

	tools, err := agent.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "create_research_task" {
			schema, _ := json.Marshal(tool.InputSchema)
			for _, banned := range []string{"budget", "rounds", "minutes", "tokens", "max_turns", "max_time", "max_tokens", "execution_mode"} {
				if strings.Contains(string(schema), `"`+banned+`"`) {
					t.Errorf("create_research_task input schema offers %q: %s", banned, schema)
				}
			}
		}
	}
	base := map[string]any{"project_slug": "atlas", "title": "Survey", "objective": "Compare options", "acceptance": []string{"Cite sources"}, "deliverable": "report"}
	for name, value := range map[string]any{
		"budget": map[string]any{"rounds": 1, "minutes": 1, "tokens": 1}, "max_turns": 5, "max_time": 60, "max_tokens": 1000,
		"rounds": 5, "minutes": 5, "tokens": 5, "execution_mode": "max_turns",
	} {
		arguments := map[string]any{name: value}
		for k, v := range base {
			arguments[k] = v
		}
		result, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "create_research_task", Arguments: arguments})
		if err == nil && !result.IsError {
			t.Errorf("create_research_task accepted %q", name)
		}
	}
	var created int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM research_task`).Scan(&created); err != nil || created != 0 {
		t.Fatalf("refused calls created %d tasks, %v", created, err)
	}

	result, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "create_research_task", Arguments: base})
	if err != nil || result.IsError {
		t.Fatalf("budgetless create = %#v, %v", result, err)
	}
	structured, _ := json.Marshal(result.StructuredContent)
	text := result.Content[0].(*mcp.TextContent).Text
	for name, body := range map[string]string{"structured": string(structured), "text": text} {
		if !strings.Contains(body, `"execution_mode":"until_done"`) || strings.Contains(body, "budget") {
			t.Errorf("%s output = %s", name, body)
		}
	}
}

// Agents follow research through review: they see status and progress, read a submitted result and its
// files while it awaits the owner, and once the owner accepts it the result is published to the project
// log (or to the Research project when the task named none).
func TestAgentsSeeResearchThroughReviewAndPublication(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	addAccess(t, db, ctx, "agent-token", []string{"ledger:read", "ledger:write"})
	server := httptest.NewServer(HTTPHandler(NewServer(db, "http://unused"), db, "https://ledger.example.com"))
	defer server.Close()
	agent := connectMCP(t, server.URL+"/mcp", "agent-token", "claude-code")

	inAtlas := callTool[researchTaskOutput](t, agent, "create_research_task", map[string]any{"project_slug": "atlas", "title": "Engines", "objective": "Compare engines", "acceptance": []string{"Cite code"}})
	loose := callTool[researchTaskOutput](t, agent, "create_research_task", map[string]any{"title": "Loose survey", "objective": "Survey", "acceptance": []string{"Cite"}})
	listed := callTool[researchTaskList](t, agent, "list_research_tasks", map[string]any{"status": "queued"})
	if len(listed.Tasks) != 2 {
		t.Fatalf("queued = %#v", listed.Tasks)
	}

	id, _ := strconv.ParseInt(inAtlas.ID, 10, 64)
	c, err := db.ClaimResearchTask(ctx, 60, "Adastrion", "dispatcher")
	if err != nil || c.Task.ID != id {
		t.Fatalf("claim = %#v, %v", c, err)
	}
	if _, err := db.ResearchHeartbeat(ctx, id, 1, "reading the scheduler"); err != nil {
		t.Fatal(err)
	}
	running := callTool[researchTaskList](t, agent, "list_research_tasks", map[string]any{"status": "running", "project_slug": "atlas"})
	if len(running.Tasks) != 1 || running.Tasks[0].Progress != "reading the scheduler" || running.Tasks[0].Runner != "Adastrion" {
		t.Fatalf("running = %#v", running.Tasks)
	}
	submitted, err := db.SubmitResearch(ctx, id, 1, "# Engines compared\n\nvLLM batches continuously.", []store.ResearchFile{{Filename: "table.csv", MediaType: "text/csv", Data: []byte("engine,batching\n")}})
	if err != nil {
		t.Fatal(err)
	}
	view := callTool[researchView](t, agent, "get_research_task", map[string]any{"id": inAtlas.ID})
	last := view.Thread[len(view.Thread)-1]
	if view.Result != "awaiting_review" || last.From != "researcher" || !strings.HasPrefix(last.Body, "# Engines compared") || len(last.Files) != 1 {
		t.Fatalf("under review = %#v", view)
	}
	file, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "read_handoff_file", Arguments: map[string]any{"file_id": strconv.FormatInt(submitted.Files[0].ID, 10)}})
	if err != nil || file.IsError || string(file.Content[0].(*mcp.EmbeddedResource).Resource.Blob) != "engine,batching\n" {
		t.Fatalf("read result file = %#v, %v", file, err)
	}

	// The owner accepts: the result is published to Atlas's log, where search and get_project find it.
	if _, err := db.UpdateHandoffMessage(ctx, c.Task.MessageID, "complete", "", store.OwnerSource, "owner", true); err != nil {
		t.Fatal(err)
	}
	if accepted := callTool[researchView](t, agent, "get_research_task", map[string]any{"id": inAtlas.ID}); accepted.Result != "accepted" {
		t.Fatalf("after accept = %#v", accepted.Result)
	}
	project := callTool[store.ProjectWithEntries](t, agent, "get_project", map[string]any{"slug": "atlas"})
	if len(project.Entries) != 1 || !strings.HasPrefix(project.Entries[0].Body, "Accepted research #"+inAtlas.ID+": Engines") || !strings.Contains(project.Entries[0].Body, "vLLM batches continuously.") ||
		!strings.Contains(project.Entries[0].Body, `1 file(s) (table.csv)`) || project.Entries[0].Kind != "note" || project.Entries[0].Source != store.OwnerSource {
		t.Fatalf("published entry = %#v", project.Entries)
	}

	// A task with no project publishes to the Research project, created on first use.
	looseID, _ := strconv.ParseInt(loose.ID, 10, 64)
	lc, _ := db.ClaimResearchTask(ctx, 60, "Adastrion", "dispatcher")
	if lc == nil || lc.Task.ID != looseID {
		t.Fatalf("loose claim = %#v", lc)
	}
	if _, err := db.SubmitResearch(ctx, looseID, 1, "Loose findings", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, lc.Task.MessageID, "complete", "", store.OwnerSource, "owner", true); err != nil {
		t.Fatal(err)
	}
	research := callTool[store.ProjectWithEntries](t, agent, "get_project", map[string]any{"slug": store.ResearchProjectSlug})
	if research.Project.Name != "Research" || len(research.Entries) != 1 || !strings.Contains(research.Entries[0].Body, "Loose findings") {
		t.Fatalf("research project = %#v", research)
	}
}

// An agent reviews like the owner: send_back needs feedback and queues a revision the next run reads;
// accept publishes the result and records which agent accepted it; nothing waiting means nothing to review.
func TestAgentsReviewResearch(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	addAccess(t, db, ctx, "agent-token", []string{"ledger:read", "ledger:write"})
	server := httptest.NewServer(HTTPHandler(NewServer(db, "http://unused"), db, "https://ledger.example.com"))
	defer server.Close()
	agent := connectMCP(t, server.URL+"/mcp", "agent-token", "claude-code")
	task := callTool[researchTaskOutput](t, agent, "create_research_task", map[string]any{"project_slug": "atlas", "title": "Engines", "objective": "Compare engines", "acceptance": []string{"Cite code"}})
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	review := func(arguments map[string]any) *mcp.CallToolResult {
		t.Helper()
		arguments["id"] = task.ID
		result, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "review_research_task", Arguments: arguments})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if early := review(map[string]any{"action": "accept"}); !early.IsError {
		t.Fatal("accepted a task with no result")
	}
	c, _ := db.ClaimResearchTask(ctx, 60, "Adastrion", "dispatcher")
	if _, err := db.SubmitResearch(ctx, id, 1, "First draft without citations", nil); err != nil {
		t.Fatal(err)
	}
	if bare := review(map[string]any{"action": "send_back"}); !bare.IsError {
		t.Fatal("sent back without feedback")
	}
	if sent := review(map[string]any{"action": "send_back", "feedback": "Cite file paths with line numbers."}); sent.IsError {
		t.Fatalf("send_back = %#v", sent)
	}
	next, err := db.ClaimResearchTask(ctx, 60, "Adastrion", "dispatcher")
	if err != nil || next == nil || next.Reason != "revision" || next.Task.Attempt != 2 || c.Task.ID != id {
		t.Fatalf("revision claim = %#v, %v", next, err)
	}
	pack, _ := db.ResearchContext(ctx, id, nil)
	var feedback bool
	for _, note := range pack.Thread {
		feedback = feedback || note.From == "agent" && note.Body == "Cite file paths with line numbers."
	}
	if !feedback {
		t.Fatalf("thread lacks the agent's feedback: %#v", pack.Thread)
	}
	if _, err := db.SubmitResearch(ctx, id, 2, "Final, cited: vllm/core/scheduler.py:120", nil); err != nil {
		t.Fatal(err)
	}
	if accepted := review(map[string]any{"action": "accept"}); accepted.IsError {
		t.Fatalf("accept = %#v", accepted)
	}
	project := callTool[store.ProjectWithEntries](t, agent, "get_project", map[string]any{"slug": "atlas"})
	if len(project.Entries) != 1 || !strings.Contains(project.Entries[0].Body, "Final, cited") || project.Entries[0].Context != "research task #"+task.ID+", accepted by claude-code" {
		t.Fatalf("published = %#v", project.Entries)
	}
	if again := review(map[string]any{"action": "accept"}); !again.IsError {
		t.Fatal("accepted twice")
	}
}

// A draft research task stays with the client that created it until it is queued.
func TestDraftResearchStaysWithItsCreator(t *testing.T) {
	db, ctx := testdb.Open(t)
	addAccess(t, db, ctx, "creator-token", []string{"ledger:read", "ledger:write"})
	if _, err := db.PutClient(ctx, store.OAuthClient{ClientID: "other", Kind: "dcr", Name: "Other", RedirectURIs: []string{"http://127.0.0.1/cb"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO oauth_token(hash,kind,client_id,scope,family,expires_at) VALUES(sha256('other-token'::bytea),'access','other','ledger:read ledger:write','00000000-0000-4000-8000-000000000002',now()+interval '15 minutes')`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(HTTPHandler(NewServer(db, "http://unused"), db, "https://ledger.example.com"))
	defer server.Close()
	creator := connectMCP(t, server.URL+"/mcp", "creator-token", "creator")
	other := connectMCP(t, server.URL+"/mcp", "other-token", "other")
	draft := callTool[researchTaskOutput](t, creator, "create_research_task", map[string]any{"title": "Secret plan", "objective": "Private", "acceptance": []string{"x"}, "draft": true})

	if mine := callTool[researchTaskList](t, creator, "list_research_tasks", map[string]any{"status": "draft"}); len(mine.Tasks) != 1 {
		t.Fatalf("creator's drafts = %#v", mine.Tasks)
	}
	if callTool[researchView](t, creator, "get_research_task", map[string]any{"id": draft.ID}).Task.Title != "Secret plan" {
		t.Fatal("creator cannot read its draft")
	}
	if theirs := callTool[researchTaskList](t, other, "list_research_tasks", map[string]any{"status": "all"}); len(theirs.Tasks) != 0 {
		t.Fatalf("another client lists the draft: %#v", theirs.Tasks)
	}
	if peek, err := other.CallTool(ctx, &mcp.CallToolParams{Name: "get_research_task", Arguments: map[string]any{"id": draft.ID}}); err != nil || !peek.IsError {
		t.Fatalf("another client reads the draft: %#v, %v", peek, err)
	}
	_, key, err := db.CreateAPIKey(ctx, "Dispatcher", []string{store.ScopeResearchDispatch})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := apiCall(t, "GET", server.URL+"/api/v1/research/tasks/"+draft.ID, key, ""); status != http.StatusNotFound {
		t.Fatalf("API reads another client's draft: %d", status)
	}
	// Files in a draft task (such as a continuation's copied result) are the creator's too.
	id, _ := strconv.ParseInt(draft.ID, 10, 64)
	var noteID int64
	if err := db.Pool.QueryRow(ctx, `INSERT INTO handoff_message(handoff_id,body,work_state,source,client_id,status_updated_source,status_updated_client_id) VALUES($1,'copied result','done','ledger','ledger','ledger','ledger') RETURNING id`, id).Scan(&noteID); err != nil {
		t.Fatal(err)
	}
	var fileID int64
	if err := db.Pool.QueryRow(ctx, `INSERT INTO handoff_file(message_id,filename,media_type,size_bytes,sha256,data) VALUES($1,'copied.csv','text/csv',1,sha256('x'::bytea),'x') RETURNING id`, noteID).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	if peek, err := other.CallTool(ctx, &mcp.CallToolParams{Name: "read_handoff_file", Arguments: map[string]any{"file_id": strconv.FormatInt(fileID, 10)}}); err != nil || !peek.IsError {
		t.Fatalf("another client reads a draft task's file: %#v, %v", peek, err)
	}
	if mine, err := creator.CallTool(ctx, &mcp.CallToolParams{Name: "read_handoff_file", Arguments: map[string]any{"file_id": strconv.FormatInt(fileID, 10)}}); err != nil || mine.IsError {
		t.Fatalf("creator cannot read its draft's file: %#v, %v", mine, err)
	}
}

// An agent cannot review or create research as the owner.
func TestResearchToolsRejectTheOwnerName(t *testing.T) {
	db, ctx := testdb.Open(t)
	addAccess(t, db, ctx, "agent-token", []string{"ledger:read", "ledger:write"})
	server := httptest.NewServer(HTTPHandler(NewServer(db, "http://unused"), db, "https://ledger.example.com"))
	defer server.Close()
	impostor := connectMCP(t, server.URL+"/mcp", "agent-token", store.OwnerSource)
	for name, arguments := range map[string]map[string]any{
		"create_research_task": {"title": "x", "objective": "x", "acceptance": []string{"x"}},
		"review_research_task": {"id": "1", "action": "accept"},
	} {
		if result, err := impostor.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments}); err == nil && !result.IsError {
			t.Errorf("%s accepted the owner's name", name)
		}
	}
}
