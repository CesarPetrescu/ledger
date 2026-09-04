package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"mime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	MaxHandoffFiles        = 10
	MaxHandoffFileBytes    = 25 << 20
	MaxHandoffMessageBytes = 100 << 20
)

var (
	ErrHandoffConflict  = errors.New("handoff state conflict")
	ErrHandoffForbidden = errors.New("handoff action forbidden")
	ErrHandoffFileLimit = errors.New("handoff file limit exceeded")
	ErrHandoffAction    = errors.New("invalid handoff action")
)

type Handoff struct {
	ID            int64      `json:"id"`
	ProjectSlug   string     `json:"project_slug,omitempty"`
	ProjectName   string     `json:"project_name,omitempty"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Scope         string     `json:"scope"`
	Source        string     `json:"source"`
	ClientID      string     `json:"client_id"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	DraftCount    int        `json:"draft_count"`
	ReadyCount    int        `json:"ready_count"`
	ProgressCount int        `json:"in_progress_count"`
	BlockedCount  int        `json:"blocked_count"`
	DoneCount     int        `json:"done_count"`
}

type HandoffMessage struct {
	ID                    int64         `json:"id"`
	HandoffID             int64         `json:"handoff_id"`
	Body                  string        `json:"body"`
	Target                string        `json:"target"`
	WorkState             string        `json:"work_state"`
	Source                string        `json:"source"`
	ClientID              string        `json:"client_id"`
	SeenAt                *time.Time    `json:"seen_at,omitempty"`
	SeenSource            string        `json:"seen_source,omitempty"`
	SeenClientID          string        `json:"seen_client_id,omitempty"`
	ClaimedAt             *time.Time    `json:"claimed_at,omitempty"`
	ClaimedSource         string        `json:"claimed_source,omitempty"`
	ClaimedClientID       string        `json:"claimed_client_id,omitempty"`
	StatusUpdatedAt       time.Time     `json:"status_updated_at"`
	StatusUpdatedSource   string        `json:"status_updated_source"`
	StatusUpdatedClientID string        `json:"status_updated_client_id"`
	CreatedAt             time.Time     `json:"created_at"`
	Files                 []HandoffFile `json:"files"`
}

type HandoffFile struct {
	ID           int64     `json:"id"`
	MessageID    int64     `json:"message_id"`
	HandoffID    int64     `json:"handoff_id,omitempty"`
	HandoffTitle string    `json:"handoff_title,omitempty"`
	Filename     string    `json:"filename"`
	MediaType    string    `json:"media_type"`
	SizeBytes    int64     `json:"size_bytes"`
	SHA256       string    `json:"sha256"`
	CreatedAt    time.Time `json:"created_at"`
	Data         []byte    `json:"-"`
}

type HandoffDetail struct {
	Handoff    Handoff          `json:"handoff"`
	Messages   []HandoffMessage `json:"messages"`
	NextBefore *int64           `json:"-"`
}

type HandoffFilter struct {
	Query          string
	ProjectSlug    string
	WorkState      string
	Target         string
	Archive        string
	CallerName     string
	CallerClientID string
	IncludeAll     bool
	Admin          bool
	Limit          int
	BeforeUpdated  *time.Time
	BeforeID       *int64
}

func validateHandoffAttribution(source, clientID string) error {
	if err := ValidateContextHeader("source", source, true); err != nil {
		return err
	}
	if strings.TrimSpace(clientID) == "" {
		return fmt.Errorf("client_id is required")
	}
	return nil
}

func scanHandoff(row pgx.Row) (Handoff, error) {
	var h Handoff
	err := row.Scan(&h.ID, &h.ProjectSlug, &h.ProjectName, &h.Title, &h.Description, &h.Scope, &h.Source, &h.ClientID, &h.CreatedAt, &h.UpdatedAt, &h.ArchivedAt)
	return h, err
}

const handoffColumns = `h.id,COALESCE(h.project_slug,''),COALESCE(p.name,''),h.title,h.description,h.scope,h.source,h.client_id,h.created_at,h.updated_at,h.archived_at`

