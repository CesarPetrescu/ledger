package mcpserver

import (
	"context"
	"github.com/cesarpetrescu/ledger/internal/oauth"
	"github.com/cesarpetrescu/ledger/internal/transcription"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"time"
)

func addSpeechTool(server *mcp.Server, speech *transcription.Client) {
	limiter := oauth.NewRateLimiter()
	type input struct {
		WAV      string `json:"wav_base64" jsonschema:"canonical 16kHz mono PCM WAV, base64, 0.1 to 30 seconds"`
		Language string `json:"language,omitempty" jsonschema:"en, ro or empty for auto detection"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "transcribe_audio", Description: "Transcribe a user-initiated recording using the configured speech provider; never saves an entry. " + DescriptionSuffix, OutputSchema: outputSchema[transcription.Result](), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, _ *mcp.CallToolRequest, in input) (*mcp.CallToolResult, any, error) {
		if !canRead(ctx) {
			return scopeError(), nil, nil
		}
		if !limiter.Allow("speech:"+identityFrom(ctx).ClientID, 10, time.Minute) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "speech rate limit exceeded; retry later"}}}, nil, nil
		}
		result, err := speech.Transcribe(ctx, in.WAV, in.Language)
		return nil, result, err
	})
}
