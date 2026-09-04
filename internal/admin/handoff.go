package admin

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cesarpetrescu/ledger/internal/store"
)

func handoffID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

func handoffFileResponse(file store.HandoffFile) map[string]any {
	return map[string]any{
		"id": strconv.FormatInt(file.ID, 10), "message_id": strconv.FormatInt(file.MessageID, 10),
		"handoff_id": strconv.FormatInt(file.HandoffID, 10), "handoff_title": file.HandoffTitle,
		"filename": file.Filename, "media_type": file.MediaType, "size_bytes": file.SizeBytes,
		"sha256": file.SHA256, "created_at": file.CreatedAt,
	}
}

func handoffMessageResponse(message store.HandoffMessage) map[string]any {
	files := make([]map[string]any, len(message.Files))
	for i, file := range message.Files {
		files[i] = handoffFileResponse(file)
	}
	return map[string]any{
		"id": strconv.FormatInt(message.ID, 10), "handoff_id": strconv.FormatInt(message.HandoffID, 10),
		"body": message.Body, "target": message.Target, "delivery_state": map[bool]string{true: "seen", false: "unseen"}[message.SeenAt != nil],
		"work_state": message.WorkState, "source": message.Source, "client_id": message.ClientID,
		"seen_at": message.SeenAt, "seen_source": message.SeenSource, "seen_client_id": message.SeenClientID,
		"claimed_at": message.ClaimedAt, "claimed_source": message.ClaimedSource, "claimed_client_id": message.ClaimedClientID,
		"status_updated_at": message.StatusUpdatedAt, "status_updated_source": message.StatusUpdatedSource,
		"status_updated_client_id": message.StatusUpdatedClientID, "created_at": message.CreatedAt, "files": files,
	}
}

func handoffResponse(handoff store.Handoff) map[string]any {
	return map[string]any{
		"id": strconv.FormatInt(handoff.ID, 10), "project_slug": handoff.ProjectSlug, "project_name": handoff.ProjectName,
		"title": handoff.Title, "description": handoff.Description, "scope": handoff.Scope,
		"source": handoff.Source, "client_id": handoff.ClientID, "created_at": handoff.CreatedAt,
		"updated_at": handoff.UpdatedAt, "archived_at": handoff.ArchivedAt,
		"draft_count": handoff.DraftCount, "ready_count": handoff.ReadyCount,
		"in_progress_count": handoff.ProgressCount, "blocked_count": handoff.BlockedCount, "done_count": handoff.DoneCount,
	}
}

func handoffDetailResponse(detail store.HandoffDetail) map[string]any {
	messages := make([]map[string]any, len(detail.Messages))
	for i, message := range detail.Messages {
		messages[i] = handoffMessageResponse(message)
	}
	response := map[string]any{"handoff": handoffResponse(detail.Handoff), "messages": messages}
	if detail.NextBefore != nil {
		response["next_before"] = strconv.FormatInt(*detail.NextBefore, 10)
	}
	return response
}

func parseHandoffCursor(raw string) (*time.Time, *int64, error) {
	if raw == "" {
		return nil, nil, nil
	}
	separator := strings.LastIndexByte(raw, '|')
	if separator < 1 {
		return nil, nil, fmt.Errorf("invalid cursor")
	}
	when, err := time.Parse(time.RFC3339Nano, raw[:separator])
	if err != nil {
		return nil, nil, fmt.Errorf("invalid cursor")
	}
	id, err := strconv.ParseInt(raw[separator+1:], 10, 64)
	if err != nil || id < 1 {
		return nil, nil, fmt.Errorf("invalid cursor")
	}
	return &when, &id, nil
}

