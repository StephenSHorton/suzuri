package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistorySinceIDReturnsOnlyNewer(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	m, err := s.Join("bot", KindAgent, "hist-1")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Post("general", "one", m.ID, "", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Post("general", "two", m.ID, "", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Post("general", "three", m.ID, "", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}

	newer, err := s.HistorySince("general", 50, b.ID, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(newer) != 1 || newer[0].ID != c.ID || newer[0].Body != "three" {
		t.Fatalf("since %s: %+v (want only %s)", b.ID, newer, c.ID)
	}

	// Apply op
	r := Apply(s, Request{Op: OpHistory, Channel: "general", SinceID: a.ID, Limit: 50})
	if !r.OK {
		t.Fatalf("apply: %+v", r)
	}
	for _, msg := range r.Messages {
		if msg.ID == a.ID {
			t.Fatalf("since_id included cursor: %+v", r.Messages)
		}
	}
	if r.Count < 2 {
		t.Fatalf("expected messages after a, got %+v", r.Messages)
	}

	// after_ts: everything after b's timestamp
	after, err := s.HistorySince("general", 50, "", b.TS)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].ID != c.ID {
		t.Fatalf("after_ts: %+v", after)
	}
}

func TestWaitTimeoutReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	if _, err := s.Join("bot", KindAgent, "wait-to"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	msgs, err := s.Wait("general", "msg_none", 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("want empty on timeout, got %+v", msgs)
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Fatalf("returned too fast (%s); must not busy-spin", time.Since(start))
	}

	r := Apply(s, Request{Op: OpWait, Channel: "general", Since: "msg_none", Timeout: 1})
	if !r.OK {
		t.Fatalf("apply wait: %+v", r)
	}
	if r.Messages == nil {
		t.Fatal("messages should be empty list, not nil")
	}
	if r.Count != 0 || len(r.Messages) != 0 {
		t.Fatalf("want empty list, got %+v", r)
	}
}

func TestWaitReturnsNewPost(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	m, err := s.Join("bot", KindAgent, "wait-post")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Post("general", "already here", m.ID, "", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	var got []Message
	go func() {
		msgs, err := s.Wait("general", first.ID, 3*time.Second)
		got = msgs
		errc <- err
	}()
	time.Sleep(200 * time.Millisecond)
	posted, err := s.Post("general", "hello waiter", m.ID, "", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != posted.ID || got[0].Body != "hello waiter" {
		t.Fatalf("wait got %+v want %s", got, posted.ID)
	}
}

func TestInboxFiltersToMember(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	alice, err := s.Join("alice", KindAgent, "in-a")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.Join("bob", KindAgent, "in-b")
	if err != nil {
		t.Fatal(err)
	}
	mention, err := s.Post("general", "hey @bob please look", alice.ID, "", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Post("general", "no mention here", alice.ID, "", KindAgent, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Post("general", "hey @alice", bob.ID, "", KindAgent, ""); err != nil {
		t.Fatal(err)
	}

	inbox, err := s.Inbox(bob.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 1 || inbox[0].ID != mention.ID {
		t.Fatalf("inbox=%+v want only %s", inbox, mention.ID)
	}

	r := Apply(s, Request{Op: OpInbox, MemberID: bob.ID, SinceID: ""})
	if !r.OK || r.Count != 1 || r.Messages[0].ID != mention.ID {
		t.Fatalf("apply inbox: %+v", r)
	}

	// since_id excludes the mention itself
	empty, err := s.Inbox(bob.ID, mention.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("inbox since mention should be empty, got %+v", empty)
	}

	// assignment field on a raw message
	assign := Message{
		ID:       "msg_assign1",
		Channel:  "general",
		TS:       time.Now().UTC(),
		FromID:   alice.ID,
		FromName: alice.Name,
		FromKind: KindAgent,
		Kind:     "text",
		Body:     "take this",
		To:       bob.ID,
	}
	raw, _ := json.Marshal(assign)
	path := filepath.Join(s.Root(), "channels", "general", "messages.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	inbox2, err := s.Inbox(bob.ID, mention.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox2) != 1 || inbox2[0].ID != assign.ID {
		t.Fatalf("assign inbox=%+v", inbox2)
	}
}

func TestStalePresenceWorkingNotPolling(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	m, err := s.Join("worker", KindAgent, "stale-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(m.ID, "", AvailWorking, nil); err != nil {
		t.Fatal(err)
	}

	// Fresh working member is polling.
	list, err := s.Members()
	if err != nil {
		t.Fatal(err)
	}
	var fresh *Member
	for i := range list {
		if list[i].ID == m.ID {
			fresh = &list[i]
			break
		}
	}
	if fresh == nil || fresh.Stale || fresh.Polling == nil || !*fresh.Polling {
		t.Fatalf("fresh working should be polling: %+v", fresh)
	}

	// Backdate last_seen on disk (computed fields are not persisted).
	path := filepath.Join(s.Root(), "members.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored []Member
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	for i := range stored {
		if stored[i].ID == m.ID {
			stored[i].LastSeen = time.Now().UTC().Add(-3 * time.Minute)
			stored[i].Status = AvailWorking
		}
	}
	out, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err = s.Members()
	if err != nil {
		t.Fatal(err)
	}
	var stale *Member
	for i := range list {
		if list[i].ID == m.ID {
			stale = &list[i]
			break
		}
	}
	if stale == nil {
		t.Fatal("member missing")
	}
	if !stale.Stale || stale.PresenceNote != "not_polling" || stale.Polling == nil || *stale.Polling {
		t.Fatalf("want stale not_polling, got %+v", stale)
	}

	r := Apply(s, Request{Op: OpMembers})
	if !r.OK {
		t.Fatalf("members: %+v", r)
	}
	found := false
	for _, mem := range r.Members {
		if mem.ID == m.ID {
			found = true
			if !mem.Stale || mem.PresenceNote != "not_polling" {
				t.Fatalf("apply members: %+v", mem)
			}
		}
	}
	if !found {
		t.Fatal("member not in apply result")
	}
}
