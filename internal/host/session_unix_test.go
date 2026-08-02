//go:build unix

package host

import (
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
