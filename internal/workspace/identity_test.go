package workspace

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJoinEmptySessionCreatesDistinctMembers(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))

	a, err := s.Join("engine", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Join("engine", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatalf("expected two members, got same id %s", a.ID)
	}
	if a.Name != "engine" {
		t.Fatalf("first name=%q want engine", a.Name)
	}
	if b.Name != "engine-2" {
		t.Fatalf("second name=%q want engine-2", b.Name)
	}
	list, err := s.Members()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("members=%d want 2: %+v", len(list), list)
	}

	// Same non-empty session_id reuses the member.
	c, err := s.Join("engine", KindAgent, "sess-stable")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Join("engine", KindAgent, "sess-stable")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != d.ID {
		t.Fatalf("same session should reuse: %s vs %s", c.ID, d.ID)
	}

	// Join must not post a system line to #general.
	hist, err := s.History("general", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range hist {
		if m.Kind == "system" && strings.Contains(m.Body, "joined") {
			t.Fatalf("join posted system line: %+v", m)
		}
	}
}

func TestClaimRoleExclusive(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	a, err := s.Join("engine", KindAgent, "s1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Join("engine", KindAgent, "s2")
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "engine-2" {
		t.Fatalf("suffix: %q", b.Name)
	}
	got, err := s.ClaimRole(a.ID, "engine")
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != RoleEngine {
		t.Fatalf("role=%q", got.Role)
	}
	// Name is still the display name; role is a separate field.
	if got.Name != "engine" {
		t.Fatalf("name should stay engine, got %q", got.Name)
	}
	// Idempotent for the holder.
	again, err := s.ClaimRole(a.ID, "engine")
	if err != nil {
		t.Fatal(err)
	}
	if again.Role != RoleEngine {
		t.Fatalf("reclaim: %+v", again)
	}
	_, err = s.ClaimRole(b.ID, "engine")
	if err == nil {
		t.Fatal("expected exclusive role failure")
	}
	if !strings.Contains(err.Error(), "already held") {
		t.Fatalf("err=%v", err)
	}
	// Other role is free.
	pm, err := s.ClaimRole(b.ID, "pm")
	if err != nil {
		t.Fatal(err)
	}
	if pm.Role != RolePM {
		t.Fatalf("pm: %+v", pm)
	}
	// Apply op
	r := Apply(s, Request{Op: OpClaimRole, MemberID: a.ID, Role: "content"})
	if !r.OK || r.Member == nil || r.Member.Role != RoleContent {
		t.Fatalf("apply claim_role: %+v", r)
	}
	// Invalid role
	r = Apply(s, Request{Op: OpClaimRole, MemberID: b.ID, Role: "wizard"})
	if r.OK {
		t.Fatalf("invalid role should fail: %+v", r)
	}
}

func TestMentionTargetsMemberID(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	e1, err := s.Join("engine", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	e2, err := s.Join("engine", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	if e2.Name != "engine-2" {
		t.Fatalf("name=%q", e2.Name)
	}
	human, err := s.Join("alice", KindHuman, "h1")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.Post("general", "@engine-2 hello", human.ID, "", KindHuman, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Mentions) != 1 || msg.Mentions[0] != e2.ID {
		t.Fatalf("mentions=%v want [%s] (e1=%s e2=%s)", msg.Mentions, e2.ID, e1.ID, e2.ID)
	}
	// @engine hits the unsuffixed member, not engine-2.
	msg2, err := s.Post("general", "hey @engine", human.ID, "", KindHuman, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msg2.Mentions) != 1 || msg2.Mentions[0] != e1.ID {
		t.Fatalf("mentions=%v want [%s]", msg2.Mentions, e1.ID)
	}
}

func TestNormalizeRole(t *testing.T) {
	r, err := NormalizeRole("ENGINE")
	if err != nil || r != RoleEngine {
		t.Fatalf("got %q %v", r, err)
	}
	if _, err := NormalizeRole("wizard"); err == nil {
		t.Fatal("expected error")
	}
	r, err = NormalizeRole("none")
	if err != nil || r != RoleNone {
		t.Fatalf("none: %q %v", r, err)
	}
}

func TestLeaveDoesNotPostSystemLine(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	m, err := s.Join("bot", KindAgent, "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Leave(m.ID, ""); err != nil {
		t.Fatal(err)
	}
	hist, err := s.History("general", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range hist {
		if msg.Kind == "system" {
			t.Fatalf("leave posted system line: %+v", msg)
		}
	}
}

func TestJoinEmptyDoesNotMergeIntoSessionedName(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	existing, err := s.Join("engine", KindAgent, "sess-already")
	if err != nil {
		t.Fatal(err)
	}
	newbie, err := s.Join("engine", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	if existing.ID == newbie.ID {
		t.Fatal("empty session_id must not merge into a sessioned same-name member")
	}
	if existing.Name != "engine" || newbie.Name != "engine-2" {
		t.Fatalf("names %q %q", existing.Name, newbie.Name)
	}
	list, _ := s.Members()
	if len(list) != 2 {
		t.Fatalf("members=%d", len(list))
	}
}

func TestPostWithoutJoinNeverMergesOnName(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	existing, err := s.Join("engine", KindAgent, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.Post("general", "drive-by", "", "engine", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	if msg.FromID == existing.ID {
		t.Fatal("post-without-join fused onto existing name")
	}
	if msg.FromName != "engine-2" {
		t.Fatalf("auto-join name=%q want engine-2", msg.FromName)
	}
	list, _ := s.Members()
	if len(list) != 2 {
		t.Fatalf("members=%d want 2", len(list))
	}
}

func TestJoinReturnsSessionAndMemberID(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	r := Apply(s, Request{Op: OpJoin, Name: "bot", Kind: "agent", SessionID: "sess-x"})
	if !r.OK || r.Member == nil {
		t.Fatalf("join: %+v", r)
	}
	if r.MemberID != r.Member.ID || r.MemberID == "" {
		t.Fatalf("member_id=%q member.id=%q", r.MemberID, r.Member.ID)
	}
	if r.SessionID != "sess-x" {
		t.Fatalf("session_id=%q", r.SessionID)
	}
}
