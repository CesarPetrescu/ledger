package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/jackc/pgx/v5/pgconn"
)

// /api/v1 is a plain JSON API for servers that dispatch research, such as Adastrion Core, authenticated
// with owner-created API keys rather than OAuth. It shares the research store with /mcp/dispatch, so the
// two interfaces behave the same.

const maxClaimWait = 25 * time.Second

type apiKeyContext struct{}

func apiError(w http.ResponseWriter, status int, code, message string) {
	writeAPI(w, status, map[string]string{"error": code, "message": message})
}

func writeAPI(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// decodeAPI reads an optional JSON object body into value.
func decodeAPI(r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func apiHandler(db *store.DB, publicURL string) http.Handler {
	mux := http.NewServeMux()
	key := func(r *http.Request) store.APIKey { return r.Context().Value(apiKeyContext{}).(store.APIKey) }
	client := func(r *http.Request) string { return store.APIKeyClientID(key(r).ID) }
	taskID := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			apiError(w, http.StatusBadRequest, "invalid_request", "task id must be a positive integer")
			return 0, false
		}
		return id, true
	}

	mux.HandleFunc("GET /api/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		writeAPI(w, http.StatusOK, map[string]any{"key": key(r).Name, "scopes": key(r).Scopes})
	})

	// claim leases the oldest ready task, waiting up to wait_seconds (at most 25) for one to appear.
	mux.HandleFunc("POST /api/v1/research/claim", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			LeaseSeconds int `json:"lease_seconds"`
			WaitSeconds  int `json:"wait_seconds"`
		}
		if err := decodeAPI(r, &input); err != nil {
			apiError(w, http.StatusBadRequest, "invalid_request", "body must be {\"lease_seconds\": n, \"wait_seconds\": n}")
			return
		}
		wait := time.Duration(input.WaitSeconds) * time.Second
		if wait < 0 || wait > maxClaimWait {
			apiError(w, http.StatusBadRequest, "invalid_request", "wait_seconds must be between 0 and 25")
			return
		}
		if input.LeaseSeconds != 0 && (input.LeaseSeconds < 30 || input.LeaseSeconds > 3600) {
			apiError(w, http.StatusBadRequest, "invalid_request", "lease_seconds must be between 30 and 3600")
			return
		}
		deadline := time.Now().Add(wait)
		for {
			claim, err := db.ClaimResearchTaskWithKey(r.Context(), key(r).ID, input.LeaseSeconds, key(r).Name)
			if errors.Is(err, store.ErrAPIKeyRevoked) {
				apiUnauthorized(w)
				return
			}
			if err != nil {
				if r.Context().Err() == nil {
					apiError(w, http.StatusInternalServerError, "server_error", "could not claim a task")
				}
				return
			}
			if claim != nil {
				writeAPI(w, http.StatusOK, claimOutput(claim, publicURL))
				return
			}
			if !time.Now().Before(deadline) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// ponytail: re-checks the queue every second while waiting; LISTEN/NOTIFY if many dispatchers wait at once.
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
		}
	})

	mux.HandleFunc("GET /api/v1/research/tasks", func(w http.ResponseWriter, r *http.Request) {
		limit := 0
		if raw := r.URL.Query().Get("limit"); raw != "" {
			var err error
			if limit, err = strconv.Atoi(raw); err != nil {
				apiError(w, http.StatusBadRequest, "invalid_request", "limit must be a number")
				return
			}
		}
		tasks, err := db.ListResearchTasks(r.Context(), r.URL.Query().Get("status"), limit)
		if err != nil {
			apiError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeAPI(w, http.StatusOK, map[string]any{"tasks": tasks})
	})

	mux.HandleFunc("GET /api/v1/research/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := taskID(w, r)
		if !ok {
			return
		}
		task, err := db.ResearchTask(r.Context(), id)
		if store.IsNotFound(err) {
			apiError(w, http.StatusNotFound, "not_found", "no research task with that id")
			return
		}
		if err != nil {
			apiError(w, http.StatusInternalServerError, "server_error", "could not read the task")
			return
		}
		writeAPI(w, http.StatusOK, taskOutput(task))
	})

	// A dispatcher may request follow-up work only from an accepted task. Run tokens cannot use this API.
	mux.HandleFunc("POST /api/v1/research/tasks/{id}/continue", func(w http.ResponseWriter, r *http.Request) {
		id, ok := taskID(w, r)
		if !ok {
			return
		}
		var input struct {
			Title      string   `json:"title"`
			Objective  string   `json:"objective"`
			Acceptance []string `json:"acceptance"`
		}
		if err := decodeAPI(r, &input); err != nil {
			apiError(w, 400, "invalid_request", "send title, objective and acceptance")
			return
		}
		parent, err := db.ResearchTask(r.Context(), id)
		if store.IsNotFound(err) {
			apiError(w, 404, "not_found", "research task not found")
			return
		}
		if err != nil {
			apiError(w, 500, "server_error", "could not read task")
			return
		}
		if parent.State != "done" {
			apiError(w, 409, "not_accepted", "accept the previous result before continuing")
			return
		}
		task, err := db.CreateResearchTask(r.Context(), store.NewResearchTask{ContinueFromTaskID: id, ProjectSlug: parent.ProjectSlug, Title: input.Title, Source: key(r).Name, ClientID: client(r), MaxAttempts: parent.MaxAttempts, Spec: store.ResearchSpec{Objective: input.Objective, Acceptance: input.Acceptance, Deliverable: parent.Spec.Deliverable, Budget: parent.Spec.Budget}})
		if errors.Is(err, store.ErrNotAccepted) {
			apiError(w, http.StatusConflict, "not_accepted", "accept the previous result before continuing")
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) || store.IsNotFound(err) || r.Context().Err() != nil {
			apiError(w, http.StatusInternalServerError, "server_error", "could not create the continuation")
			return
		}
		if err != nil {
			apiError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeAPI(w, http.StatusCreated, taskOutput(task))
	})

	type runInput struct {
		Attempt int    `json:"attempt"`
		Error   string `json:"error"`
	}
	mux.HandleFunc("POST /api/v1/research/tasks/{id}/renew", func(w http.ResponseWriter, r *http.Request) {
		id, ok := taskID(w, r)
		var input runInput
		if !ok {
			return
		}
		if err := decodeAPI(r, &input); err != nil || input.Attempt < 1 {
			apiError(w, http.StatusBadRequest, "invalid_request", "body must be {\"attempt\": n} with the attempt from the claim")
			return
		}
		until, err := db.RenewResearchLease(r.Context(), id, input.Attempt, client(r))
		if errors.Is(err, store.ErrResearchLease) {
			apiError(w, http.StatusConflict, "lease_lost", "the run is over (lapsed, submitted, asked the owner, or stopped by the owner); stop its chat")
			return
		}
		if err != nil {
			apiError(w, http.StatusInternalServerError, "server_error", "could not renew the lease")
			return
		}
		writeAPI(w, http.StatusOK, map[string]any{"lease_until": until})
	})

	mux.HandleFunc("POST /api/v1/research/tasks/{id}/end", func(w http.ResponseWriter, r *http.Request) {
		id, ok := taskID(w, r)
		var input runInput
		if !ok {
			return
		}
		if err := decodeAPI(r, &input); err != nil || input.Attempt < 1 {
			apiError(w, http.StatusBadRequest, "invalid_request", "body must be {\"attempt\": n, \"error\": \"why it stopped\"}")
			return
		}
		task, err := db.EndResearchRun(r.Context(), id, input.Attempt, client(r), input.Error)
		switch {
		case store.IsNotFound(err):
			apiError(w, http.StatusNotFound, "not_found", "no research task with that id")
		case errors.Is(err, store.ErrHandoffForbidden):
			apiError(w, http.StatusForbidden, "forbidden", "another dispatcher claimed this run")
		case err != nil:
			apiError(w, http.StatusInternalServerError, "server_error", "could not end the run")
		default:
			writeAPI(w, http.StatusOK, taskOutput(task))
		}
	})

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		apiError(w, http.StatusNotFound, "not_found", "no such API endpoint")
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := bearerToken(r.Header)
		if !ok {
			apiUnauthorized(w)
			return
		}
		found, err := db.LookupAPIKey(r.Context(), secret)
		if err != nil {
			apiUnauthorized(w)
			return
		}
		if !slices.Contains(found.Scopes, store.ScopeResearchDispatch) {
			apiError(w, http.StatusForbidden, "insufficient_scope", "this key cannot dispatch research")
			return
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), apiKeyContext{}, found)))
	})
}

func apiUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	apiError(w, http.StatusUnauthorized, "invalid_key", "send an API key from Ledger's Agents page as Authorization: Bearer <key>")
}
