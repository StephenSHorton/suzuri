//go:build windows

package host

import (
	"strings"
	"testing"
)

func TestQuietPromptPowerShellUsesSpacePrompt(t *testing.T) {
	in := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe -NoLogo -NoProfile`
	got := QuietPrompt(in)
	// Visual prompt is a single space; also emit OSC cwd for the host bar.
	if !strings.Contains(got, "function global:prompt") {
		t.Fatalf("expected prompt function, got %q", got)
	}
	if !strings.Contains(got, "7878;cwd=") {
		t.Fatalf("expected cwd OSC, got %q", got)
	}
	if !strings.Contains(got, "Clear-Host") {
		t.Fatalf("expected Clear-Host, got %q", got)
	}
	// Empty '' prompt falls back to PS> on WinPS — must not be used.
	if strings.Contains(got, "{ '' }") {
		t.Fatalf("empty prompt regresses to PS>, got %q", got)
	}
}

func TestQuietPromptLeavesCustomCommandAlone(t *testing.T) {
	in := `powershell.exe -NoLogo -Command "Get-Date"`
	if got := QuietPrompt(in); got != in {
		t.Fatalf("custom -Command should pass through, got %q", got)
	}
}

func TestQuietPromptCmd(t *testing.T) {
	got := QuietPrompt(`C:\Windows\System32\cmd.exe`)
	low := strings.ToLower(got)
	if !strings.Contains(low, `/k prompt`) {
		t.Fatalf("cmd quiet prompt: %q", got)
	}
	if !strings.Contains(got, "7878;cwd=") {
		t.Fatalf("expected cwd OSC in cmd prompt: %q", got)
	}
}
