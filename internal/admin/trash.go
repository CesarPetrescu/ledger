package admin

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
)

// withAction adds the undoable action's ID to a response.
func withAction(item map[string]any, actionID int64) map[string]any {
	item["action_id"] = strconv.FormatInt(actionID, 10)
	return item
}

func pathID(w http.ResponseWriter, r *http.Request, what string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid "+what+" id")
		return 0, false
	}
	return id, true
}

func (s *Server) trashEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathEntryID(w, r)
	if !ok {
		return
	}
	trashID, actionID, err := s.db.TrashEntry(r.Context(), id)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, withAction(map[string]any{"trash_id": strconv.FormatInt(trashID, 10)}, actionID))
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, "entry not found")
	default:
		s.internalError(w, r, err)
	}
}

func (s *Server) projectDeletionPreview(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := store.ValidateProjectSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	preview, err := s.db.ProjectDeletionPreview(r.Context(), slug)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, preview)
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, "project not found")
	default:
		s.internalError(w, r, err)
	}
}

// trashProject requires the request to repeat the slug, so a stray or
// replayed DELETE cannot remove a project without the owner's confirmation.
func (s *Server) trashProject(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := store.ValidateProjectSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		Confirm string `json:"confirm"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	if input.Confirm != slug {
		writeError(w, http.StatusBadRequest, "confirm must repeat the project slug")
		return
	}
	trashID, actionID, err := s.db.TrashProject(r.Context(), slug)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, withAction(map[string]any{"trash_id": strconv.FormatInt(trashID, 10)}, actionID))
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, "project not found")
	default:
		s.internalError(w, r, err)
	}
}

func (s *Server) listTrash(w http.ResponseWriter, r *http.Request) {
	items, err := s.db.ListTrash(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		rows = append(rows, map[string]any{"id": strconv.FormatInt(item.ID, 10), "kind": item.Kind, "label": item.Label, "project_slug": item.ProjectSlug,
			"entry_count": item.EntryCount, "deleted_at": item.DeletedAt, "purge_at": item.PurgeAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (s *Server) restoreTrash(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "trash")
	if !ok {
		return
	}
	err := s.db.RestoreTrash(r.Context(), id)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"restored": true})
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, "item not in trash")
	case errors.Is(err, store.ErrProjectGone), errors.Is(err, store.ErrProjectTaken):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.internalError(w, r, err)
	}
}

func (s *Server) deleteTrash(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "trash")
	if !ok {
		return
	}
	switch err := s.db.DeleteTrash(r.Context(), id); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, "item not in trash")
	default:
		s.internalError(w, r, err)
	}
}

func (s *Server) listActions(w http.ResponseWriter, r *http.Request) {
	actions, err := s.db.ListActions(r.Context(), 50)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	rows := make([]map[string]any, 0, len(actions))
	for _, a := range actions {
		row := map[string]any{"id": strconv.FormatInt(a.ID, 10), "kind": a.Kind, "label": a.Label, "project_slug": a.ProjectSlug, "created_at": a.CreatedAt, "undoable": a.Undoable}
		if a.UndoneAt != nil {
			row["undone_at"] = a.UndoneAt
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": rows})
}

func (s *Server) undoAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "action")
	if !ok {
		return
	}
	err := s.db.UndoAction(r.Context(), id)
	var conflict *store.UndoConflict
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"undone": true})
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, "action not found")
	case errors.Is(err, store.ErrNotUndoable), errors.Is(err, store.ErrProjectGone), errors.Is(err, store.ErrProjectTaken), errors.As(err, &conflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.internalError(w, r, err)
	}
}

// RunTrashPurge permanently removes trash past retention, hourly.
func (s *Server) RunTrashPurge(ctx context.Context) error {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if n, err := s.db.PurgeTrash(ctx); err != nil && ctx.Err() == nil {
			log.Printf("admin: purge trash: %v", err)
		} else if n > 0 {
			log.Printf("admin: purged %d trash items past %s", n, store.TrashRetention)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
