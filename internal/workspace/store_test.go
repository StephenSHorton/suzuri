package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeChannel(t *testing.T) {
	cases := map[string]string{
		"#General":   "general",
		"Fix Auth":   "fix-auth",
		"  PR_142  ": "pr-142",
		"":           "",
		"###":        "",
	}
	for in, want := range cases {
		if got := NormalizeChannel(in); got != want {
			t.Errorf("NormalizeChannel(%q)=%q want %q", in, got, want)
		}
	}
}

func TestPostHistoryJoin(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "workspace"))

	m, err := s.Join("implementer", KindAgent, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "implementer" || m.Kind != KindAgent {
		t.Fatalf("member: %+v", m)
	}

	msg, err := s.Post("general", "hello from agent", m.ID, "", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Body != "hello from agent" || msg.FromName != "implementer" {
		t.Fatalf("msg: %+v", msg)
	}

	// Human post by name (auto-join).
	_, err = s.Post("#general", "looking good", "", "stephen", KindHuman, "")
	if err != nil {
		t.Fatal(err)
	}

	hist, err := s.History("general", 50)
	if err != nil {
		t.Fatal(err)
	}
	// join system + agent + human
	if len(hist) < 3 {
		t.Fatalf("history len=%d want >=3: %+v", len(hist), hist)
	}
	last := hist[len(hist)-1]
	if last.Body != "looking good" || last.FromKind != KindHuman {
		t.Fatalf("last: %+v", last)
	}

	chs, err := s.ListChannels()
	if err != nil || len(chs) < 1 {
		t.Fatalf("channels: %v %v", chs, err)
	}
	ch, err := s.CreateChannel("pr-142", "auth fix")
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID != "pr-142" {
		t.Fatalf("channel: %+v", ch)
	}
	_, err = s.Post("pr-142", "opened", m.ID, "", KindAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := s.History("pr-142", 10)
	if err != nil || len(h2) != 1 {
		t.Fatalf("pr history: %v %v", h2, err)
	}
}

func TestApplyOps(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	r := Apply(s, Request{Op: OpJoin, Name: "bot", Kind: "agent", SessionID: "x"})
	if !r.OK || r.Member == nil {
		t.Fatalf("join: %+v", r)
	}
	r = Apply(s, Request{Op: OpPost, Channel: "general", Body: "hi", MemberID: r.Member.ID})
	if !r.OK || r.Message == nil {
		t.Fatalf("post: %+v", r)
	}
	r = Apply(s, Request{Op: OpHistory, Channel: "general", Limit: 10})
	if !r.OK || r.Count < 1 {
		t.Fatalf("history: %+v", r)
	}
	r = Apply(s, Request{Op: OpStatus})
	if !r.OK || r.Status == nil {
		t.Fatalf("status: %+v", r)
	}
}

func TestUploadDownload(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	src := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(src, []byte("hello workspace"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := s.Join("bot", KindAgent, "u1")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.Upload("general", src, m.ID, "", KindAgent, "see this")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Kind != "file" || msg.File == nil || msg.File.Bytes != 15 {
		t.Fatalf("msg: %+v", msg)
	}
	abs, ref, err := s.ResolveFile("general", msg.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "hello.txt" {
		t.Fatalf("ref: %+v", ref)
	}
	b, err := os.ReadFile(abs)
	if err != nil || string(b) != "hello workspace" {
		t.Fatalf("read %s: %q %v", abs, b, err)
	}
	// download op
	r := Apply(s, Request{Op: OpDownload, Channel: "general", FileID: msg.File.ID})
	if !r.OK || r.LocalPath == "" {
		t.Fatalf("download: %+v", r)
	}
}

func TestChannelCreateAndUploadOp(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	r := Apply(s, Request{Op: OpChannelCreate, Channel: "pr-99", Topic: "ship it"})
	if !r.OK || r.Channel == nil || r.Channel.ID != "pr-99" {
		t.Fatalf("create: %+v", r)
	}
	src := filepath.Join(dir, "patch.diff")
	_ = os.WriteFile(src, []byte("diff"), 0o644)
	r = Apply(s, Request{
		Op: OpUpload, Channel: "pr-99", FilePath: src,
		Name: "reviewer", Kind: "agent", Body: "patch",
	})
	if !r.OK || r.Message == nil || r.Message.File == nil {
		t.Fatalf("upload: %+v", r)
	}
}

func TestDeleteChannelCleansHistory(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	_, err := s.CreateChannel("temp-room", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Post("temp-room", "bye soon", "", "alice", KindHuman, "")
	if err != nil {
		t.Fatal(err)
	}
	h, _ := s.History("temp-room", 10)
	if len(h) < 1 {
		t.Fatal("expected history")
	}
	if _, err := s.DeleteChannel("temp-room"); err != nil {
		t.Fatal(err)
	}
	// Directory gone
	chs, _ := s.ListChannels()
	for _, c := range chs {
		if c.ID == "temp-room" {
			t.Fatal("channel still listed")
		}
	}
	// Cannot delete general
	if _, err := s.DeleteChannel("general"); err == nil {
		t.Fatal("expected error deleting general")
	}
}