func (db *DB) CreateHandoff(ctx context.Context, h Handoff, message HandoffMessage) (HandoffDetail, error) {
	if err := ValidateHandoff(h.Title, h.Description, h.Scope); err != nil {
		return HandoffDetail{}, err
	}
	if h.ProjectSlug != "" {
		if err := ValidateProjectSlug(h.ProjectSlug); err != nil {
			return HandoffDetail{}, err
		}
	}
	if err := validateHandoffAttribution(h.Source, h.ClientID); err != nil {
		return HandoffDetail{}, err
	}
	if message.WorkState != "draft" && message.WorkState != "ready" {
		return HandoffDetail{}, fmt.Errorf("new message work_state must be draft or ready")
	}
	if err := ValidateHandoffMessage(message.Body, message.Target, message.WorkState); err != nil {
		return HandoffDetail{}, err
	}
	if err := validateHandoffAttribution(message.Source, message.ClientID); err != nil {
		return HandoffDetail{}, err
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return HandoffDetail{}, err
	}
	defer tx.Rollback(ctx)
	var project any
	if h.ProjectSlug != "" {
		project = h.ProjectSlug
	}
	h, err = scanHandoff(tx.QueryRow(ctx, `INSERT INTO handoff(project_slug,title,description,scope,source,client_id)
VALUES($1,$2,$3,$4,$5,$6) RETURNING id,COALESCE(project_slug,''),'',title,description,scope,source,client_id,created_at,updated_at,archived_at`, project, h.Title, h.Description, h.Scope, h.Source, h.ClientID))
	if err != nil {
		return HandoffDetail{}, err
	}
	message.HandoffID = h.ID
	message, err = insertHandoffMessage(ctx, tx, message)
	if err != nil {
		return HandoffDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return HandoffDetail{}, err
	}
	if message.WorkState == "draft" {
		h.DraftCount = 1
	} else {
		h.ReadyCount = 1
	}
	return HandoffDetail{Handoff: h, Messages: []HandoffMessage{message}}, nil
}

func insertHandoffMessage(ctx context.Context, tx pgx.Tx, message HandoffMessage) (HandoffMessage, error) {
	message.Files = []HandoffFile{}
	err := tx.QueryRow(ctx, `INSERT INTO handoff_message(handoff_id,body,target,work_state,source,client_id,status_updated_source,status_updated_client_id)
VALUES($1,$2,$3,$4,$5,$6,$5,$6)
RETURNING id,handoff_id,body,target,work_state,source,client_id,seen_at,COALESCE(seen_source,''),COALESCE(seen_client_id,''),claimed_at,COALESCE(claimed_source,''),COALESCE(claimed_client_id,''),status_updated_at,status_updated_source,status_updated_client_id,created_at`,
		message.HandoffID, message.Body, message.Target, message.WorkState, message.Source, message.ClientID).
		Scan(&message.ID, &message.HandoffID, &message.Body, &message.Target, &message.WorkState, &message.Source, &message.ClientID, &message.SeenAt, &message.SeenSource, &message.SeenClientID, &message.ClaimedAt, &message.ClaimedSource, &message.ClaimedClientID, &message.StatusUpdatedAt, &message.StatusUpdatedSource, &message.StatusUpdatedClientID, &message.CreatedAt)
	return message, err
}

