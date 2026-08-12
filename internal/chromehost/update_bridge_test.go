package chromehost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseUpdateReq(t *testing.T) {
	if parseUpdateReq("check") != "check" {
		t.Fatal("check")
	}
	if parseUpdateReq("  install  ") != "install" {
		t.Fatal("install")
	}
	if parseUpdateReq("later") != "later" {
		t.Fatal("later")
	}
	if parseUpdateReq("nope") != "" {
		t.Fatal("unknown")
	}
	if parseUpdateReq("") != "" {
		t.Fatal("empty")
	}
}

func TestWriteUpdateEvt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	writeUpdateEvt("toast hello")
	body, err := os.ReadFile(updateEvtPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "toast hello" {
		t.Fatalf("body=%q", body)
	}
	writeUpdateEvt("toast update available: v1.2.3\noffer 1.2.3")
	body, err = os.ReadFile(updateEvtPath())
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(body))
	if !strings.Contains(got, "offer 1.2.3") || !strings.Contains(got, "toast update available") {
		t.Fatalf("got %q", got)
	}
}

func TestApplyGate(t *testing.T) {
	g := newApplyGate()
	if g.Applying() {
		t.Fatal("fresh gate should be idle")
	}
	g.Begin()
	if !g.Applying() {
		t.Fatal("Begin should mark applying")
	}
	done := make(chan struct{})
	go func() {
		g.Wait(2 * time.Second)
		close(done)
	}()
	g.Finish()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after Finish")
	}
}

func TestNilServiceToastsStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	b := newUpdateBridge(nil, newApplyGate())
	b.runCheck(false)
	body, err := os.ReadFile(updateEvtPath())
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(body))
	if got != "toast updates via Microsoft Store" {
		t.Fatalf("got %q", got)
	}
}

func TestUpdatePathsUseConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	if updateReqPath() != filepath.Join(dir, "suzuri", UpdateReqFile) {
		t.Fatalf("req=%s", updateReqPath())
	}
	if updateEvtPath() != filepath.Join(dir, "suzuri", UpdateEvtFile) {
		t.Fatalf("evt=%s", updateEvtPath())
	}
}
