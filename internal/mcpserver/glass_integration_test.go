//go:build integration

package mcpserver

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/testdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGlassToolsRequireReadScopeBeforeTouchingDataOrProviders(t *testing.T) {
	db, ctx := testdb.Open(t)
	addAccess(t, db, ctx, "glass-no-read", []string{"ledger:write"})
	server := httptest.NewServer(HTTPHandler(NewServer(db, "http://127.0.0.1:1"), db, "https://ledger.example.com"))
	defer server.Close()
	session := connectMCP(t, server.URL+"/mcp", "glass-no-read", "permission-probe")
	for _, call := range []*mcp.CallToolParams{
		{Name: "get_entry", Arguments: map[string]any{"id": "1"}},
		{Name: "list_changes", Arguments: map[string]any{"reader": "glass"}},
		{Name: "ack_changes", Arguments: map[string]any{"reader": "glass", "through": "0"}},
		{Name: "transcribe_audio", Arguments: map[string]any{"wav_base64": "not-real-audio"}},
	} {
		result, err := session.CallTool(ctx, call)
		if err != nil || result == nil || !result.IsError || len(result.Content) != 1 {
			t.Fatalf("%s permission response: %#v %v", call.Name, result, err)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok || !strings.Contains(text.Text, "insufficient_scope") {
			t.Fatalf("%s did not enforce read scope before validation/provider access", call.Name)
		}
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM glass_reader`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("denied call created reader state: %d %v", count, err)
	}
}