func (db *DB) AppendHandoffMessage(ctx context.Context, message HandoffMessage) (HandoffMessage, error) {
	if message.WorkState != "draft" && message.WorkState != "ready" {
		return HandoffMessage{}, fmt.Errorf("new message work_state must be draft or ready")
	}
	if err := ValidateHandoffMessage(message.Body, message.Target, message.WorkState); err != nil {
		return HandoffMessage{}, err
	}
	if err := validateHandoffAttribution(message.Source, message.ClientID); err != nil {
		return HandoffMessage{}, err
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return HandoffMessage{}, err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `SELECT id FROM handoff WHERE id=$1 FOR UPDATE`, message.HandoffID).Scan(&message.HandoffID); err != nil {
		return HandoffMessage{}, err
	}
	message, err = insertHandoffMessage(ctx, tx, message)
	if err != nil {
		return HandoffMessage{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE handoff SET updated_at=now(),archived_at=NULL WHERE id=$1`, message.HandoffID); err != nil {
		return HandoffMessage{}, err
	}
	return message, tx.Commit(ctx)
}

func (db *DB) ListHandoffs(ctx context.Context, filter HandoffFilter) ([]Handoff, error) {
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	archive := filter.Archive
	if archive == "" {
		archive = "active"
	}
	rows, err := db.Pool.Query(ctx, `SELECT `+handoffColumns+`,
count(*) FILTER (WHERE m.work_state='draft' AND ($7 OR m.client_id=$6)),
count(*) FILTER (WHERE m.work_state='ready'),
count(*) FILTER (WHERE m.work_state='in_progress'),
count(*) FILTER (WHERE m.work_state='blocked'),
count(*) FILTER (WHERE m.work_state='done')
FROM handoff h LEFT JOIN project p ON p.slug=h.project_slug JOIN handoff_message m ON m.handoff_id=h.id
WHERE ($1='all' OR ($1='active' AND h.archived_at IS NULL) OR ($1='archived' AND h.archived_at IS NOT NULL))
AND ($2='' OR h.project_slug=$2)
AND ($3='' OR EXISTS (SELECT 1 FROM handoff_message sm WHERE sm.handoff_id=h.id AND sm.work_state=$3 AND ($7 OR sm.work_state<>'draft' OR sm.client_id=$6)))
AND ($4='' OR h.tsv @@ websearch_to_tsquery('public.ledger_ts'::regconfig,$4) OR EXISTS (SELECT 1 FROM handoff_message qm WHERE qm.handoff_id=h.id AND qm.tsv @@ websearch_to_tsquery('public.ledger_ts'::regconfig,$4) AND ($7 OR qm.work_state<>'draft' OR qm.client_id=$6)))
AND EXISTS (SELECT 1 FROM handoff_message vm WHERE vm.handoff_id=h.id
  AND ($7 OR vm.work_state<>'draft' OR vm.client_id=$6)
AND (($5<>'')::boolean AND lower(vm.target)=lower($5) OR $5='' AND ($8 OR vm.target='' OR vm.claimed_client_id=$6 OR strpos(lower($9),lower(vm.target))>0 OR strpos(lower(vm.target),lower($9))>0)))
AND ($10::timestamptz IS NULL OR (h.updated_at,h.id)<($10,$11::bigint))
GROUP BY h.id,p.name ORDER BY h.updated_at DESC,h.id DESC LIMIT $12`,
		archive, filter.ProjectSlug, filter.WorkState, strings.TrimSpace(filter.Query), filter.Target, filter.CallerClientID, filter.Admin, filter.IncludeAll || filter.Admin, filter.CallerName, filter.BeforeUpdated, filter.BeforeID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Handoff{}
	for rows.Next() {
		var h Handoff
		if err := rows.Scan(&h.ID, &h.ProjectSlug, &h.ProjectName, &h.Title, &h.Description, &h.Scope, &h.Source, &h.ClientID, &h.CreatedAt, &h.UpdatedAt, &h.ArchivedAt, &h.DraftCount, &h.ReadyCount, &h.ProgressCount, &h.BlockedCount, &h.DoneCount); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (db *DB) GetHandoff(ctx context.Context, id int64, limit int, before *int64, viewerClientID string, admin bool) (HandoffDetail, error) {
	if limit < 1 {
		limit = 20
	}
	h, err := scanHandoff(db.Pool.QueryRow(ctx, `SELECT `+handoffColumns+` FROM handoff h LEFT JOIN project p ON p.slug=h.project_slug WHERE h.id=$1`, id))
	if err != nil {
		return HandoffDetail{}, err
	}
	err = db.Pool.QueryRow(ctx, `SELECT
count(*) FILTER (WHERE work_state='draft'),
count(*) FILTER (WHERE work_state='ready'),
count(*) FILTER (WHERE work_state='in_progress'),
count(*) FILTER (WHERE work_state='blocked'),
count(*) FILTER (WHERE work_state='done')
FROM handoff_message WHERE handoff_id=$1 AND ($2 OR work_state<>'draft' OR client_id=$3)`, id, admin, viewerClientID).
		Scan(&h.DraftCount, &h.ReadyCount, &h.ProgressCount, &h.BlockedCount, &h.DoneCount)
	if err != nil {
		return HandoffDetail{}, err
	}
	rows, err := db.Pool.Query(ctx, `SELECT id,handoff_id,body,target,work_state,source,client_id,seen_at,COALESCE(seen_source,''),COALESCE(seen_client_id,''),claimed_at,COALESCE(claimed_source,''),COALESCE(claimed_client_id,''),status_updated_at,status_updated_source,status_updated_client_id,created_at
FROM handoff_message WHERE handoff_id=$1 AND ($2 OR work_state<>'draft' OR client_id=$3) AND ($4::bigint IS NULL OR id<$4)
ORDER BY id DESC LIMIT $5`, id, admin, viewerClientID, before, limit+1)
	if err != nil {
		return HandoffDetail{}, err
	}
	defer rows.Close()
	messages := []HandoffMessage{}
	for rows.Next() {
		var m HandoffMessage
		if err := rows.Scan(&m.ID, &m.HandoffID, &m.Body, &m.Target, &m.WorkState, &m.Source, &m.ClientID, &m.SeenAt, &m.SeenSource, &m.SeenClientID, &m.ClaimedAt, &m.ClaimedSource, &m.ClaimedClientID, &m.StatusUpdatedAt, &m.StatusUpdatedSource, &m.StatusUpdatedClientID, &m.CreatedAt); err != nil {
			return HandoffDetail{}, err
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return HandoffDetail{}, err
	}
	var next *int64
	if len(messages) > limit {
		messages = messages[:limit]
		value := messages[len(messages)-1].ID
		next = &value
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	if err := addFilesToMessages(ctx, db.Pool, messages); err != nil {
		return HandoffDetail{}, err
	}
	return HandoffDetail{Handoff: h, Messages: messages, NextBefore: next}, nil
}

type handoffRowsQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func addFilesToMessages(ctx context.Context, q handoffRowsQuerier, messages []HandoffMessage) error {
	ids := make([]int64, len(messages))
	byID := make(map[int64]int, len(messages))
	for i := range messages {
		ids[i] = messages[i].ID
		byID[messages[i].ID] = i
		messages[i].Files = []HandoffFile{}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.Query(ctx, `SELECT id,message_id,filename,media_type,size_bytes,encode(sha256,'hex'),created_at FROM handoff_file WHERE message_id=ANY($1) ORDER BY created_at,id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var f HandoffFile
		if err := rows.Scan(&f.ID, &f.MessageID, &f.Filename, &f.MediaType, &f.SizeBytes, &f.SHA256, &f.CreatedAt); err != nil {
			return err
		}
		i := byID[f.MessageID]
		f.HandoffID = messages[i].HandoffID
		messages[i].Files = append(messages[i].Files, f)
	}
	return rows.Err()
}

func (db *DB) UpdateHandoff(ctx context.Context, h Handoff) (Handoff, error) {
	if err := ValidateHandoff(h.Title, h.Description, h.Scope); err != nil {
		return Handoff{}, err
	}
	var project any
	if h.ProjectSlug != "" {
		if err := ValidateProjectSlug(h.ProjectSlug); err != nil {
			return Handoff{}, err
		}
		project = h.ProjectSlug
	}
	return scanHandoff(db.Pool.QueryRow(ctx, `UPDATE handoff h SET project_slug=$2,title=$3,description=$4,scope=$5,updated_at=now()
FROM (SELECT 1) x LEFT JOIN project p ON p.slug=$2 WHERE h.id=$1
RETURNING h.id,COALESCE(h.project_slug,''),COALESCE(p.name,''),h.title,h.description,h.scope,h.source,h.client_id,h.created_at,h.updated_at,h.archived_at`, h.ID, project, h.Title, h.Description, h.Scope))
}

type messageState struct {
	HandoffID       int64
	WorkState       string
	AuthorClientID  string
	Target          string
	SeenAt          *time.Time
	SeenSource      string
	SeenClientID    string
	ClaimedAt       *time.Time
	ClaimedSource   string
	ClaimedClientID string
}

func (db *DB) UpdateHandoffMessage(ctx context.Context, id int64, action, target, source, clientID string, admin bool) (HandoffMessage, error) {
	if err := validateHandoffAttribution(source, clientID); err != nil {
		return HandoffMessage{}, err
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return HandoffMessage{}, err
	}
	defer tx.Rollback(ctx)
	var state messageState
	err = tx.QueryRow(ctx, `SELECT h.id FROM handoff h JOIN handoff_message m ON m.handoff_id=h.id WHERE m.id=$1 FOR UPDATE OF h`, id).Scan(&state.HandoffID)
	if err != nil {
		return HandoffMessage{}, err
	}
	err = tx.QueryRow(ctx, `SELECT work_state,client_id,target,seen_at,COALESCE(seen_source,''),COALESCE(seen_client_id,''),claimed_at,COALESCE(claimed_source,''),COALESCE(claimed_client_id,'') FROM handoff_message WHERE id=$1 FOR UPDATE`, id).
		Scan(&state.WorkState, &state.AuthorClientID, &state.Target, &state.SeenAt, &state.SeenSource, &state.SeenClientID, &state.ClaimedAt, &state.ClaimedSource, &state.ClaimedClientID)
	if err != nil {
		return HandoffMessage{}, err
	}
	now := time.Now()
	newState := state.WorkState
	newTarget := state.Target
	seenAt, seenSource, seenClient := state.SeenAt, state.SeenSource, state.SeenClientID
	claimedAt, claimedSource, claimedClient := state.ClaimedAt, state.ClaimedSource, state.ClaimedClientID
	owner := admin || state.AuthorClientID == clientID
	claimant := admin || state.ClaimedClientID == clientID
	switch action {
	case "acknowledge":
		if state.WorkState == "draft" {
			return HandoffMessage{}, ErrHandoffConflict
		}
		if seenAt == nil {
			seenAt, seenSource, seenClient = &now, source, clientID
		}
	case "publish":
		if state.WorkState != "draft" || !owner {
			return HandoffMessage{}, ErrHandoffForbidden
		}
		newState = "ready"
	case "claim":
		if state.WorkState != "ready" || state.ClaimedAt != nil {
			return HandoffMessage{}, ErrHandoffConflict
		}
		newState = "in_progress"
		claimedAt, claimedSource, claimedClient = &now, source, clientID
		if seenAt == nil {
			seenAt, seenSource, seenClient = &now, source, clientID
		}
	case "block":
		if state.WorkState != "in_progress" || !claimant {
			return HandoffMessage{}, ErrHandoffForbidden
		}
		newState = "blocked"
	case "complete":
		if (state.WorkState != "in_progress" && state.WorkState != "blocked") || !claimant {
			return HandoffMessage{}, ErrHandoffForbidden
		}
		newState = "done"
	case "release":
		if (state.WorkState != "in_progress" && state.WorkState != "blocked") || !claimant {
			return HandoffMessage{}, ErrHandoffForbidden
		}
		newState, claimedAt, claimedSource, claimedClient = "ready", nil, "", ""
	case "reopen":
		if state.WorkState != "done" || !admin {
			return HandoffMessage{}, ErrHandoffForbidden
		}
		newState, claimedAt, claimedSource, claimedClient = "ready", nil, "", ""
	case "retarget":
		if (state.WorkState != "draft" && state.WorkState != "ready") || state.ClaimedAt != nil || !owner {
			return HandoffMessage{}, ErrHandoffForbidden
		}
		if utf8.RuneCountInString(target) > 100 || strings.ContainsAny(target, "\r\n") {
			return HandoffMessage{}, fmt.Errorf("target must be at most 100 characters on one line")
		}
		newTarget = target
	default:
		return HandoffMessage{}, ErrHandoffAction
	}
	var message HandoffMessage
	err = tx.QueryRow(ctx, `UPDATE handoff_message SET target=$2,work_state=$3,seen_at=$4,seen_source=$5,seen_client_id=$6,claimed_at=$7,claimed_source=$8,claimed_client_id=$9,status_updated_at=now(),status_updated_source=$10,status_updated_client_id=$11
WHERE id=$1 RETURNING id,handoff_id,body,target,work_state,source,client_id,seen_at,COALESCE(seen_source,''),COALESCE(seen_client_id,''),claimed_at,COALESCE(claimed_source,''),COALESCE(claimed_client_id,''),status_updated_at,status_updated_source,status_updated_client_id,created_at`,
		id, newTarget, newState, seenAt, nullableString(seenSource), nullableString(seenClient), claimedAt, nullableString(claimedSource), nullableString(claimedClient), source, clientID).
		Scan(&message.ID, &message.HandoffID, &message.Body, &message.Target, &message.WorkState, &message.Source, &message.ClientID, &message.SeenAt, &message.SeenSource, &message.SeenClientID, &message.ClaimedAt, &message.ClaimedSource, &message.ClaimedClientID, &message.StatusUpdatedAt, &message.StatusUpdatedSource, &message.StatusUpdatedClientID, &message.CreatedAt)
	if err != nil {
		return HandoffMessage{}, err
	}
	messages := []HandoffMessage{message}
	if err := addFilesToMessages(ctx, tx, messages); err != nil {
		return HandoffMessage{}, err
	}
	message = messages[0]
	if _, err := tx.Exec(ctx, `UPDATE handoff SET updated_at=now(),archived_at=CASE
WHEN EXISTS (SELECT 1 FROM handoff_message WHERE handoff_id=$1 AND work_state<>'done') THEN NULL
ELSE COALESCE(archived_at,now()) END WHERE id=$1`, state.HandoffID); err != nil {
		return HandoffMessage{}, err
	}
	return message, tx.Commit(ctx)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func normalizeMediaType(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "application/octet-stream", nil
	}
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil {
		return "", fmt.Errorf("invalid media type")
	}
	normalized := mime.FormatMediaType(mediaType, params)
	if utf8.RuneCountInString(normalized) > 255 {
		return "", fmt.Errorf("media type must be at most 255 characters")
	}
	return normalized, nil
}

func validateHandoffFile(filename string, data []byte) error {
	if length := utf8.RuneCountInString(filename); length == 0 || length > 255 || strings.ContainsAny(filename, "/\\\x00") {
		return fmt.Errorf("filename must be 1 to 255 characters without path separators")
	}
	if len(data) == 0 || len(data) > MaxHandoffFileBytes {
		return fmt.Errorf("file must be 1 byte to %d bytes", MaxHandoffFileBytes)
	}
	return nil
}

func (db *DB) AddHandoffFile(ctx context.Context, messageID int64, filename, mediaType string, data []byte, clientID string, admin bool) (HandoffFile, error) {
	if err := validateHandoffFile(filename, data); err != nil {
		return HandoffFile{}, err
	}
	mediaType, err := normalizeMediaType(mediaType)
	if err != nil {
		return HandoffFile{}, err
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return HandoffFile{}, err
	}
	defer tx.Rollback(ctx)
	var handoffID int64
	var state, author string
	if err := tx.QueryRow(ctx, `SELECT h.id FROM handoff h JOIN handoff_message m ON m.handoff_id=h.id WHERE m.id=$1 FOR UPDATE OF h`, messageID).Scan(&handoffID); err != nil {
		return HandoffFile{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT work_state,client_id FROM handoff_message WHERE id=$1 FOR UPDATE`, messageID).Scan(&state, &author); err != nil {
		return HandoffFile{}, err
	}
	if state != "draft" {
		return HandoffFile{}, ErrHandoffConflict
	}
	if !admin && author != clientID {
		return HandoffFile{}, ErrHandoffForbidden
	}
	var count int
	var total int64
	if err := tx.QueryRow(ctx, `SELECT count(*),COALESCE(sum(size_bytes),0) FROM handoff_file WHERE message_id=$1`, messageID).Scan(&count, &total); err != nil {
		return HandoffFile{}, err
	}
	if count >= MaxHandoffFiles || total+int64(len(data)) > MaxHandoffMessageBytes {
		return HandoffFile{}, ErrHandoffFileLimit
	}
	sum := sha256.Sum256(data)
	var file HandoffFile
	err = tx.QueryRow(ctx, `INSERT INTO handoff_file(message_id,filename,media_type,size_bytes,sha256,data) VALUES($1,$2,$3,$4,$5,$6)
RETURNING id,message_id,filename,media_type,size_bytes,encode(sha256,'hex'),created_at`, messageID, filename, mediaType, len(data), sum[:], data).
		Scan(&file.ID, &file.MessageID, &file.Filename, &file.MediaType, &file.SizeBytes, &file.SHA256, &file.CreatedAt)
	if err != nil {
		return HandoffFile{}, err
	}
	file.HandoffID = handoffID
	if _, err := tx.Exec(ctx, `UPDATE handoff SET updated_at=now() WHERE id=$1`, handoffID); err != nil {
		return HandoffFile{}, err
	}
	return file, tx.Commit(ctx)
}

func (db *DB) GetHandoffFile(ctx context.Context, id int64, viewerClientID string, admin bool) (HandoffFile, error) {
	var file HandoffFile
	err := db.Pool.QueryRow(ctx, `SELECT f.id,f.message_id,m.handoff_id,h.title,f.filename,f.media_type,f.size_bytes,encode(f.sha256,'hex'),f.created_at,f.data
FROM handoff_file f JOIN handoff_message m ON m.id=f.message_id JOIN handoff h ON h.id=m.handoff_id
WHERE f.id=$1 AND ($2 OR m.work_state<>'draft' OR m.client_id=$3)`, id, admin, viewerClientID).
		Scan(&file.ID, &file.MessageID, &file.HandoffID, &file.HandoffTitle, &file.Filename, &file.MediaType, &file.SizeBytes, &file.SHA256, &file.CreatedAt, &file.Data)
	return file, err
}

func (db *DB) DeleteHandoffFile(ctx context.Context, id int64) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var handoffID int64
	if err := tx.QueryRow(ctx, `SELECT h.id FROM handoff h JOIN handoff_message m ON m.handoff_id=h.id JOIN handoff_file f ON f.message_id=m.id WHERE f.id=$1 FOR UPDATE OF h`, id).Scan(&handoffID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM handoff_file WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE handoff SET updated_at=now() WHERE id=$1`, handoffID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (db *DB) ListProjectHandoffFiles(ctx context.Context, slug string) ([]HandoffFile, error) {
	rows, err := db.Pool.Query(ctx, `SELECT f.id,f.message_id,h.id,h.title,f.filename,f.media_type,f.size_bytes,encode(f.sha256,'hex'),f.created_at
FROM handoff_file f JOIN handoff_message m ON m.id=f.message_id JOIN handoff h ON h.id=m.handoff_id
WHERE h.project_slug=$1 ORDER BY f.created_at DESC,f.id DESC`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HandoffFile{}
	for rows.Next() {
		var file HandoffFile
		if err := rows.Scan(&file.ID, &file.MessageID, &file.HandoffID, &file.HandoffTitle, &file.Filename, &file.MediaType, &file.SizeBytes, &file.SHA256, &file.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, file)
	}
	return out, rows.Err()
}
