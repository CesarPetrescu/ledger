package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	calendarapi "github.com/cesarpetrescu/ledger/internal/calendar"
	"github.com/cesarpetrescu/ledger/internal/retrieval"
	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/transcription"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolsListIsExactAndAnnotated(t *testing.T) {
	ctx := context.Background()
	server := NewServer(nil, "")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"get_entry": true, "list_changes": false, "ack_changes": false, "transcribe_audio": true,
		"list_projects": true, "get_project": true, "search": true, "upsert_project": false, "append_entry": false,
		"list_calendars": true, "list_calendar_events": true, "create_calendar_event": false, "update_calendar_event": false, "delete_calendar_event": false,
		"list_handoffs": true, "get_handoff": true, "create_handoff": false, "append_handoff_message": false,
		"update_handoff_message": false, "attach_handoff_file": false, "read_handoff_file": true,
		"create_research_task": false, "list_research_tasks": true, "get_research_task": true, "review_research_task": false,
		"list_repos": true, "link_repo": false, "unlink_repo": false,
	}
	if len(result.Tools) != len(want) {
		t.Fatalf("got %d tools", len(result.Tools))
	}
	now := time.Now().UTC()
	file := store.HandoffFile{ID: 9007199254740993, MessageID: 2, CreatedAt: now}
	message := store.HandoffMessage{ID: 2, HandoffID: 1, Files: []store.HandoffFile{file}}
	detail := handoffDetailOutput(store.HandoffDetail{Messages: []store.HandoffMessage{message}})
	cursor := int64(2)
	page := handoffDetailOutput(store.HandoffDetail{NextBefore: &cursor})
	samples := map[string][]any{
		"get_entry":              {store.EntryView{}},
		"list_changes":           {store.ChangePage{Entries: []store.Change{}}},
		"ack_changes":            {store.ReadReceipt{Checkpoint: "0"}},
		"transcribe_audio":       {transcription.Result{Text: "Atlas note"}},
		"list_projects":          {[]any{}, []any{map[string]any{"slug": "atlas", "name": "Atlas", "tier": "focus", "hours_wk": 8, "goal": "Ship", "deadline": "", "last_entry_at": nil}}, []any{map[string]any{"slug": "atlas", "name": "Atlas", "tier": "focus", "hours_wk": 8, "goal": "Ship", "deadline": "", "last_entry_at": now}}},
		"get_project":            {projectOutput{ProjectWithEntries: store.ProjectWithEntries{Entries: []store.Entry{}}, Repos: []store.ProjectRepo{}}, projectOutput{ProjectWithEntries: store.ProjectWithEntries{Entries: []store.Entry{}}, Repos: []store.ProjectRepo{repoSample(now)}}},
		"list_repos":             {repoList{Repos: []store.ProjectRepo{}}, repoList{Repos: []store.ProjectRepo{repoSample(now)}}},
		"link_repo":              {repoSample(now)},
		"unlink_repo":            {repoSample(now)},
		"search":                 {retrieval.SearchResult{Hits: []retrieval.Ranked{}, Degraded: []string{}}, retrieval.SearchResult{Hits: []retrieval.Ranked{{Ref: "entry:1", Score: 0.5}}, Degraded: []string{"rerank"}}},
		"upsert_project":         {store.Project{}, store.Project{LastEntryAt: &now}},
		"append_entry":           {map[string]any{"id": int64(1), "created_at": now}},
		"list_calendars":         {[]calendarapi.Calendar{}, []calendarapi.Calendar{{ID: "calendar", Selected: true}}},
		"list_calendar_events":   {[]calendarapi.Event{}, []calendarapi.Event{{ID: "event"}}},
		"create_calendar_event":  {calendarapi.Event{}},
		"update_calendar_event":  {calendarapi.Event{Description: "Updated"}},
		"delete_calendar_event":  {map[string]bool{"deleted": true}},
		"list_handoffs":          {map[string]any{"handoffs": []any{}}, map[string]any{"handoffs": []any{handoffOutput(store.Handoff{ArchivedAt: &now})}, "next_before": now.Format(time.RFC3339Nano) + "|1"}},
		"get_handoff":            {detail, page},
		"create_handoff":         {detail},
		"append_handoff_message": {handoffMessageOutput(message)},
		"update_handoff_message": {handoffMessageOutput(store.HandoffMessage{SeenAt: &now, ClaimedAt: &now})},
		"attach_handoff_file":    {handoffFileOutput(file)},
		"read_handoff_file":      {map[string]string{"id": "9007199254740993", "filename": "note.txt", "uri": "ledger://handoff-file/9007199254740993"}},
		"create_research_task":   {taskOutput(researchSample(now))},
		"review_research_task":   {taskOutput(researchSample(now))},
		"list_research_tasks":    {researchTaskList{Tasks: []store.ResearchSummary{}}, researchTaskList{Tasks: []store.ResearchSummary{{ID: "9", Title: "Survey", Status: "review", Attempt: 1, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now, HeartbeatAt: &now}}}},
		"get_research_task":      {researchView{researchContextOutput: contextOutput(store.ResearchContext{Task: researchSample(now), Files: []store.HandoffFile{}, Thread: []store.ResearchNote{{From: "researcher", Body: "# Result", At: now, Files: []store.HandoffFile{}}}}), Result: "awaiting_review"}},
	}
	for name, key := range map[string]string{"list_projects": "projects", "list_calendars": "calendars", "list_calendar_events": "events"} {
		for i, value := range samples[name] {
			samples[name][i] = map[string]any{key: value}
		}
	}
	for _, tool := range result.Tools {
		if tool.OutputSchema == nil {
			t.Errorf("tool %q missing output schema", tool.Name)
			continue
		}
		var schema jsonschema.Schema
		encoded, err := json.Marshal(tool.OutputSchema)
		if err != nil || json.Unmarshal(encoded, &schema) != nil {
			t.Fatalf("tool %q invalid output schema: %s, %v", tool.Name, encoded, err)
		}
		if schema.Type != "object" {
			t.Errorf("tool %q output schema must have an object root for client compatibility", tool.Name)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(samples[tool.Name]) == 0 {
			t.Errorf("tool %q missing output samples", tool.Name)
		}
		for _, sample := range samples[tool.Name] {
			encoded, err := json.Marshal(sample)
			var value any
			if err != nil || json.Unmarshal(encoded, &value) != nil {
				t.Fatalf("tool %q invalid sample: %v", tool.Name, err)
			}
			if err := resolved.Validate(value); err != nil {
				t.Errorf("tool %q output does not match schema: %v", tool.Name, err)
			}
		}
		if resolved.Validate(map[string]any{"unexpected": true}) == nil {
			t.Errorf("tool %q accepts an invalid output", tool.Name)
		}
		readOnly, ok := want[tool.Name]
		if !ok || tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly {
			t.Errorf("tool %q annotations = %#v", tool.Name, tool.Annotations)
		}
		suffix := DescriptionSuffix
		if strings.Contains(tool.Name, "calendar") {
			suffix = CalendarDescriptionSuffix
			if tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
				t.Errorf("calendar tool %q must declare open-world access", tool.Name)
			}
		} else if strings.Contains(tool.Name, "handoff") {
			suffix = HandoffDescriptionSuffix
		} else if strings.Contains(tool.Name, "research") {
			suffix = ResearchDescriptionSuffix
		} else if strings.HasSuffix(tool.Name, "_repo") || strings.HasSuffix(tool.Name, "_repos") {
			suffix = RepoDescriptionSuffix
		}
		if !strings.HasSuffix(tool.Description, suffix) {
			t.Errorf("tool %q description missing required suffix", tool.Name)
		}
	}
}

