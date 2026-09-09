package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		"get_project":            {store.ProjectWithEntries{Entries: []store.Entry{}}},
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
	if unauthenticated.Code != http.StatusUnauthorized || unauthenticated.Header().Get("WWW-Authenticate") != `Bearer resource_metadata="https://ledger.example.com/.well-known/oauth-protected-resource"` {
		t.Fatalf("unauthenticated MCP = %d, %q", unauthenticated.Code, unauthenticated.Header().Get("WWW-Authenticate"))
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
