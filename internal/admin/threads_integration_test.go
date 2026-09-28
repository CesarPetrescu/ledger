//go:build integration

package admin

import (
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
	if err != nil || view.Context != "repo ledger, branch main" || len(view.Replies) != 2 || view.Replies[0].Body != "10% is right" || view.Replies[1].Source != "codex" {
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
	if res := request(t, server, http.MethodGet, "/admin/api/entries/999999/history", "", authed(s, false)); res.Code != http.StatusNotFound {
		t.Fatalf("missing history = %d", res.Code)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/entries/"+askID+"/replies", `{"body":"   "}`, authed(s, true)); res.Code != http.StatusBadRequest {
		t.Fatalf("empty reply = %d", res.Code)
	}
}
