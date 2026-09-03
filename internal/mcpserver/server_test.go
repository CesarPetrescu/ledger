package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	want := map[string]bool{"list_projects": true, "get_project": true, "search": true, "upsert_project": false, "append_entry": false}
	if len(result.Tools) != len(want) {
		t.Fatalf("got %d tools", len(result.Tools))
	}
	for _, tool := range result.Tools {
		readOnly, ok := want[tool.Name]
		if !ok || tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly {
			t.Errorf("tool %q annotations = %#v", tool.Name, tool.Annotations)
		}
		if !strings.HasSuffix(tool.Description, DescriptionSuffix) {
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
	if err := json.Unmarshal(metadata.Body.Bytes(), &document); err != nil || len(document.Scopes) != 2 || document.Scopes[0] != "ledger:read" || document.Scopes[1] != "ledger:write" {
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