func TestProtectedResourceMetadataIsPublicButMCPIsNot(t *testing.T) {
	handler := HTTPHandler(NewServer(nil, ""), nil, "https://ledger.example.com")
	metadata := httptest.NewRecorder()
	handler.ServeHTTP(metadata, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	if metadata.Code != http.StatusOK || !strings.Contains(metadata.Body.String(), `"resource":"https://ledger.example.com/mcp"`) {
		t.Fatalf("metadata = %d %s", metadata.Code, metadata.Body.String())
	}
	var document struct {
		Scopes []string `json:"scopes_supported"`
	}
	if err := json.Unmarshal(metadata.Body.Bytes(), &document); err != nil || len(document.Scopes) != 4 || document.Scopes[0] != "ledger:read" || document.Scopes[1] != "ledger:write" || document.Scopes[2] != "calendar:read" || document.Scopes[3] != "calendar:write" {
		t.Fatalf("metadata scopes = %v, %v", document.Scopes, err)
	}
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if unauthenticated.Code != http.StatusUnauthorized || unauthenticated.Header().Get("WWW-Authenticate") != `Bearer resource_metadata="https://ledger.example.com/.well-known/oauth-protected-resource", scope="ledger:read ledger:write calendar:read calendar:write"` {
		t.Fatalf("unauthenticated MCP = %d, %q", unauthenticated.Code, unauthenticated.Header().Get("WWW-Authenticate"))
	}
	dispatcher := httptest.NewRecorder()
	handler.ServeHTTP(dispatcher, httptest.NewRequest(http.MethodPost, "/mcp/dispatch", nil))
	if dispatcher.Code != http.StatusUnauthorized || !strings.HasSuffix(dispatcher.Header().Get("WWW-Authenticate"), `, scope="research:dispatch"`) {
		t.Fatalf("unauthenticated dispatch = %d, %q", dispatcher.Code, dispatcher.Header().Get("WWW-Authenticate"))
	}
}

func TestBearerTokenParsesHTTPAuthorizationScheme(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{name: "canonical", values: []string{"Bearer token"}, want: "token"},
		{name: "mixed case", values: []string{"bEaReR token"}, want: "token"},
		{name: "spaces", values: []string{"Bearer   token"}, want: "token"},
		{name: "surrounding OWS", values: []string{"\tBearer token \t"}, want: "token"},
		{name: "missing"},
		{name: "empty", values: []string{""}},
		{name: "missing credentials", values: []string{"Bearer"}},
		{name: "empty credentials", values: []string{"Bearer "}},
		{name: "tab separator", values: []string{"Bearer\ttoken"}},
		{name: "extra credentials", values: []string{"Bearer token extra"}},
		{name: "embedded tab", values: []string{"Bearer tok\ten"}},
		{name: "combined", values: []string{"Bearer token, Basic other"}},
		{name: "comma in credentials", values: []string{"Bearer token,other"}},
		{name: "wrong scheme", values: []string{"Basic token"}},
		{name: "duplicate", values: []string{"Bearer token", "Bearer other"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{"Authorization": test.values}
			got, ok := bearerToken(header)
			if got != test.want || ok != (test.want != "") {
				t.Fatalf("bearerToken(%q) = %q, %v; want %q, %v", test.values, got, ok, test.want, test.want != "")
			}
		})
	}
}

