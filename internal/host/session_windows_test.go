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

func TestSessionEnvBrandsWhenAbsent(t *testing.T) {
	// Bare process env without graphics branding.
	base := []string{
		"PATH=C:\\Windows\\System32",
		"USERNAME=test",
	}
	got := sessionEnv(base)
	if v := getenvEnv(got, "TERM_PROGRAM"); v != "ghostty" {
		t.Fatalf("TERM_PROGRAM: got %q want ghostty", v)
	}
	if v := getenvEnv(got, "KITTY_WINDOW_ID"); v != "1" {
		t.Fatalf("KITTY_WINDOW_ID: got %q want 1", v)
	}
	if v := getenvEnv(got, "COLORTERM"); v != "truecolor" {
		t.Fatalf("COLORTERM: got %q want truecolor", v)
	}
	if v := getenvEnv(got, "TERM"); v != "xterm-256color" {
		t.Fatalf("TERM: got %q want xterm-256color", v)
	}
	if v := getenvEnv(got, "TERM_PROGRAM_VERSION"); v != "1.0.0" {
		t.Fatalf("TERM_PROGRAM_VERSION: got %q want 1.0.0", v)
	}
	// Base keys preserved.
	if v := getenvEnv(got, "PATH"); !strings.Contains(v, "System32") {
		t.Fatalf("PATH lost: %q", v)
	}
}

func TestSessionEnvDoesNotClobberUserBrand(t *testing.T) {
	base := []string{
		"TERM_PROGRAM=wezterm",
		"KITTY_WINDOW_ID=42",
		"COLORTERM=24bit",
		"TERM=xterm-kitty",
	}
	got := sessionEnv(base)
	if v := getenvEnv(got, "TERM_PROGRAM"); v != "wezterm" {
		t.Fatalf("clobbered TERM_PROGRAM: %q", v)
	}
	if v := getenvEnv(got, "KITTY_WINDOW_ID"); v != "42" {
		t.Fatalf("clobbered KITTY_WINDOW_ID: %q", v)
	}
	if v := getenvEnv(got, "COLORTERM"); v != "24bit" {
		t.Fatalf("clobbered COLORTERM: %q", v)
	}
	if v := getenvEnv(got, "TERM"); v != "xterm-kitty" {
		t.Fatalf("clobbered TERM: %q", v)
	}
}

func TestSessionEnvExtraOverridesBase(t *testing.T) {
	base := []string{"TERM_PROGRAM=old"}
	got := sessionEnv(base, "SUZURI_TAB_ID=3", "TERM_PROGRAM=custom")
	if v := getenvEnv(got, "TERM_PROGRAM"); v != "custom" {
		t.Fatalf("extra should win: %q", v)
	}
	if v := getenvEnv(got, "SUZURI_TAB_ID"); v != "3" {
		t.Fatalf("extra env missing: %q", v)
	}
	// Still brand missing keys.
	if v := getenvEnv(got, "KITTY_WINDOW_ID"); v != "1" {
		t.Fatalf("KITTY_WINDOW_ID: got %q", v)
	}
}

func TestSetEnvIfEmptyFillsBlank(t *testing.T) {
	env := []string{"TERM_PROGRAM="}
	got := setEnvIfEmpty(env, "TERM_PROGRAM", "ghostty")
	if v := getenvEnv(got, "TERM_PROGRAM"); v != "ghostty" {
		t.Fatalf("blank should fill: %q", v)
	}
}
