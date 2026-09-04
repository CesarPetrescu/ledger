//go:build integration

package store_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestHandoffLifecycleRoutingAndFiles(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "ledger", Name: "Ledger", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	detail, err := db.CreateHandoff(ctx,
		store.Handoff{ProjectSlug: "ledger", Title: "Ship handoffs", Description: "Pass work between agents", Scope: "ledger backend", Source: "codex", ClientID: "codex-client"},
		store.HandoffMessage{Body: "Implement the store flow.", Target: "Claude", WorkState: "ready", Source: "codex", ClientID: "codex-client"},
	)
	if err != nil || detail.Handoff.ID == 0 || len(detail.Messages) != 1 || detail.Handoff.ReadyCount != 1 {
		t.Fatalf("create handoff = %#v, %v", detail, err)
	}
	messageID := detail.Messages[0].ID

	codex, err := db.ListHandoffs(ctx, store.HandoffFilter{CallerName: "Codex CLI", CallerClientID: "codex-client", Limit: 20})
	if err != nil || len(codex) != 0 {
		t.Fatalf("codex default queue = %#v, %v", codex, err)
	}
	claude, err := db.ListHandoffs(ctx, store.HandoffFilter{CallerName: "Claude Code", CallerClientID: "claude-client", Limit: 20})
	if err != nil || len(claude) != 1 {
		t.Fatalf("claude default queue = %#v, %v", claude, err)
	}

	claimed, err := db.UpdateHandoffMessage(ctx, messageID, "claim", "", "claude", "claude-client", false)
	if err != nil || claimed.WorkState != "in_progress" || claimed.SeenAt == nil || claimed.ClaimedClientID != "claude-client" {
		t.Fatalf("claim = %#v, %v", claimed, err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, messageID, "claim", "", "codex", "codex-client", false); !errors.Is(err, store.ErrHandoffConflict) {
		t.Fatalf("second claim = %v", err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, messageID, "complete", "", "codex", "codex-client", false); !errors.Is(err, store.ErrHandoffForbidden) {
		t.Fatalf("foreign complete = %v", err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, messageID, "complete", "", "claude", "claude-client", false); err != nil {
		t.Fatal(err)
	}
	done, err := db.GetHandoff(ctx, detail.Handoff.ID, 20, nil, "codex-client", true)
	if err != nil || done.Handoff.ArchivedAt == nil || done.Handoff.DoneCount != 1 {
		t.Fatalf("completed handoff = %#v, %v", done.Handoff, err)
	}

	draft, err := db.AppendHandoffMessage(ctx, store.HandoffMessage{HandoffID: detail.Handoff.ID, Body: "Follow-up with files", Target: "Codex", WorkState: "draft", Source: "claude", ClientID: "claude-client"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := db.GetHandoff(ctx, detail.Handoff.ID, 20, nil, "codex-client", true)
	if err != nil || active.Handoff.ArchivedAt != nil || active.Handoff.DraftCount != 1 || active.Handoff.DoneCount != 1 {
		t.Fatalf("reactivated handoff = %#v, %v", active.Handoff, err)
	}
	searched, err := db.ListHandoffs(ctx, store.HandoffFilter{Query: "follow-up files", Admin: true, Archive: "all", Limit: 20})
	if err != nil || len(searched) != 1 || searched[0].ID != detail.Handoff.ID {
		t.Fatalf("message full-text search = %#v, %v", searched, err)
	}
	for i := 0; i < store.MaxHandoffFiles; i++ {
		if _, err := db.AddHandoffFile(ctx, draft.ID, fmt.Sprintf("file-%d.txt", i), "text/plain", []byte("content"), "claude-client", false); err != nil {
			t.Fatalf("add file %d: %v", i, err)
		}
	}
	if _, err := db.AddHandoffFile(ctx, draft.ID, "overflow.txt", "text/plain", []byte("x"), "claude-client", false); !errors.Is(err, store.ErrHandoffFileLimit) {
		t.Fatalf("eleventh file = %v", err)
	}
	files, err := db.ListProjectHandoffFiles(ctx, "ledger")
	if err != nil || len(files) != store.MaxHandoffFiles {
		t.Fatalf("project files = %d, %v", len(files), err)
	}
	visible, err := db.GetHandoff(ctx, detail.Handoff.ID, 20, nil, "codex-client", false)
	if err != nil || len(visible.Messages) != 1 {
		t.Fatalf("other-client draft visibility = %#v, %v", visible.Messages, err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, draft.ID, "publish", "", "codex", "codex-client", false); !errors.Is(err, store.ErrHandoffForbidden) {
		t.Fatalf("foreign publish = %v", err)
	}
	if retargeted, err := db.UpdateHandoffMessage(ctx, draft.ID, "retarget", "Codex CLI", "claude", "claude-client", false); err != nil || retargeted.Target != "Codex CLI" {
		t.Fatalf("retarget = %#v, %v", retargeted, err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, draft.ID, "publish", "", "claude", "claude-client", false); err != nil {
		t.Fatal(err)
	}
	if acknowledged, err := db.UpdateHandoffMessage(ctx, draft.ID, "acknowledge", "", "codex", "codex-client", false); err != nil || acknowledged.SeenAt == nil || acknowledged.WorkState != "ready" {
		t.Fatalf("acknowledge = %#v, %v", acknowledged, err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, draft.ID, "claim", "", "codex", "codex-client", false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, draft.ID, "block", "", "codex", "codex-client", false); err != nil {
		t.Fatal(err)
	}
	if released, err := db.UpdateHandoffMessage(ctx, draft.ID, "release", "", "codex", "codex-client", false); err != nil || released.WorkState != "ready" || released.ClaimedAt != nil {
		t.Fatalf("release = %#v, %v", released, err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, draft.ID, "claim", "", "codex", "codex-client", false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, draft.ID, "complete", "", "codex", "codex-client", false); err != nil {
		t.Fatal(err)
	}
	if reopened, err := db.UpdateHandoffMessage(ctx, draft.ID, "reopen", "", "ledger-admin", "admin", true); err != nil || reopened.WorkState != "ready" {
		t.Fatalf("admin reopen = %#v, %v", reopened, err)
	}
	reopened, err := db.GetHandoff(ctx, detail.Handoff.ID, 20, nil, "", true)
	if err != nil || reopened.Handoff.ArchivedAt != nil {
		t.Fatalf("reopened handoff = %#v, %v", reopened.Handoff, err)
	}
}

func TestConcurrentAppendAndCompletionKeepHandoffActive(t *testing.T) {
	db, ctx := testdb.Open(t)
	detail, err := db.CreateHandoff(ctx,
		store.Handoff{Title: "Concurrent handoff", Source: "author", ClientID: "author-client"},
		store.HandoffMessage{Body: "Original work", WorkState: "ready", Source: "author", ClientID: "author-client"},
	)
	if err != nil {
		t.Fatal(err)
	}
	messageID := detail.Messages[0].ID
	if _, err := db.UpdateHandoffMessage(ctx, messageID, "claim", "", "worker", "worker-client", false); err != nil {
		t.Fatal(err)
	}

	guard, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Rollback(ctx)
	if _, err := guard.Exec(ctx, `SELECT id FROM handoff WHERE id=$1 FOR UPDATE`, detail.Handoff.ID); err != nil {
		t.Fatal(err)
	}
	waitForBlocked := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var count int
			if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query ILIKE '%handoff%'`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count >= want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("only %d handoff transactions blocked", want-1)
	}

	appendDone := make(chan error, 1)
	go func() {
		_, err := db.AppendHandoffMessage(ctx, store.HandoffMessage{HandoffID: detail.Handoff.ID, Body: "New active work", WorkState: "ready", Source: "author", ClientID: "author-client"})
		appendDone <- err
	}()
	waitForBlocked(1)
	completeDone := make(chan error, 1)
	go func() {
		_, err := db.UpdateHandoffMessage(ctx, messageID, "complete", "", "worker", "worker-client", false)
		completeDone <- err
	}()
	waitForBlocked(2)
	if err := guard.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-appendDone; err != nil {
		t.Fatal(err)
	}
	if err := <-completeDone; err != nil {
		t.Fatal(err)
	}

	got, err := db.GetHandoff(ctx, detail.Handoff.ID, 20, nil, "", true)
	if err != nil || got.Handoff.ArchivedAt != nil || got.Handoff.ReadyCount != 1 || got.Handoff.DoneCount != 1 {
		t.Fatalf("handoff after concurrent append/completion = %#v, %v", got.Handoff, err)
	}
}

func TestConcurrentCompletionsArchiveHandoff(t *testing.T) {
	db, ctx := testdb.Open(t)
	detail, err := db.CreateHandoff(ctx,
		store.Handoff{Title: "Concurrent completions", Source: "author", ClientID: "author-client"},
		store.HandoffMessage{Body: "First task", WorkState: "ready", Source: "author", ClientID: "author-client"},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.AppendHandoffMessage(ctx, store.HandoffMessage{HandoffID: detail.Handoff.ID, Body: "Second task", WorkState: "ready", Source: "author", ClientID: "author-client"})
	if err != nil {
		t.Fatal(err)
	}
	messageIDs := []int64{detail.Messages[0].ID, second.ID}
	for _, id := range messageIDs {
		if _, err := db.UpdateHandoffMessage(ctx, id, "claim", "", "worker", "worker-client", false); err != nil {
			t.Fatal(err)
		}
	}

	guard, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Rollback(ctx)
	if _, err := guard.Exec(ctx, `SELECT id FROM handoff WHERE id=$1 FOR UPDATE`, detail.Handoff.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, len(messageIDs))
	for _, id := range messageIDs {
		go func() {
			_, err := db.UpdateHandoffMessage(ctx, id, "complete", "", "worker", "worker-client", false)
			done <- err
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query ILIKE '%handoff%'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == len(messageIDs) {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("completion transactions did not block on the handoff")
	}
	if err := guard.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range messageIDs {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.GetHandoff(ctx, detail.Handoff.ID, 20, nil, "", true)
	if err != nil || got.Handoff.ArchivedAt == nil || got.Handoff.DoneCount != 2 {
		t.Fatalf("handoff after concurrent completions = %#v, %v", got.Handoff, err)
	}
}

func TestHandoffRoutingTreatsWildcardCharactersLiterally(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.CreateHandoff(ctx,
		store.Handoff{Title: "Literal wildcard", Source: "author", ClientID: "author-client"},
		store.HandoffMessage{Body: "Only for percent", Target: "%", WorkState: "ready", Source: "author", ClientID: "author-client"},
	); err != nil {
		t.Fatal(err)
	}
	got, err := db.ListHandoffs(ctx, store.HandoffFilter{CallerName: "Claude", CallerClientID: "claude-client", Limit: 20})
	if err != nil || len(got) != 0 {
		t.Fatalf("literal wildcard routing = %#v, %v", got, err)
	}
}
