//go:build integration

package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestRepliesThreadAndHistory(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	for _, slug := range []string{"atlas", "beacon"} {
		if _, err := db.UpsertProject(ctx, store.Project{Slug: slug, Name: slug, Tier: "focus"}); err != nil {
			t.Fatal(err)
		}
	}
	ask, _, err := db.Append(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Which pricing is right?", Source: "codex", ClientID: "c", Context: "repo ledger, branch main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveEntryMeta(ctx, ask.ID, store.EntryMeta{Title: "Pricing", Ask: "Confirm the pricing"}); err != nil {
		t.Fatal(err)
	}
	askID := strconv.FormatInt(ask.ID, 10)
	needsYou := func() bool {
		res := request(t, server, http.MethodGet, "/admin/api/entries?needs=you", "", authed(s, false))
		return strings.Contains(res.Body.String(), `"id":"`+askID+`"`)
	}
	if !needsYou() {
		t.Fatal("ask not in Needs you")
	}

	// The owner answers: saved under the ask, which leaves Needs you, undoably.
	res := request(t, server, http.MethodPost, "/admin/api/entries/"+askID+"/replies", `{"body":"  10% is right  "}`, authed(s, true))
	var reply struct {
		ID       string `json:"id"`
		ReplyTo  string `json:"reply_to"`
		Body     string `json:"body"`
		ActionID string `json:"action_id"`
	}
	if res.Code != http.StatusCreated || json.Unmarshal(res.Body.Bytes(), &reply) != nil || reply.ReplyTo != askID || reply.Body != "10% is right" || reply.ActionID == "" {
		t.Fatalf("reply = %d %s", res.Code, res.Body.String())
	}
	if needsYou() {
		t.Fatal("answered ask still in Needs you")
	}
	// The owner's own reply never asks the owner anything.
	replyID, _ := strconv.ParseInt(reply.ID, 10, 64)
	if err := db.SaveEntryMeta(ctx, replyID, store.EntryMeta{Title: "Reply", Ask: "echo"}); err != nil {
		t.Fatal(err)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/entries?needs=you", "", authed(s, false)); strings.Contains(res.Body.String(), `"id":"`+reply.ID+`"`) {
		t.Fatal("owner reply listed in Needs you")
	}

	// An agent answering the reply joins the root's thread and brings it back.
	answer, _, err := db.Append(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Updated the site", Source: "codex", ClientID: "c", ReplyTo: replyID})
	if err != nil || answer.ReplyTo == nil || *answer.ReplyTo != ask.ID {
		t.Fatalf("agent answer = %+v %v", answer, err)
	}
	if !needsYou() {
		t.Fatal("agent answer did not bring the ask back")
	}
	if _, _, err := db.Append(ctx, store.NewEntry{Slug: "beacon", Kind: "note", Body: "Wrong place", Source: "codex", ClientID: "c", ReplyTo: ask.ID}); err != store.ErrReplyElsewhere {
		t.Fatalf("cross-project reply err = %v", err)
	}
	if _, _, err := db.Append(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Lost", Source: "codex", ClientID: "c", ReplyTo: 999999}); err != store.ErrReplyNotFound {
		t.Fatalf("missing root err = %v", err)
	}

	// Agents read the thread through get_entry.
	view, err := db.GetEntry(ctx, askID)
	if err != nil || view.Context != "repo ledger, branch main" || view.RepliesTotal != 2 || len(view.Replies) != 2 || view.Replies[0].Body != "10% is right" || view.Replies[1].Source != "codex" {
		t.Fatalf("view = %+v %v", view, err)
	}

	// A label correction is part of the history too.
	if err := db.SetLabels(ctx, ask.ID, map[string]any{"title": "Pricing check"}, nil); err != nil {
		t.Fatal(err)
	}
	res = request(t, server, http.MethodGet, "/admin/api/entries/"+askID+"/history", "", authed(s, false))
	var history struct {
		History []struct {
			Kind, Actor, Text string
		} `json:"history"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &history) != nil {
		t.Fatalf("history = %d %s", res.Code, res.Body.String())
	}
	var got []string
	for _, h := range history.History {
		got = append(got, h.Kind+"/"+h.Actor+"/"+h.Text)
	}
	joined := strings.Join(got, " | ")
	for _, want := range []string{"created/codex/", "action/ledger-admin/Replied, marked handled", "reply/ledger-admin/10% is right", "reply/codex/Updated the site", "labels/ledger-admin/title"} {
		if !strings.Contains(joined, want) {
			t.Errorf("history missing %q in %s", want, joined)
		}
	}
	if !strings.HasPrefix(joined, "created/") {
		t.Errorf("history not oldest first: %s", joined)
	}
	// A long history keeps its newest events and says older ones were left out.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO entry_label_edit(entry_id,fields,created_at) SELECT $1,'{old}',now()-interval '1 year'+g*interval '1 second' FROM generate_series(1,250) g`, ask.ID); err != nil {
		t.Fatal(err)
	}
	res = request(t, server, http.MethodGet, "/admin/api/entries/"+askID+"/history", "", authed(s, false))
	var long struct {
		History   []struct{ Kind, Text string } `json:"history"`
		Truncated bool                          `json:"truncated"`
	}
	if json.Unmarshal(res.Body.Bytes(), &long) != nil || !long.Truncated || len(long.History) != 200 || long.History[len(long.History)-1].Kind != "labels" || long.History[len(long.History)-1].Text != "title" {
		t.Fatalf("long history: truncated=%v len=%d last=%+v", long.Truncated, len(long.History), long.History[len(long.History)-1])
	}

	// With the root in Trash, a reply to one of its replies still joins the root.
	if _, _, err := db.TrashEntry(ctx, ask.ID); err != nil {
		t.Fatal(err)
	}
	late, _, err := db.Append(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "While it was away", Source: "codex", ClientID: "c", ReplyTo: replyID})
	if err != nil || late.ReplyTo == nil || *late.ReplyTo != ask.ID {
		t.Fatalf("reply under trashed root = %+v %v", late, err)
	}

	if res := request(t, server, http.MethodGet, "/admin/api/entries/999999/history", "", authed(s, false)); res.Code != http.StatusNotFound {
		t.Fatalf("missing history = %d", res.Code)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/entries/"+askID+"/replies", `{"body":"   "}`, authed(s, true)); res.Code != http.StatusBadRequest {
		t.Fatalf("empty reply = %d", res.Code)
	}
}

func TestGetEntryListsTheNewestReplies(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	root, _, err := db.Append(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Root", Source: "codex", ClientID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO entry(slug,kind,body,source,client_id,reply_to,created_at) SELECT 'atlas','note','reply '||g,'codex','c',$1,now()+g*interval '1 second' FROM generate_series(1,105) g`, root.ID); err != nil {
		t.Fatal(err)
	}
	view, err := db.GetEntry(ctx, strconv.FormatInt(root.ID, 10))
	if err != nil || view.RepliesTotal != 105 || len(view.Replies) != 100 || view.Replies[0].Body != "reply 6" || view.Replies[99].Body != "reply 105" {
		t.Fatalf("replies total=%d len=%d first=%q last=%q err=%v", view.RepliesTotal, len(view.Replies), view.Replies[0].Body, view.Replies[len(view.Replies)-1].Body, err)
	}
}

func TestFollowUpsSnoozesAndThreadHistory(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, s := login(t, server, "correct horse", "")
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	add := func(body, source string, replyTo int64, ask string) store.Entry {
		e, _, err := db.Append(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: body, Source: source, ClientID: "c", ReplyTo: replyTo})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SaveEntryMeta(ctx, e.ID, store.EntryMeta{Title: body, Ask: ask}); err != nil {
			t.Fatal(err)
		}
		return e
	}
	handled := func(id int64) bool {
		found, err := db.ListEntries(ctx, store.EntryFilter{ID: id})
		if err != nil || len(found) != 1 {
			t.Fatal(err)
		}
		return found[0].Owner.Handled
	}
	root := add("Which plan?", "codex", 0, "Pick a plan")
	// Snoozed, then an agent writes more: it comes back now, not at the snooze date.
	if _, _, err := db.SetOwnerState(ctx, root.ID, store.OwnerPatch{SnoozeDays: ptr(7)}); err != nil {
		t.Fatal(err)
	}
	followUp := add("Also: monthly or yearly?", "codex", root.ID, "Monthly or yearly billing")
	needs, err := db.ListEntries(ctx, store.EntryFilter{NeedsYou: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, e := range needs {
		ids[e.ID] = true
	}
	if !ids[root.ID] || !ids[followUp.ID] {
		t.Fatalf("needs you = %v, want root %d and follow-up %d", ids, root.ID, followUp.ID)
	}
	// Answering the follow-up settles the follow-up; the root still waits.
	res := request(t, server, http.MethodPost, "/admin/api/entries/"+strconv.FormatInt(followUp.ID, 10)+"/replies", `{"body":"Yearly"}`, authed(s, true))
	if res.Code != http.StatusCreated || !handled(followUp.ID) || handled(root.ID) {
		t.Fatalf("reply = %d %s; follow-up handled=%v root handled=%v", res.Code, res.Body.String(), handled(followUp.ID), handled(root.ID))
	}
	// The agent answers the follow-up: the follow-up needs the owner again.
	add("Yearly it is; one more thing", "codex", followUp.ID, "")
	if handled(followUp.ID) {
		t.Fatal("agent answer did not bring the follow-up back")
	}
	// Opened on the follow-up, the history shows the whole conversation.
	res = request(t, server, http.MethodGet, "/admin/api/entries/"+strconv.FormatInt(followUp.ID, 10)+"/history", "", authed(s, false))
	if body := res.Body.String(); !strings.Contains(body, `"text":"Yearly"`) || strings.Contains(body, `"text":"Also: monthly or yearly?","entry_id"`) {
		t.Fatalf("follow-up history = %s", body)
	}
}

func TestTrashKeepsLabelHistoryUntilPurgedAndRestoresOlderPayloads(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	edited := func(title string) int64 {
		e, _, err := db.Append(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: title, Source: "codex", ClientID: "c"})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SaveEntryMeta(ctx, e.ID, store.EntryMeta{Title: title}); err != nil {
			t.Fatal(err)
		}
		if err := db.SetLabels(ctx, e.ID, map[string]any{"title": title + " fixed"}, nil); err != nil {
			t.Fatal(err)
		}
		return e.ID
	}
	edits := func(id int64) (n int) {
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM entry_label_edit WHERE entry_id=$1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	kept, purged := edited("Kept"), edited("Purged")
	keptTrash, _, err := db.TrashEntry(ctx, kept)
	if err != nil {
		t.Fatal(err)
	}
	// A payload trashed before entries had a context still restores.
	if _, err := db.Pool.Exec(ctx, `UPDATE trash SET payload=payload #- '{entry,context}' #- '{entry,reply_to}' WHERE id=$1`, keptTrash); err != nil {
		t.Fatal(err)
	}
	if err := db.RestoreTrash(ctx, keptTrash); err != nil {
		t.Fatalf("restore older payload: %v", err)
	}
	if edits(kept) != 1 {
		t.Fatal("restored entry lost its label history")
	}
	purgedTrash, _, err := db.TrashEntry(ctx, purged)
	if err != nil {
		t.Fatal(err)
	}
	if edits(purged) != 1 {
		t.Fatal("label history gone while the entry is still in Trash")
	}
	if err := db.DeleteTrash(ctx, purgedTrash); err != nil {
		t.Fatal(err)
	}
	if edits(purged) != 0 {
		t.Fatal("label history outlived the entry")
	}
}

func TestAgentAnswerReopensAFollowUpUnderATrashedRoot(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	root, _, _ := db.Append(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Root", Source: "codex", ClientID: "c"})
	child, _, _ := db.Append(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Follow-up", Source: "codex", ClientID: "c", ReplyTo: root.ID})
	if err := db.SaveEntryMeta(ctx, child.ID, store.EntryMeta{Title: "Follow-up", Ask: "Which one?"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SetOwnerState(ctx, child.ID, store.OwnerPatch{Handled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.TrashEntry(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Append(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Answer", Source: "codex", ClientID: "c", ReplyTo: child.ID}); err != nil {
		t.Fatal(err)
	}
	found, err := db.ListEntries(ctx, store.EntryFilter{ID: child.ID})
	if err != nil || len(found) != 1 || found[0].Owner.Handled {
		t.Fatalf("follow-up = %+v %v", found, err)
	}
}

func TestPlainRetriesKeepTheirReceipts(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	first, err := db.AppendEntryOnce(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Once", Source: "glass", ClientID: "c"}, "retry-0001")
	if err != nil {
		t.Fatal(err)
	}
	// A receipt written before replies existed hashed only these four fields.
	legacy := sha256.Sum256([]byte(`["atlas","note","Once","glass"]`))
	var stored []byte
	if err := db.Pool.QueryRow(ctx, `SELECT payload_hash FROM entry_write_receipt WHERE request_id='retry-0001'`).Scan(&stored); err != nil || !bytes.Equal(stored, legacy[:]) {
		t.Fatalf("receipt hash changed format: %x %v", stored, err)
	}
	again, err := db.AppendEntryOnce(ctx, store.NewEntry{Slug: "atlas", Kind: "note", Body: "Once", Source: "glass", ClientID: "c"}, "retry-0001")
	if err != nil || again.ID != first.ID {
		t.Fatalf("retry = %+v %v", again, err)
	}
}
