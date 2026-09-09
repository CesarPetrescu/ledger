package mcpserver

import (
	"context"
	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addGlassTools(server *mcp.Server, db *store.DB) {
	type entryInput struct {
		ID string `json:"id" jsonschema:"exact entry ID from search or changes"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "get_entry", Description: "Read an immutable source entry by ID. " + DescriptionSuffix, OutputSchema: outputSchema[store.EntryView](), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, in entryInput) (*mcp.CallToolResult, any, error) {
		if !canRead(ctx) {
			return scopeError(), nil, nil
		}
		v, err := db.GetEntry(ctx, in.ID)
		return nil, v, err
	})
	type changesInput struct {
		Reader  string `json:"reader,omitempty" jsonschema:"independent device reader namespace, default glass"`
		After   string `json:"after,omitempty" jsonschema:"next_cursor from the previous fetched page; omit to resume acknowledged position"`
		Through string `json:"through,omitempty" jsonschema:"snapshot through from the first page; omit to start a snapshot"`
		Limit   int    `json:"limit,omitempty" jsonschema:"page size, default 18, maximum 100"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "list_changes", Description: "Read complete, commit-ordered entry changes; records delivery but never acknowledges reading. " + DescriptionSuffix, OutputSchema: outputSchema[store.ChangePage](), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false)}}, func(ctx context.Context, _ *mcp.CallToolRequest, in changesInput) (*mcp.CallToolResult, any, error) {
		if !canRead(ctx) {
			return scopeError(), nil, nil
		}
		if in.Reader == "" {
			in.Reader = "glass"
		}
		if in.Limit == 0 {
			in.Limit = 18
		}
		v, err := db.Changes(ctx, identityFrom(ctx).ClientID, in.Reader, in.After, in.Through, in.Limit)
		return nil, v, err
	})
	type ackInput struct {
		Reader  string `json:"reader,omitempty" jsonschema:"reader namespace, default glass"`
		Through string `json:"through" jsonschema:"last cursor the user explicitly acknowledged; cannot exceed delivered data"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "ack_changes", Description: "Acknowledge fetched changes for this device only; does not mutate project entries. " + DescriptionSuffix, OutputSchema: outputSchema[store.ReadReceipt](), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false)}}, func(ctx context.Context, _ *mcp.CallToolRequest, in ackInput) (*mcp.CallToolResult, any, error) {
		if !canRead(ctx) {
			return scopeError(), nil, nil
		}
		if in.Reader == "" {
			in.Reader = "glass"
		}
		v, err := db.AcknowledgeChanges(ctx, identityFrom(ctx).ClientID, in.Reader, in.Through)
		return nil, v, err
	})
}
