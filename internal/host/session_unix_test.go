//go:build unix

package host

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestQuietZshHidesUserHostPrompt(t *testing.T) {
	s, err := StartSession("", 80, 24, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	time.Sleep(500 * time.Millisecond)
	buf := make([]byte, 8192)
	n, _ := s.Read(buf)
	out := string(buf[:n])
	t.Logf("pty out (%d): %q", n, out)
	// Should not contain typical user@host prompt pieces from the machine.
	if strings.Contains(out, "@") && strings.Contains(out, "%") {
		// Allow if it's buried in OSC/title; flag obvious prompt lines.
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "@") && strings.Contains(line, "%") &&
				!strings.Contains(line, "\x1b") {
				t.Fatalf("looks like a visible zsh prompt: %q", line)
			}
		}
	}
}

func TestTIOCGPGRPSeesRunningCommand(t *testing.T) {
	// Pin zsh. The default shell on a CI runner is often bash, which never
	// emits the bracketed-paste sequence this used to wait on.
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	s, err := StartSession(zsh, 80, 24, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mu sync.Mutex
	var acc []byte
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := s.Read(buf)
			if n > 0 {
				mu.Lock()
				acc = append(acc, buf[:n]...)
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	// 7878 is the quiet-prompt cwd mark, emitted after the rc files finish.
	// Bracketed paste is a zsh default on this machine and absent on others.
	deadline := time.Now().Add(12 * time.Second)
	gotPrompt := false
	for time.Now().Before(deadline) {
		mu.Lock()
		gotPrompt = strings.Contains(string(acc), "7878;cwd=")
		mu.Unlock()
		if gotPrompt {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !gotPrompt {
		t.Fatal("shell never reached a prompt")
	}
	if _, err := s.Write([]byte("/bin/sleep 30\r")); err != nil {
		t.Fatal(err)
	}
	shell := s.Pid()
	saw := 0
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		pg := s.ForegroundPGID()
		if pg > 0 && pg != shell {
			saw = pg
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if saw == 0 {
		mu.Lock()
		snap := string(acc)
		mu.Unlock()
		var oscs []string
		for _, part := range strings.Split(snap, "\x1b]") {
			if i := strings.IndexByte(part, '\a'); i >= 0 {
				part = part[:i]
			}
			if strings.HasPrefix(part, "7879;") {
				oscs = append(oscs, part)
			}
		}
		out, _ := exec.Command("ps", "-ax", "-o", "pid=,ppid=,pgid=,stat=,command=").Output()
		var rel []string
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && (f[0] == fmt.Sprint(shell) || f[1] == fmt.Sprint(shell)) {
				rel = append(rel, strings.TrimSpace(line))
			}
		}
		t.Fatalf("foreground pgid=%d shell=%d oscs=%q\n%s", s.ForegroundPGID(), shell, oscs, strings.Join(rel, "\n"))
	}
	_ = syscall.Kill(-saw, syscall.SIGKILL)
}

func TestQuietShellReportsCommandDone(t *testing.T) {
	env, zdot, err := quietShellEnv("/bin/zsh", nil, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(zdot)
	if getenv(env, "ZDOTDIR") != zdot {
		t.Fatalf("ZDOTDIR %q", getenv(env, "ZDOTDIR"))
	}
	b, err := os.ReadFile(filepath.Join(zdot, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "7879;done;") || !strings.Contains(string(b), "7879;start") || !strings.Contains(string(b), "preexec_functions") {
		t.Fatalf("zsh hook missing: %s", b)
	}

	env, dir, err := quietShellEnv("/bin/bash", nil, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	rc := getenv(env, "SUZURI_BASHRC")
	b, err = os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "7879;done;") || !strings.Contains(string(b), "7879;start") || !strings.Contains(string(b), "trap _suzuri_debug DEBUG") {
		t.Fatalf("bash hook missing: %s", b)
	}
	if !strings.Contains(string(b), `_suzuri_ec=$?; _suzuri_prompt`) {
		t.Fatalf("bash prompt must capture $? first: %s", b)
	}
}
