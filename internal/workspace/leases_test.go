package workspace

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLeaseExclusive(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	a, err := s.Join("alpha", KindAgent, "lease-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Join("bravo", KindAgent, "lease-b")
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.AcquireLease("js/main.js", a.ID, "10m", false, "general")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "js/main.js" || got.MemberID != a.ID {
		t.Fatalf("lease: %+v", got)
	}

	_, err = s.AcquireLease("js/main.js", b.ID, "10m", false, "general")
	if err == nil {
		t.Fatal("expected second lease to fail")
	}
	if !strings.Contains(err.Error(), "already leased") {
		t.Fatalf("error: %v", err)
	}

	// Same member renews.
	renew, err := s.AcquireLease("./js/main.js", a.ID, "15m", false, "general")
	if err != nil || renew.MemberID != a.ID {
		t.Fatalf("renew: %+v %v", renew, err)
	}

	list, err := s.ListLeases()
	if err != nil || len(list) != 1 || list[0].Path != "js/main.js" {
		t.Fatalf("list: %+v %v", list, err)
	}

	r := Apply(s, Request{Op: OpLeaseList})
	if !r.OK || r.Count != 1 || len(r.Leases) != 1 {
		t.Fatalf("apply list: %+v", r)
	}
}

func TestLeaseStealPostsSystem(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	a, err := s.Join("alpha", KindAgent, "steal-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Join("bravo", KindAgent, "steal-b")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.AcquireLease("js/main.js", a.ID, "10m", false, "general"); err != nil {
		t.Fatal(err)
	}
	stolen, err := s.AcquireLease("js/main.js", b.ID, "10m", true, "general")
	if err != nil {
		t.Fatal(err)
	}
	if stolen.MemberID != b.ID {
		t.Fatalf("steal holder: %+v", stolen)
	}

	hist, err := s.History("general", 50)
	if err != nil {
		t.Fatal(err)
	}
	want := "stole lease js/main.js from @" + a.ID
	if !historyHas(hist, "system", want) {
		t.Fatalf("missing steal system line %q: %+v", want, hist)
	}
	if !historyHas(hist, "system", "@"+b.ID) {
		t.Fatalf("steal line should mention new holder: %+v", hist)
	}

	r := Apply(s, Request{
		Op: OpLease, Path: "lib/util.js", MemberID: a.ID, TTL: "5m",
	})
	if !r.OK || r.Lease == nil || r.Lease.Path != "lib/util.js" {
		t.Fatalf("apply lease: %+v", r)
	}
	r = Apply(s, Request{
		Op: OpLease, Path: "lib/util.js", MemberID: b.ID, Steal: true,
	})
	if !r.OK || r.Lease == nil || r.Lease.MemberID != b.ID {
		t.Fatalf("apply steal: %+v", r)
	}
}
