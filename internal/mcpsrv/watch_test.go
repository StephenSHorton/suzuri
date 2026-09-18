package mcpsrv

import (
	"path/filepath"
	"testing"

	"github.com/StephenSHorton/suzuri/internal/workspace"
)

func TestChannelWatchEmitsMentionNotSelf(t *testing.T) {
	dir := t.TempDir()
	store := workspace.New(filepath.Join(dir, "ws"))
	human, err := store.Join("alice", workspace.KindHuman, "sess-h")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := store.Join("engine", workspace.KindAgent, "sess-a")
	if err != nil {
		t.Fatal(err)
	}

	var got []map[string]any
	w := newChannelWatch(store, func(method string, params any) error {
		if method != claudeChannelMethod {
			t.Fatalf("method %s", method)
		}
		p, ok := params.(map[string]any)
		if !ok {
			t.Fatalf("params %T", params)
		}
		got = append(got, p)
		return nil
	})
	w.Bind(agent.ID)
	if w.MemberID() != agent.ID {
		t.Fatalf("bound %q", w.MemberID())
	}

	w.step()
	if len(got) != 0 {
		t.Fatalf("replayed history: %+v", got)
	}

	if _, err := store.Post("general", "hello room", human.ID, "", workspace.KindHuman, ""); err != nil {
		t.Fatal(err)
	}
	w.step()
	if len(got) != 0 {
		t.Fatalf("ungated post woke agent: %+v", got)
	}

	if _, err := store.Post("general", "@engine please look", human.ID, "", workspace.KindHuman, ""); err != nil {
		t.Fatal(err)
	}
	w.step()
	if len(got) != 1 {
		t.Fatalf("want 1 mention wake, got %d %+v", len(got), got)
	}
	if got[0]["content"] != "@engine please look" {
		t.Fatalf("content %+v", got[0]["content"])
	}
	meta, _ := got[0]["meta"].(map[string]string)
	if meta["member_id"] != agent.ID || meta["from"] != "alice" {
		t.Fatalf("meta %+v", meta)
	}

	if _, err := store.Post("general", "@engine from myself", agent.ID, "", workspace.KindAgent, ""); err != nil {
		t.Fatal(err)
	}
	w.step()
	if len(got) != 1 {
		t.Fatalf("self mention woke: %+v", got)
	}
}

func TestChannelNotificationParamsShape(t *testing.T) {
	p := channelNotificationParams("hi", map[string]string{"channel": "general", "message_id": "m1"})
	if p["content"] != "hi" {
		t.Fatalf("%+v", p)
	}
	meta := p["meta"].(map[string]string)
	if meta["channel"] != "general" || meta["message_id"] != "m1" {
		t.Fatalf("%+v", meta)
	}
}