func researchSample(now time.Time) store.ResearchTask {
	attempt := 1
	return store.ResearchTask{ID: 4, MessageID: 9, Title: "Survey", State: "in_progress", DependsOn: []int64{}, Spec: store.ResearchSpec{Objective: "Find", Acceptance: []string{"Cited"}, Deliverable: "report"},
		Attempt: 1, MaxAttempts: 3, LeaseUntil: &now, Checkpoint: "step 2", CheckpointAttempt: &attempt, CheckpointAt: &now}
}

// The research and dispatch endpoints expose exactly their own tools, each with an object output schema
// that the tool's real output satisfies.
func TestResearchAndDispatchToolsAreExact(t *testing.T) {
	now := time.Now().UTC()
	task := researchSample(now)
	task.DependsOn = []int64{9007199254740993}
	out := taskOutput(task)
	if encoded, _ := json.Marshal(out); !strings.Contains(string(encoded), `"id":"4"`) || !strings.Contains(string(encoded), `"message_id":"9"`) || !strings.Contains(string(encoded), `"depends_on":["9007199254740993"]`) {
		t.Fatalf("task output IDs must be strings: %s", encoded)
	}
	schema := outputSchema[researchTaskOutput]()
	for _, field := range []string{"id", "message_id"} {
		if property := schema.Properties[field]; property == nil || property.Type != "string" {
			t.Fatalf("schema %s = %#v, want a string", field, property)
		}
	}
	for name, test := range map[string]struct {
		server  *mcp.Server
		samples map[string]any
	}{
		"research": {NewResearchServer(nil), map[string]any{
			"get_task":   contextOutput(store.ResearchContext{Task: task, Files: []store.HandoffFile{}, Project: &store.ResearchProject{Slug: "atlas", Name: "Atlas"}, Thread: []store.ResearchNote{{From: "owner", Body: "Use 2026 data", At: now, Files: []store.HandoffFile{{ID: 9007199254740993, MessageID: 6, Filename: "bench.csv", CreatedAt: now}}}}}),
			"heartbeat":  leaseOutput{LeaseUntil: now},
			"checkpoint": leaseOutput{LeaseUntil: now},
			"submit":     submitOutput{MessageID: "12", State: "blocked", Phase: "review"},
			"ask_owner":  stateOutput{State: "blocked", Phase: "question"},
			"read_file":  handoffFileResource{ID: "5", Filename: "bench.csv", URI: "ledger://research-file/5"},
		}},
		"dispatch": {NewDispatchServer(nil, "https://ledger.example.com"), map[string]any{
			"claim_research_task":  dispatchClaim{Claimed: true, Task: &out, Token: "t", Endpoint: "https://ledger.example.com/mcp/research"},
			"renew_research_lease": leaseOutput{LeaseUntil: now},
			"end_research_run":     out,
		}},
	} {
		ctx := context.Background()
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		serverSession, err := test.server.Connect(ctx, serverTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Tools) != len(test.samples) {
			t.Errorf("%s: got %d tools, want %d", name, len(result.Tools), len(test.samples))
		}
		for _, tool := range result.Tools {
			sample, ok := test.samples[tool.Name]
			if !ok {
				t.Errorf("%s: unexpected tool %q", name, tool.Name)
				continue
			}
			var schema jsonschema.Schema
			encoded, _ := json.Marshal(tool.OutputSchema)
			if err := json.Unmarshal(encoded, &schema); err != nil || schema.Type != "object" {
				t.Fatalf("%s/%s: output schema %s", name, tool.Name, encoded)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			encoded, _ = json.Marshal(sample)
			_ = json.Unmarshal(encoded, &value)
			if err := resolved.Validate(value); err != nil {
				t.Errorf("%s/%s: output does not match schema: %v", name, tool.Name, err)
			}
		}
		session.Close()
		serverSession.Close()
	}
}

// Clients cache output schemas; a schema that forbids unknown fields makes every added field a breaking
// change for them (a new project field broke cached clients' get_project once). No tool output may.
func TestOutputSchemasAllowAddedFields(t *testing.T) {
	servers := map[string]*mcp.Server{"mcp": NewServer(nil, ""), "research": NewResearchServer(nil), "dispatch": NewDispatchServer(nil, "")}
	for name, server := range servers {
		ctx := context.Background()
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		serverSession, err := server.Connect(ctx, serverTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range result.Tools {
			encoded, _ := json.Marshal(tool.OutputSchema)
			if strings.Contains(string(encoded), `"additionalProperties":false`) {
				t.Errorf("%s/%s output schema forbids added fields: %s", name, tool.Name, encoded)
			}
		}
		session.Close()
		serverSession.Close()
	}
	// The project an agent receives keeps its published fields; owner-only fields stay out of it.
	project := outputSchema[store.ProjectWithEntries]().Properties["project"]
	var fields []string
	for field := range project.Properties {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	if got := strings.Join(fields, ","); got != "automate,deadline,description,goal,hours_wk,last_entry_at,name,needs_me,slug,stack,tier,type,updated_at" {
		t.Fatalf("project fields = %s", got)
	}
}

func repoSample(now time.Time) store.ProjectRepo {
	open, private := 2, true
	return store.ProjectRepo{ID: "1", ProjectSlug: "atlas", URL: "https://github.com/acme/atlas", Provider: "github", Repo: "acme/atlas", WebURL: "https://github.com/acme/atlas", AddedBy: "claude", CreatedAt: now,
		Sync: &store.RepoSync{SyncedAt: now, HeadSHA: "abc", HeadMessage: "Fix", HeadAt: &now, OpenPRs: &open, Private: &private, LatestRelease: "v1", LatestReleaseAt: &now}}
}
