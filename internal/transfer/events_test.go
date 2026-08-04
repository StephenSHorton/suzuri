package transfer

import (
	"testing"
)

func TestParseEventReady(t *testing.T) {
	line := []byte(`{"v":1,"event":"ready","ticket":"blobabc","relays":1,"ips":2,"path":"/tmp/f"}`)
	ev, err := ParseEvent(line)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Event != "ready" || ev.Ticket != "blobabc" {
		t.Fatalf("got %+v", ev)
	}
	if ev.Relays == nil || *ev.Relays != 1 {
		t.Fatalf("relays: %+v", ev.Relays)
	}
}

func TestParseEventProgress(t *testing.T) {
	line := []byte(`{"v":1,"event":"progress","done":10,"total":100}`)
	ev, err := ParseEvent(line)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Done == nil || *ev.Done != 10 || ev.Total == nil || *ev.Total != 100 {
		t.Fatalf("got %+v", ev)
	}
}

func TestParseEventMissing(t *testing.T) {
	if _, err := ParseEvent([]byte(`{"v":1}`)); err == nil {
		t.Fatal("expected error")
	}
}
