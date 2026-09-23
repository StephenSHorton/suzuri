//go:build unix

package host

import (
	"os"
	"path/filepath"
	"strings"
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
	if !strings.Contains(string(b), "7879;done;") || !strings.Contains(string(b), "preexec_functions") {
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
	if !strings.Contains(string(b), "7879;done;") || !strings.Contains(string(b), "trap _suzuri_debug DEBUG") {
		t.Fatalf("bash hook missing: %s", b)
	}
	if !strings.Contains(string(b), `_suzuri_ec=$?; _suzuri_prompt`) {
		t.Fatalf("bash prompt must capture $? first: %s", b)
	}
}
