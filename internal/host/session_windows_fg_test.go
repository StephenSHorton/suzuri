//go:build windows

package host

import (
	"io"
	"testing"
	"time"
)

func TestProgramOwnsConsoleKeys(t *testing.T) {
	const shell = 100
	cases := []struct {
		name  string
		pids  []uint32
		shell int
		want  bool
	}{
		{name: "shell only", pids: []uint32{shell}, shell: shell, want: false},
		{name: "shell and child", pids: []uint32{shell, 200}, shell: shell, want: true},
		{name: "child without shell in list", pids: []uint32{200}, shell: shell, want: true},
		{name: "grandchild id", pids: []uint32{200, 201}, shell: shell, want: true},
		{name: "zeros ignored", pids: []uint32{0, shell, 0}, shell: shell, want: false},
		{name: "empty", pids: nil, shell: shell, want: false},
		{name: "unknown shell", pids: []uint32{200}, shell: 0, want: false},
		{name: "negative shell", pids: []uint32{200}, shell: -1, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := programOwnsConsoleKeys(tc.pids, tc.shell)
			if got != tc.want {
				t.Fatalf("programOwnsConsoleKeys(%v, %d)=%v want %v", tc.pids, tc.shell, got, tc.want)
			}
		})
	}
}

// childOwnsTTY keeps the keyboard when ForegroundPGID is positive and not the shell.
func TestWindowsForegroundIDHandsKeysToChild(t *testing.T) {
	const shell = 35420
	if pg := windowsForegroundID(shell, false); pg != 0 {
		t.Fatalf("shell-only pgid=%d", pg)
	}
	pg := windowsForegroundID(shell, programOwnsConsoleKeys([]uint32{uint32(shell), 15012}, shell))
	if pg <= 0 || pg == shell {
		t.Fatalf("child pgid=%d shell=%d", pg, shell)
	}
	again := windowsForegroundID(shell, true)
	if again != pg {
		t.Fatalf("sentinel changed %d -> %d", pg, again)
	}
	// Sentinel must stay distinct even if it equals the shell pid.
	pg = windowsForegroundID(windowsChildForeground, true)
	if pg <= 0 || pg == windowsChildForeground {
		t.Fatalf("colliding shell pgid=%d", pg)
	}
	if windowsForegroundID(0, true) != 0 {
		t.Fatal("missing shell should not report a child")
	}
}

func TestDescendantPIDsFrom(t *testing.T) {
	all := []procLink{
		{pid: 10, ppid: 1},
		{pid: 11, ppid: 10},
		{pid: 12, ppid: 11},
		{pid: 20, ppid: 1},
		{pid: 0, ppid: 10},
		{pid: 11, ppid: 10}, // duplicate edge must not loop
	}
	got := descendantPIDsFrom(all, 10)
	if len(got) != 2 || got[0] != 11 || got[1] != 12 {
		t.Fatalf("descendants=%v", got)
	}
	if programOwnsConsoleKeys(got, 10) != true {
		t.Fatal("descendant should take keys")
	}
	if descendantPIDsFrom(all, 20) != nil {
		t.Fatal("leaf shell has no child")
	}
	if programOwnsConsoleKeys(descendantPIDsFrom(all, 20), 20) {
		t.Fatal("shell alone should keep keys")
	}
	if descendantPIDsFrom(all, 0) != nil {
		t.Fatal("shell 0")
	}
}

func TestForegroundPGIDWithoutSession(t *testing.T) {
	var s *Session
	if s.ForegroundPGID() != 0 {
		t.Fatal("nil session")
	}
	s = &Session{}
	if s.ForegroundPGID() != 0 {
		t.Fatal("no conpty")
	}
}

// The UI calls ForegroundPGID from paint and key paths. It must return
// without touching the pseudoconsole and later notice a running child.
func TestForegroundPGIDNeverBlocksAndSeesChild(t *testing.T) {
	s, err := StartSession(`C:\Windows\System32\cmd.exe /k ping -n 30 127.0.0.1`, 80, 24, "")
	if err != nil {
		t.Skipf("conpty unavailable: %v", err)
	}
	defer s.Close()
	go func() { _ = s.CopyOutput(io.Discard) }()

	start := time.Now()
	for range 1000 {
		s.ForegroundPGID()
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("1000 calls took %v; the probe must not run inline", d)
	}

	deadline := time.Now().Add(3 * time.Second)
	for s.ForegroundPGID() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("child ping never reported")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pg := s.ForegroundPGID(); pg == s.Pid() {
		t.Fatalf("sentinel %d equals shell pid", pg)
	}

	_ = s.Close()
	deadline = time.Now().Add(time.Second)
	for s.ForegroundPGID() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("probe still reporting after Close")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
