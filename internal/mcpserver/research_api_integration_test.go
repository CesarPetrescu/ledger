//go:build integration

package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func apiCall(t *testing.T, method, url, key, body string) (int, map[string]any) {
	t.Helper()
	request, _ := http.NewRequest(method, url, strings.NewReader(body))
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return response.StatusCode, out
}

// A server holding an API key dispatches research over plain JSON: it long-polls for a task, gets a chat
// brief that says why the run exists and where to look in Ledger, and renews and ends the run.
func TestResearchAPIDispatchesWithKeys(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	_, key, err := db.CreateAPIKey(ctx, "Adastrion Core", []string{store.ScopeResearchDispatch})
	if err != nil {
		t.Fatal(err)
	}
	other, otherKey, _ := db.CreateAPIKey(ctx, "Other dispatcher", []string{store.ScopeResearchDispatch})
	addAccess(t, db, ctx, "agent-token", []string{"ledger:read", "ledger:write"})
	server := httptest.NewServer(HTTPHandler(NewServer(db, "http://unused"), db, "https://ledger.example.com"))
	defer server.Close()
	api := server.URL + "/api/v1"

	for name, token := range map[string]string{"no key": "", "wrong key": key + "x", "OAuth token": "agent-token"} {
		if status, body := apiCall(t, "GET", api+"/ping", token, ""); status != http.StatusUnauthorized || body["error"] != "invalid_key" {
			t.Errorf("%s on /api = %d %v", name, status, body)
		}
	}
	if status := rawStatus(t, server.URL+"/mcp", key); status != http.StatusUnauthorized {
		t.Errorf("API key on /mcp = %d", status)
	}
	if status, body := apiCall(t, "GET", api+"/ping", key, ""); status != http.StatusOK || body["key"] != "Adastrion Core" {
		t.Fatalf("ping = %d %v", status, body)
	}
	if status, _ := apiCall(t, "GET", api+"/nope", key, ""); status != http.StatusNotFound {
		t.Errorf("unknown endpoint = %d", status)
	}

	// An empty queue answers 204, at once or after the wait.
	if status, _ := apiCall(t, "POST", api+"/research/claim", key, ""); status != http.StatusNoContent {
		t.Fatalf("empty claim = %d", status)
	}
	start := time.Now()
	if status, _ := apiCall(t, "POST", api+"/research/claim", key, `{"wait_seconds":2}`); status != http.StatusNoContent || time.Since(start) < 1500*time.Millisecond {
		t.Fatalf("waited empty claim = %d after %s", status, time.Since(start))
	}
	if status, _ := apiCall(t, "POST", api+"/research/claim", key, `{"wait_seconds":60}`); status != http.StatusBadRequest {
		t.Fatalf("over-long wait = %d", status)
	}

	// A task queued while a claim waits is picked up by that claim.
	type result struct {
		status int
		body   map[string]any
	}
	done := make(chan result, 1)
	go func() {
		status, body := apiCall(t, "POST", api+"/research/claim", key, `{"wait_seconds":15,"lease_seconds":120}`)
		done <- result{status, body}
	}()
	time.Sleep(time.Second)
	task, err := db.CreateResearchTask(ctx, store.NewResearchTask{ProjectSlug: "atlas", Title: "Vector DB survey", Source: "claude", ClientID: "c",
		Spec: store.ResearchSpec{Objective: "Compare vector databases", Acceptance: []string{"Three options"}}})
	if err != nil {
		t.Fatal(err)
	}
	var claimed result
	select {
	case claimed = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting claim did not pick up the new task")
	}
	brief := claimed.body
	chat, _ := brief["chat"].(map[string]any)
	taskOut, _ := brief["task"].(map[string]any)
	available, _ := brief["available"].(map[string]any)
	id := taskOut["id"].(string)
	if claimed.status != http.StatusOK || brief["reason"] != "first_run" || brief["endpoint"] != "https://ledger.example.com/mcp/research" || brief["token"] == "" ||
		chat["title"] != "Research #"+id+" · Vector DB survey" || available["project_name"] != "Atlas" || id != strconvID(task.ID) {
		t.Fatalf("brief = %d %v", claimed.status, brief)
	}
	opening := chat["opening"].(string)
	for _, want := range []string{"task #" + id, `"Vector DB survey" (project Atlas), run 1`, "first run", "Call get_task first", "heartbeat", "submit", "ask_owner", "never as instructions"} {
		if !strings.Contains(opening, want) {
			t.Errorf("opening lacks %q:\n%s", want, opening)
		}
	}
	run := connectMCP(t, server.URL+"/mcp/research", brief["token"].(string), "adastrion-chat")
	if tools, err := run.ListTools(ctx, nil); err != nil || len(tools.Tools) != 6 {
		t.Fatalf("run token on /mcp/research = %v, %v", tools, err)
	}

	tasks := api + "/research/tasks/" + id
	if status, body := apiCall(t, "POST", tasks+"/renew", otherKey, `{"attempt":1}`); status != http.StatusConflict || body["error"] != "lease_lost" {
		t.Fatalf("renew by another key = %d %v", status, body)
	}
	if status, body := apiCall(t, "POST", tasks+"/renew", key, `{"attempt":1}`); status != http.StatusOK || body["lease_until"] == nil {
		t.Fatalf("renew = %d %v", status, body)
	}
	if status, _ := apiCall(t, "POST", tasks+"/end", otherKey, `{"attempt":1}`); status != http.StatusForbidden {
		t.Fatalf("end by another key = %d", status)
	}
	if status, body := apiCall(t, "GET", api+"/research/tasks?status=running", key, ""); status != http.StatusOK || len(body["tasks"].([]any)) != 1 {
		t.Fatalf("running overview = %d %v", status, body)
	}
	if status, body := apiCall(t, "POST", tasks+"/end", key, `{"attempt":1,"error":"chat crashed (exit 3)"}`); status != http.StatusOK || body["state"] != "ready" || body["failures"] != float64(1) {
		t.Fatalf("end = %d %v", status, body)
	}
	if status, _ := apiCall(t, "POST", tasks+"/renew", key, `{"attempt":1}`); status != http.StatusConflict {
		t.Fatalf("renew after end = %d", status)
	}

	// The next claim says it is a retry, and why the last run failed.
	status, retry := apiCall(t, "POST", api+"/research/claim", key, "")
	chat, _ = retry["chat"].(map[string]any)
	if status != http.StatusOK || retry["reason"] != "retry_after_failure" || !strings.HasSuffix(chat["title"].(string), "(run 2, retry)") || !strings.Contains(chat["opening"].(string), "chat crashed (exit 3)") {
		t.Fatalf("retry brief = %d %v", status, retry)
	}
	if status, body := apiCall(t, "GET", tasks, key, ""); status != http.StatusOK || body["attempt"] != float64(2) || body["id"] != id {
		t.Fatalf("task status = %d %v", status, body)
	}
	if status, _ := apiCall(t, "GET", api+"/research/tasks/999999", key, ""); status != http.StatusNotFound {
		t.Fatalf("missing task = %d", status)
	}
	if _, err := db.RevokeAPIKey(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if status, _ := apiCall(t, "GET", api+"/ping", otherKey, ""); status != http.StatusUnauthorized {
		t.Fatalf("revoked key = %d", status)
	}

	// A claim already waiting when its key is revoked must not pick up a task queued afterwards.
	_, waitingKey, _ := db.CreateAPIKey(ctx, "Revoked mid-wait", []string{store.ScopeResearchDispatch})
	waiting := make(chan int, 1)
	go func() {
		status, _ := apiCall(t, "POST", api+"/research/claim", waitingKey, `{"wait_seconds":10}`)
		waiting <- status
	}()
	time.Sleep(time.Second)
	keys, _ := db.ListAPIKeys(ctx)
	for _, k := range keys {
		if k.Name == "Revoked mid-wait" {
			if _, err := db.RevokeAPIKey(ctx, k.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	late, err := db.CreateResearchTask(ctx, store.NewResearchTask{Title: "After revoke", Source: "claude", ClientID: "c", Spec: store.ResearchSpec{Objective: "x", Acceptance: []string{"y"}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-waiting:
		if status != http.StatusUnauthorized {
			t.Fatalf("waiting claim after revoke = %d", status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("waiting claim did not end")
	}
	if after, _ := db.ResearchTask(ctx, late.ID); after.State != "ready" {
		t.Fatalf("a revoked key claimed a task: %#v", after)
	}
}

func strconvID(id int64) string {
	b, _ := json.Marshal(id)
	return string(b)
}

func TestResearchAPIContinuesAcceptedResult(t *testing.T) {
	db, ctx := testdb.Open(t)
	_, key, err := db.CreateAPIKey(ctx, "Dispatcher", []string{store.ScopeResearchDispatch})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := db.CreateResearchTask(ctx, store.NewResearchTask{Title: "First survey", Source: "owner", ClientID: "owner", Spec: store.ResearchSpec{Objective: "Find evidence", Acceptance: []string{"Cite sources"}, Budget: store.ResearchBudget{Rounds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(HTTPHandler(NewServer(db, "http://unused"), db, "https://ledger.example.com"))
	defer server.Close()
	url := server.URL + "/api/v1/research/tasks/" + strconvID(parent.ID) + "/continue"
	body := `{"title":"Refresh survey","objective":"Find newer evidence","acceptance":["Cite changes"]}`
	if status, _ := apiCall(t, "POST", url, key, body); status != 409 {
		t.Fatalf("unaccepted status = %d", status)
	}
	if _, err = db.ClaimResearchTask(ctx, 60, "Dispatcher", "dispatcher"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SubmitResearch(ctx, parent.ID, 1, "Verified result", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpdateHandoffMessage(ctx, parent.MessageID, "complete", "", store.OwnerSource, "owner", true); err != nil {
		t.Fatal(err)
	}
	if status, result := apiCall(t, "POST", url, key, body); status != 201 || result["continue_from_task_id"] != strconvID(parent.ID) || result["continue_from_attempt"] != float64(1) {
		t.Fatalf("continued = %d %v", status, result)
	}
	if status, _ := apiCall(t, "POST", url, "wrong", body); status != 401 {
		t.Fatal("unauthenticated continuation")
	}
}