func (s *Server) listHandoffs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := store.HandoffFilter{Query: strings.TrimSpace(query.Get("q")), ProjectSlug: query.Get("project"), WorkState: query.Get("status"), Target: query.Get("target"), Archive: query.Get("archive"), IncludeAll: true, Admin: true, Limit: 20}
	if utf8.RuneCountInString(filter.Query) > maxSearchRunes {
		writeError(w, http.StatusBadRequest, "q must be at most 1000 characters")
		return
	}
	if err := store.ValidateContextHeader("target", filter.Target, false); err != nil || utf8.RuneCountInString(filter.Target) > 100 {
		writeError(w, http.StatusBadRequest, "target must be at most 100 characters on one line")
		return
	}
	if filter.ProjectSlug != "" {
		if err := store.ValidateProjectSlug(filter.ProjectSlug); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if filter.WorkState != "" && !slices.Contains(store.HandoffWorkStates, filter.WorkState) {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	if filter.Archive == "" {
		filter.Archive = "active"
	}
	if filter.Archive != "active" && filter.Archive != "archived" && filter.Archive != "all" {
		writeError(w, http.StatusBadRequest, "archive must be active, archived, or all")
		return
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		filter.Limit = limit
	}
	when, id, err := parseHandoffCursor(query.Get("before"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter.BeforeUpdated, filter.BeforeID = when, id
	limit := filter.Limit
	filter.Limit++
	handoffs, err := s.db.ListHandoffs(r.Context(), filter)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	response := map[string]any{}
	if len(handoffs) > limit {
		handoffs = handoffs[:limit]
		last := handoffs[len(handoffs)-1]
		response["next_before"] = last.UpdatedAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(last.ID, 10)
	}
	items := make([]map[string]any, len(handoffs))
	for i, handoff := range handoffs {
		items[i] = handoffResponse(handoff)
	}
	response["handoffs"] = items
	writeJSON(w, http.StatusOK, response)
}

type handoffCreateInput struct {
	ProjectSlug string `json:"project_slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Body        string `json:"body"`
	Target      string `json:"target"`
	Draft       bool   `json:"draft"`
}

func (s *Server) createHandoff(w http.ResponseWriter, r *http.Request) {
	var input handoffCreateInput
	if err := decodeJSON(w, r, &input, maxHandoffJSON); err != nil {
		writeDecodeError(w, err)
		return
	}
	clientID := clientIdentifier(sessionFrom(r))
	state := "ready"
	if input.Draft {
		state = "draft"
	}
	detail, err := s.db.CreateHandoff(r.Context(), store.Handoff{ProjectSlug: input.ProjectSlug, Title: input.Title, Description: input.Description, Scope: input.Scope, Source: writeSource, ClientID: clientID}, store.HandoffMessage{Body: input.Body, Target: input.Target, WorkState: state, Source: writeSource, ClientID: clientID})
	if err != nil {
		s.handoffError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, handoffDetailResponse(detail))
}

func (s *Server) getHandoff(w http.ResponseWriter, r *http.Request) {
	id, ok := handoffID(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("messages"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "messages must be between 1 and 100")
			return
		}
		limit = parsed
	}
	var before *int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "before must be a positive message ID")
			return
		}
		before = &parsed
	}
	detail, err := s.db.GetHandoff(r.Context(), id, limit, before, "", true)
	if err != nil {
		s.handoffError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, handoffDetailResponse(detail))
}

func (s *Server) putHandoff(w http.ResponseWriter, r *http.Request) {
	id, ok := handoffID(w, r)
	if !ok {
		return
	}
	var input struct {
		ProjectSlug string `json:"project_slug"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Scope       string `json:"scope"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	handoff, err := s.db.UpdateHandoff(r.Context(), store.Handoff{ID: id, ProjectSlug: input.ProjectSlug, Title: input.Title, Description: input.Description, Scope: input.Scope})
	if err != nil {
		s.handoffError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, handoffResponse(handoff))
}

func (s *Server) appendHandoffMessage(w http.ResponseWriter, r *http.Request) {
	handoffID, ok := handoffID(w, r)
	if !ok {
		return
	}
	var input struct {
		Body   string `json:"body"`
		Target string `json:"target"`
		Draft  bool   `json:"draft"`
	}
	if err := decodeJSON(w, r, &input, maxHandoffJSON); err != nil {
		writeDecodeError(w, err)
		return
	}
	state := "ready"
	if input.Draft {
		state = "draft"
	}
	clientID := clientIdentifier(sessionFrom(r))
	message, err := s.db.AppendHandoffMessage(r.Context(), store.HandoffMessage{HandoffID: handoffID, Body: input.Body, Target: input.Target, WorkState: state, Source: writeSource, ClientID: clientID})
	if err != nil {
		s.handoffError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, handoffMessageResponse(message))
}

func (s *Server) updateHandoffMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := handoffID(w, r)
	if !ok {
		return
	}
	var input struct {
		Action string `json:"action"`
		Target string `json:"target"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	message, err := s.db.UpdateHandoffMessage(r.Context(), id, input.Action, input.Target, writeSource, clientIdentifier(sessionFrom(r)), true)
	if err != nil {
		s.handoffError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, handoffMessageResponse(message))
}

func (s *Server) uploadHandoffFile(w http.ResponseWriter, r *http.Request) {
	messageID, ok := handoffID(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, store.MaxHandoffFileBytes+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected one multipart file")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	data, err := io.ReadAll(io.LimitReader(part, store.MaxHandoffFileBytes+1))
	_ = part.Close()
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read file")
		return
	}
	if len(data) > store.MaxHandoffFileBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds 25 MB")
		return
	}
	if extra, err := reader.NextPart(); err != io.EOF || extra != nil {
		writeError(w, http.StatusBadRequest, "upload exactly one file")
		return
	}
	mediaType := part.Header.Get("Content-Type")
	file, err := s.db.AddHandoffFile(r.Context(), messageID, part.FileName(), mediaType, data, clientIdentifier(sessionFrom(r)), true)
	if err != nil {
		s.handoffError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, handoffFileResponse(file))
}

func (s *Server) downloadHandoffFile(w http.ResponseWriter, r *http.Request) {
	id, ok := handoffID(w, r)
	if !ok {
		return
	}
	file, err := s.db.GetHandoffFile(r.Context(), id, "", true)
	if err != nil {
		s.handoffError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", file.MediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Filename}))
	w.Header().Set("Content-Length", strconv.FormatInt(file.SizeBytes, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Data)
}

func (s *Server) deleteHandoffFile(w http.ResponseWriter, r *http.Request) {
	id, ok := handoffID(w, r)
	if !ok {
		return
	}
	if err := s.db.DeleteHandoffFile(r.Context(), id); err != nil {
		s.handoffError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listProjectFiles(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := store.ValidateProjectSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	files, err := s.db.ListProjectHandoffFiles(r.Context(), slug)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	items := make([]map[string]any, len(files))
	for i, file := range files {
		items[i] = handoffFileResponse(file)
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": items})
}

func (s *Server) exportHandoff(w http.ResponseWriter, r *http.Request) {
	id, ok := handoffID(w, r)
	if !ok {
		return
	}
	detail, err := s.db.GetHandoff(r.Context(), id, 10000, nil, "", true)
	if err != nil {
		s.handoffError(w, r, err)
		return
	}
	for detail.NextBefore != nil {
		page, err := s.db.GetHandoff(r.Context(), id, 10000, detail.NextBefore, "", true)
		if err != nil {
			s.handoffError(w, r, err)
			return
		}
		detail.Messages = append(page.Messages, detail.Messages...)
		detail.NextBefore = page.NextBefore
	}
	var body strings.Builder
	fmt.Fprintf(&body, "# %s\n\n%s\n\nScope: %s\n", detail.Handoff.Title, detail.Handoff.Description, detail.Handoff.Scope)
	if detail.Handoff.ProjectSlug != "" {
		fmt.Fprintf(&body, "Project: %s\n", detail.Handoff.ProjectSlug)
	}
	for _, message := range detail.Messages {
		fmt.Fprintf(&body, "\n## %s · %s · %s\n\n%s\n", message.CreatedAt.UTC().Format(time.RFC3339), message.Source, message.WorkState, message.Body)
		if message.Target != "" {
			fmt.Fprintf(&body, "\nTarget: %s\n", message.Target)
		}
		for _, file := range message.Files {
			fmt.Fprintf(&body, "- Attachment #%d: %s (%s, %d bytes)\n", file.ID, file.Filename, file.MediaType, file.SizeBytes)
		}
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "handoff-" + strconv.FormatInt(id, 10) + ".md"}))
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body.String())
}

func (s *Server) handoffError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, "handoff item not found")
	case store.IsForeignKeyViolation(err):
		writeError(w, http.StatusBadRequest, "project not found")
	case store.IsCheckViolation(err):
		writeError(w, http.StatusBadRequest, "handoff violates its constraints")
	case errors.Is(err, store.ErrHandoffConflict), errors.Is(err, store.ErrHandoffFileLimit):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrHandoffForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, store.ErrHandoffAction):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		if strings.Contains(err.Error(), "must be") || strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "invalid media") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.internalError(w, r, err)
	}
}
