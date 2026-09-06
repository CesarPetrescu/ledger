package mcpserver

import (
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// Keep handlers' error results unstructured while validating successful outputs.
func outputSchema[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	return schema
}

// These types describe the MCP projections, which differ from the stored rows.
type projectSummary struct {
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Tier        string     `json:"tier"`
	HoursWK     int        `json:"hours_wk"`
	Goal        string     `json:"goal"`
	Deadline    string     `json:"deadline"`
	LastEntryAt *time.Time `json:"last_entry_at"`
}

type entryReceipt struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type deleteReceipt struct {
	Deleted bool `json:"deleted"`
}

type handoffSummary struct {
	ID            string     `json:"id"`
	ProjectSlug   string     `json:"project_slug"`
	ProjectName   string     `json:"project_name"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Scope         string     `json:"scope"`
	Source        string     `json:"source"`
	ClientID      string     `json:"client_id"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ArchivedAt    *time.Time `json:"archived_at"`
	DraftCount    int        `json:"draft_count"`
	ReadyCount    int        `json:"ready_count"`
	ProgressCount int        `json:"in_progress_count"`
	BlockedCount  int        `json:"blocked_count"`
	DoneCount     int        `json:"done_count"`
}

type handoffMessage struct {
	ID                    string        `json:"id"`
	HandoffID             string        `json:"handoff_id"`
	Body                  string        `json:"body"`
	Target                string        `json:"target"`
	DeliveryState         string        `json:"delivery_state"`
	WorkState             string        `json:"work_state"`
	Source                string        `json:"source"`
	ClientID              string        `json:"client_id"`
	SeenAt                *time.Time    `json:"seen_at"`
	SeenSource            string        `json:"seen_source"`
	SeenClientID          string        `json:"seen_client_id"`
	ClaimedAt             *time.Time    `json:"claimed_at"`
	ClaimedSource         string        `json:"claimed_source"`
	ClaimedClientID       string        `json:"claimed_client_id"`
	StatusUpdatedAt       time.Time     `json:"status_updated_at"`
	StatusUpdatedSource   string        `json:"status_updated_source"`
	StatusUpdatedClientID string        `json:"status_updated_client_id"`
	CreatedAt             time.Time     `json:"created_at"`
	Files                 []handoffFile `json:"files"`
}

type handoffFile struct {
	ID        string    `json:"id"`
	MessageID string    `json:"message_id"`
	Filename  string    `json:"filename"`
	MediaType string    `json:"media_type"`
	SizeBytes int64     `json:"size_bytes"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

type handoffList struct {
	Handoffs   []handoffSummary `json:"handoffs"`
	NextBefore string           `json:"next_before,omitempty"`
}

type handoffDetail struct {
	Handoff    handoffSummary   `json:"handoff"`
	Messages   []handoffMessage `json:"messages"`
	NextBefore string           `json:"next_before,omitempty"`
}

type handoffFileResource struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	URI      string `json:"uri"`
}
