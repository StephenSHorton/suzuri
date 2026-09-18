//go:build windows || darwin

package ui

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// absForkBin is an OS-absolute grok binary path. filepath.IsAbs("/usr/bin/x")
// is false on Windows (needs a volume), so Unix fixtures cannot exercise the
// allowlist there.
func absForkBin(name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(`C:\Users\test\bin`, name+".exe")
	}
	return "/usr/bin/" + name
}

func TestParseForkOSCBasic(t *testing.T) {
	req, ok := parseForkOSCPayload([]byte("7880;fork=1;resume=abc-123;bin=/usr/bin/grok-fork"))
	if !ok {
		t.Fatal("parse")
	}
	if req.resume != "abc-123" || req.bin != "/usr/bin/grok-fork" || req.newSession {
		t.Fatalf("%+v", req)
	}
}

func TestParseForkOSCPercentPrompt(t *testing.T) {
	req, ok := parseForkOSCPayload([]byte("7880;fork=1;resume=s;bin=/usr/bin/grok;prompt=try%20the%20async;brand=fork"))
	if !ok {
		t.Fatal("parse")
	}
	if req.prompt != "try the async" || req.brand != "fork" {
		t.Fatalf("%+v", req)
	}
}

func TestParseForkOSCRequiresResumeAndBin(t *testing.T) {
	if _, ok := parseForkOSCPayload([]byte("7880;fork=1;bin=/usr/bin/grok")); ok {
		t.Fatal("expected fail")
	}
	if _, ok := parseForkOSCPayload([]byte("7880;fork=1;resume=x")); ok {
		t.Fatal("expected fail")
	}
}

func TestParseForkOSCNewSession(t *testing.T) {
	req, ok := parseForkOSCPayload([]byte("7880;new=1;session=abc-123;bin=/usr/bin/grok-fork;title=review"))
	if !ok || !req.newSession || req.resume != "abc-123" || req.title != "review" {
		t.Fatalf("%+v ok=%v", req, ok)
	}
}

func TestAllowedForkBin(t *testing.T) {
	if !allowedForkBin(absForkBin("grok-fork")) {
		t.Fatal("grok-fork")
	}
	if !allowedForkBin(absForkBin("xai-grok-pager")) {
		t.Fatal("pager")
	}
	if allowedForkBin("grok-fork") || allowedForkBin(absForkBin("zsh")) || allowedForkBin("/usr/bin/../bin/grok") {
		t.Fatal("allowlist too open")
	}
}

func TestStripAndTakeFork(t *testing.T) {
	raw := []byte("hi\x1b]7880;fork=1;resume=s;bin=/usr/bin/grok\x07there")
	clean, reqs := stripAndTakeFork(raw)
	if string(clean) != "hithere" {
		t.Fatalf("clean=%q", clean)
	}
	if len(reqs) != 1 || reqs[0].resume != "s" {
		t.Fatalf("%+v", reqs)
	}
}

func TestForkLaunchSpec(t *testing.T) {
	want := absForkBin("grok-fork")
	bin, args, env, err := forkLaunchSpec(forkPaneRequest{
		bin: want, resume: "abc", newSession: true, brand: "fork",
	})
	if err != "" || bin != want {
		t.Fatalf("err=%s bin=%s", err, bin)
	}
	if len(args) < 2 || args[0] != "--session-id" || args[1] != "abc" {
		t.Fatalf("args=%v", args)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GROK_FORK=1") {
		t.Fatalf("env=%v", env)
	}
	if strings.Contains(joined, "GROK_SKIP_") || strings.Contains(joined, "GROK_DISABLE_AUTOUPDATER") {
		t.Fatalf("launcher forbids skip env: %v", env)
	}
}
