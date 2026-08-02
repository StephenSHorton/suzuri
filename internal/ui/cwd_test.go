//go:build windows || darwin

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripAndTakeCwdOSC7878(t *testing.T) {
	raw := []byte("hello\x1b]7878;cwd=C:\\Users\\test\x07world")
	clean, cwd, ok := stripAndTakeCwd(raw)
	if !ok || cwd != `C:\Users\test` {
		t.Fatalf("cwd=%q ok=%v", cwd, ok)
	}
	if string(clean) != "helloworld" {
		t.Fatalf("clean=%q", clean)
	}
}

func TestStripAndTakeCwdOSC7(t *testing.T) {
	raw := []byte("\x1b]7;file:///C:/Users/foo\x07x")
	clean, cwd, ok := stripAndTakeCwd(raw)
	if !ok {
		t.Fatal("expected cwd")
	}
	if !strings.EqualFold(cwd, `C:\Users\foo`) {
		t.Fatalf("cwd=%q", cwd)
	}
	if string(clean) != "x" {
		t.Fatalf("clean=%q", clean)
	}
}

func TestExpandBarSubmitBareCD(t *testing.T) {
	disp, pay := expandBarSubmit("cd", `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe -NoLogo`)
	if disp != "cd ~" {
		t.Fatalf("display=%q", disp)
	}
	if pay != "Set-Location ~" {
		t.Fatalf("payload=%q", pay)
	}
	disp, pay = expandBarSubmit("cd", "cmd.exe")
	if disp != "cd ~" || !strings.Contains(pay, "USERPROFILE") {
		t.Fatalf("cmd disp=%q pay=%q", disp, pay)
	}
	// With args: leave alone.
	disp, pay = expandBarSubmit("cd src", "powershell.exe")
	if disp != "cd src" || pay != "cd src" {
		t.Fatalf("args should pass through: %q %q", disp, pay)
	}
}

func TestCwdAfterCommand(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home")
	}
	got, ok := cwdAfterCommand(`C:\tmp`, "Set-Location ~")
	if !ok || !strings.EqualFold(filepath.Clean(got), filepath.Clean(home)) {
		t.Fatalf("got=%q ok=%v home=%q", got, ok, home)
	}
	got, ok = cwdAfterCommand(`C:\Users\x`, `cd Documents`)
	if !ok || !strings.HasSuffix(strings.ToLower(got), "documents") {
		t.Fatalf("rel cd: %q", got)
	}
}

func TestPushBlockIncludesCwd(t *testing.T) {
	s := newScrollback()
	s.pushBlock("echo hi", 40, `C:\Users\test\project`)
	var found bool
	for _, hl := range s.lines {
		// Path and command share one primary-colored line.
		if hl.kind == histBlockCmd && strings.Contains(hl.text, "echo hi") &&
			(strings.Contains(hl.text, "Users") || strings.Contains(hl.text, "test") || strings.Contains(hl.text, "~")) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected path+cmd on same histBlockCmd line: %+v", s.lines)
	}
}

func TestDisplayPathHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home")
	}
	if displayPath(home) != "~" {
		t.Fatalf("home -> %q", displayPath(home))
	}
}
