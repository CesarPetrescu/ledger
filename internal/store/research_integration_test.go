//go:build integration

package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func newResearch(t *testing.T, db *store.DB, ctx context.Context, title string, maxAttempts int, depends ...int64) store.ResearchTask {
	t.Helper()
	task, err := db.CreateResearchTask(ctx, store.NewResearchTask{ProjectSlug: "atlas", Title: title, MaxAttempts: maxAttempts, DependsOn: depends, Source: "claude", ClientID: "claude-client",
		Spec: store.ResearchSpec{Objective: "Compare vector databases", Acceptance: []string{"Three options", "Every claim cited"}, Budget: store.ResearchBudget{Minutes: 30}}})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func claim(t *testing.T, db *store.DB, ctx context.Context) *store.ResearchClaim {
	t.Helper()
	c, err := db.ClaimResearchTask(ctx, 60, "Adastrion dispatcher", "dispatch-client")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func lapse(t *testing.T, db *store.DB, ctx context.Context, id int64) {
	t.Helper()
	if _, err := db.Pool.Exec(ctx, `UPDATE research_task SET lease_until=now()-interval '1 second' WHERE handoff_id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func researchDB(t *testing.T) (*store.DB, context.Context) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus", Goal: "Ship search", Description: "Private roadmap"}); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

// The P0 acceptance test: a run that dies mid-way returns the task to the queue within the lease, the next
// claim is attempt 2, it starts from the checkpoint, and the dead run's token no longer works.
func TestResearchRunThatDiesIsRetriedFromItsCheckpoint(t *testing.T) {
	db, ctx := researchDB(t)
	task := newResearch(t, db, ctx, "Vector DB survey", 3)
	if task.State != "ready" || task.Attempt != 0 || task.Spec.Deliverable != "report" || task.MaxAttempts != 3 {
		t.Fatalf("created = %#v", task)
	}
	first := claim(t, db, ctx)
	if first == nil || first.Task.ID != task.ID || first.Task.Attempt != 1 || first.Task.State != "in_progress" || first.Task.Runner != "Adastrion dispatcher" || first.Token == "" {
		t.Fatalf("first claim = %#v", first)
	}
	if again := claim(t, db, ctx); again != nil {
		t.Fatalf("a leased task was claimed twice: %#v", again)
	}
	id, attempt, err := db.ResearchRun(ctx, first.Token)
	if err != nil || id != task.ID || attempt != 1 {
		t.Fatalf("run token = %d/%d, %v", id, attempt, err)
	}
	if _, err := db.SaveResearchCheckpoint(ctx, id, attempt, "read sources 1-3; next: pgvector benchmarks"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ResearchHeartbeat(ctx, id, attempt, "benchmarking"); err != nil {
		t.Fatal(err)
	}

	lapse(t, db, ctx, task.ID) // the sandbox was killed: no more heartbeats
	if n, err := db.ExpireResearchLeases(ctx); err != nil || n != 1 {
		t.Fatalf("expired = %d, %v", n, err)
	}
	after, _ := db.ResearchTask(ctx, task.ID)
	if after.State != "ready" || after.Failures != 1 || after.LeaseUntil != nil || after.Runner != "" || !strings.Contains(after.LastError, "lease expired") {
		t.Fatalf("after lapse = %#v", after)
	}
	if _, _, err := db.ResearchRun(ctx, first.Token); !store.IsNotFound(err) {
		t.Fatalf("dead run token still works: %v", err)
	}
	if _, err := db.ResearchHeartbeat(ctx, id, 1, ""); !errors.Is(err, store.ErrResearchLease) {
		t.Fatalf("heartbeat after lapse = %v", err)
	}

	second := claim(t, db, ctx)
	if second == nil || second.Task.Attempt != 2 || second.Token == first.Token {
		t.Fatalf("second claim = %#v", second)
	}
	pack, err := db.ResearchContext(ctx, task.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Task.Checkpoint != "read sources 1-3; next: pgvector benchmarks" || pack.Task.CheckpointAttempt == nil || *pack.Task.CheckpointAttempt != 1 || pack.Task.Attempt != 2 || pack.Task.Failures != 1 {
		t.Fatalf("context task = %#v", pack.Task)
	}
	var notes []string
	for _, note := range pack.Thread {
		notes = append(notes, note.From+": "+note.Body)
	}
	joined := strings.Join(notes, "\n")
	for _, want := range []string{"ledger: Run 1 started by Adastrion dispatcher.", "ledger: Run 1 ended without a result: the lease expired without a heartbeat. Retrying (failure 1 of 3).", "ledger: Run 2 started"} {
		if !strings.Contains(joined, want) {
			t.Errorf("thread missing %q:\n%s", want, joined)
		}
	}
	// The project was not shared with research, so its private summary stays out of the sandbox.
	if pack.Project == nil || pack.Project.Shared || pack.Project.Description != "" || pack.Project.Goal != "" {
		t.Fatalf("unshared project leaked: %#v", pack.Project)
	}
	if err := db.SetProjectResearchVisible(ctx, "atlas", true); err != nil {
		t.Fatal(err)
	}
	if pack, _ = db.ResearchContext(ctx, task.ID, nil); !pack.Project.Shared || pack.Project.Description != "Private roadmap" {
		t.Fatalf("shared project = %#v", pack.Project)
	}
}

func TestResearchTaskStopsAfterItsAttemptsAndTheOwnerCanRetry(t *testing.T) {
	db, ctx := researchDB(t)
	task := newResearch(t, db, ctx, "Flaky", 2)
	for run := 1; run <= 2; run++ {
		c := claim(t, db, ctx)
		if c == nil || c.Task.Attempt != run {
			t.Fatalf("run %d claim = %#v", run, c)
		}
		if _, err := db.RenewResearchLease(ctx, task.ID, run, "someone-else"); !errors.Is(err, store.ErrResearchLease) {
			t.Fatalf("another client renewed the lease: %v", err)
		}
		if _, err := db.RenewResearchLease(ctx, task.ID, run, "dispatch-client"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.EndResearchRun(ctx, task.ID, run, "dispatch-client", "exit 137"); err != nil {
			t.Fatal(err)
		}
	}
	dead, _ := db.ResearchTask(ctx, task.ID)
	if dead.State != "blocked" || dead.Phase != "dead" || dead.Failures != 2 || dead.LastError != "exit 137" {
		t.Fatalf("dead = %#v", dead)
	}
	if c := claim(t, db, ctx); c != nil {
		t.Fatalf("dead task was claimed: %#v", c)
	}
	// Ending an already ended run changes nothing.
	if again, err := db.EndResearchRun(ctx, task.ID, 2, "dispatch-client", "late"); err != nil || again.Failures != 2 || again.LastError != "exit 137" {
		t.Fatalf("repeat end = %#v, %v", again, err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, task.MessageID, "release", "", store.OwnerSource, "owner", true); err != nil {
		t.Fatal(err)
	}
	retried, _ := db.ResearchTask(ctx, task.ID)
	if retried.State != "ready" || retried.Phase != "" || retried.Failures != 0 {
		t.Fatalf("after owner retry = %#v", retried)
	}
	if c := claim(t, db, ctx); c == nil || c.Task.Attempt != 3 {
		t.Fatalf("claim after retry = %#v", c)
	}
}

func TestResearchSubmitGoesToReviewAndFeedbackReachesTheNextRun(t *testing.T) {
	db, ctx := researchDB(t)
	task := newResearch(t, db, ctx, "Survey", 3)
	c := claim(t, db, ctx)
	long := "# Findings\n\nqdrant, pgvector, lancedb\n\n" + strings.Repeat("Benchmark detail. ", 1000)
	message, err := db.SubmitResearch(ctx, task.ID, 1, long, []store.ResearchFile{{Filename: "table.csv", MediaType: "text/csv", Data: []byte("a,b\n")}})
	if err != nil || len(message.Files) != 1 || message.WorkState != "done" || message.Source != "Researcher (run 1)" {
		t.Fatalf("submit = %#v, %v", message, err)
	}
	review, _ := db.ResearchTask(ctx, task.ID)
	if review.State != "blocked" || review.Phase != "review" || review.Failures != 0 || review.LeaseUntil != nil {
		t.Fatalf("review = %#v", review)
	}
	if _, _, err := db.ResearchRun(ctx, c.Token); !store.IsNotFound(err) {
		t.Fatalf("token works after submit: %v", err)
	}
	if _, err := db.SubmitResearch(ctx, task.ID, 1, "again", nil); !errors.Is(err, store.ErrResearchLease) {
		t.Fatalf("second submit = %v", err)
	}
	if ended, _ := db.EndResearchRun(ctx, task.ID, 1, "dispatch-client", "exit 0"); ended.Phase != "review" || ended.Failures != 0 {
		t.Fatalf("end after submit = %#v", ended)
	}
	// The owner sends it back: a reply (a note, not new work), then release.
	reply, err := db.AppendHandoffMessage(ctx, store.HandoffMessage{HandoffID: task.ID, Body: "Add Milvus and cite benchmarks.", WorkState: "ready", Source: store.OwnerSource, ClientID: "owner"}, true)
	if err != nil || reply.WorkState != "done" {
		t.Fatalf("owner reply = %#v, %v", reply, err)
	}
	// A reply with an attachment goes draft, upload, publish; publishing makes it a note the run sees.
	attached, err := db.AppendHandoffMessage(ctx, store.HandoffMessage{HandoffID: task.ID, Body: "Use this benchmark sheet.", WorkState: "draft", Source: store.OwnerSource, ClientID: "owner"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddHandoffFile(ctx, attached.ID, "bench.csv", "text/csv", []byte("db,qps\n"), "owner", true); err != nil {
		t.Fatal(err)
	}
	if published, err := db.UpdateHandoffMessage(ctx, attached.ID, "publish", "", store.OwnerSource, "owner", true); err != nil || published.WorkState != "done" {
		t.Fatalf("publish attached reply = %#v, %v", published, err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, attached.ID, "reopen", "", store.OwnerSource, "owner", true); !errors.Is(err, store.ErrHandoffForbidden) {
		t.Fatalf("reopen a note = %v", err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, task.MessageID, "release", "", store.OwnerSource, "owner", true); err != nil {
		t.Fatal(err)
	}
	next := claim(t, db, ctx)
	if next == nil || next.Task.Attempt != 2 {
		t.Fatalf("next = %#v", next)
	}
	pack, _ := db.ResearchContext(ctx, task.ID, nil)
	var sawDeliverable, sawFeedback, sawAttached bool
	for _, note := range pack.Thread {
		sawDeliverable = sawDeliverable || note.From == "researcher" && note.Body == long
		sawFeedback = sawFeedback || note.From == "owner" && note.Body == "Add Milvus and cite benchmarks."
		sawAttached = sawAttached || note.From == "owner" && note.Body == "Use this benchmark sheet."
	}
	if !sawDeliverable || !sawFeedback || !sawAttached {
		t.Fatalf("thread = %#v", pack.Thread)
	}
	if err := db.AskResearchOwner(ctx, task.ID, 2, "Which Milvus version?"); err != nil {
		t.Fatal(err)
	}
	if asked, _ := db.ResearchTask(ctx, task.ID); asked.Phase != "question" || asked.State != "blocked" {
		t.Fatalf("asked = %#v", asked)
	}
	// Accepting is completing the brief; the thread then archives.
	if _, err := db.UpdateHandoffMessage(ctx, task.MessageID, "complete", "", store.OwnerSource, "owner", true); err != nil {
		t.Fatal(err)
	}
	detail, err := db.GetHandoff(ctx, task.ID, 50, nil, "", true)
	if err != nil || detail.Research == nil || detail.Research.State != "done" || detail.Research.Phase != "" || detail.Handoff.ArchivedAt == nil || detail.Handoff.Kind != "research" {
		t.Fatalf("accepted detail = %#v, %v", detail, err)
	}
	// A thank-you note on the accepted task is not new work: it stays archived.
	if _, err := db.AppendHandoffMessage(ctx, store.HandoffMessage{HandoffID: task.ID, Body: "Thanks, merged into the plan.", WorkState: "ready", Source: store.OwnerSource, ClientID: "owner"}, true); err != nil {
		t.Fatal(err)
	}
	if after, _ := db.GetHandoff(ctx, task.ID, 50, nil, "", true); after.Handoff.ArchivedAt == nil {
		t.Fatal("a note unarchived an accepted research task")
	}
}

func TestResearchWaitsForDependenciesAndIsHiddenFromAgents(t *testing.T) {
	db, ctx := researchDB(t)
	first := newResearch(t, db, ctx, "Collect sources", 3)
	second := newResearch(t, db, ctx, "Synthesize", 3, first.ID)
	if _, err := db.CreateResearchTask(ctx, store.NewResearchTask{Title: "Bad", DependsOn: []int64{999999}, Source: "claude", ClientID: "c", Spec: store.ResearchSpec{Objective: "x", Acceptance: []string{"y"}}}); err == nil {
		t.Fatal("unknown dependency accepted")
	}
	c := claim(t, db, ctx)
	if c == nil || c.Task.ID != first.ID {
		t.Fatalf("claim = %#v", c)
	}
	if blocked := claim(t, db, ctx); blocked != nil {
		t.Fatalf("dependent task claimed early: %#v", blocked)
	}
	if _, err := db.SubmitResearch(ctx, first.ID, 1, "sources", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, first.MessageID, "complete", "", store.OwnerSource, "owner", true); err != nil {
		t.Fatal(err)
	}
	if c := claim(t, db, ctx); c == nil || c.Task.ID != second.ID {
		t.Fatalf("dependent claim = %#v", c)
	}

	// Agents on /mcp see none of it, and even the owner cannot hand the brief to an agent.
	general, err := db.CreateHandoff(ctx, store.Handoff{Title: "General", Source: "claude", ClientID: "claude-client"}, store.HandoffMessage{Body: "Do it", WorkState: "ready", Source: "claude", ClientID: "claude-client"})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := db.ListHandoffs(ctx, store.HandoffFilter{Archive: "all", IncludeAll: true, CallerName: "claude", CallerClientID: "claude-client"})
	if err != nil || len(listed) != 1 || listed[0].ID != general.Handoff.ID {
		t.Fatalf("agent list = %#v, %v", listed, err)
	}
	if all, _ := db.ListHandoffs(ctx, store.HandoffFilter{Archive: "all", Admin: true}); len(all) != 3 {
		t.Fatalf("owner list = %d", len(all))
	}
	if _, err := db.GetHandoff(ctx, second.ID, 10, nil, "claude-client", false); !store.IsNotFound(err) {
		t.Fatalf("agent get = %v", err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, second.MessageID, "release", "", "claude", "claude-client", false); !store.IsNotFound(err) {
		t.Fatalf("agent update = %v", err)
	}
	if _, err := db.AppendHandoffMessage(ctx, store.HandoffMessage{HandoffID: second.ID, Body: "hi", WorkState: "ready", Source: "claude", ClientID: "claude-client"}, false); !store.IsNotFound(err) {
		t.Fatalf("agent append = %v", err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, second.MessageID, "release", "", store.OwnerSource, "owner", true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateHandoffMessage(ctx, second.MessageID, "claim", "", store.OwnerSource, "owner", true); !errors.Is(err, store.ErrHandoffForbidden) {
		t.Fatalf("owner claim of a brief = %v", err)
	}
}

// A long thread pages: the newest page first, then older pages until the first note.
func TestResearchContextPagesThroughALongThread(t *testing.T) {
	db, ctx := researchDB(t)
	task := newResearch(t, db, ctx, "Long", 3)
	for i := 1; i <= 45; i++ {
		if _, err := db.AppendHandoffMessage(ctx, store.HandoffMessage{HandoffID: task.ID, Body: fmt.Sprintf("note %d", i), WorkState: "ready", Source: store.OwnerSource, ClientID: "owner"}, true); err != nil {
			t.Fatal(err)
		}
	}
	first, err := db.ResearchContext(ctx, task.ID, nil)
	if err != nil || len(first.Thread) != 30 || first.Thread[29].Body != "note 45" || first.NextBefore == nil {
		t.Fatalf("first page = %d notes, next %v, %v", len(first.Thread), first.NextBefore, err)
	}
	second, err := db.ResearchContext(ctx, task.ID, first.NextBefore)
	if err != nil || len(second.Thread) != 15 || second.Thread[0].Body != "note 1" || second.Thread[14].Body != "note 15" || second.NextBefore != nil {
		t.Fatalf("second page = %#v, %v", second.Thread, err)
	}
}

func TestConcurrentDispatchersNeverShareATask(t *testing.T) {
	db, ctx := researchDB(t)
	newResearch(t, db, ctx, "Only one", 3)
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := db.ClaimResearchTask(ctx, 60, "dispatcher", "dispatch-client")
			if err != nil {
				t.Error(err)
			}
			if c != nil {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d dispatchers claimed the one task", won)
	}
}
