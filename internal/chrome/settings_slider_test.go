package chrome

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/suzuri/internal/config"
)

func TestFormatRainOpacitySlider(t *testing.T) {
	s := formatRainOpacitySlider(0, 10)
	if !strings.Contains(s, "0%") || strings.Contains(s, "█") {
		t.Fatalf("empty bar: %q", s)
	}
	s = formatRainOpacitySlider(100, 10)
	if !strings.Contains(s, "100%") || strings.Count(s, "█") != 10 {
		t.Fatalf("full bar: %q", s)
	}
	s = formatRainOpacitySlider(50, 10)
	if !strings.Contains(s, "50%") {
		t.Fatalf("mid: %q", s)
	}
}

func TestNoticePositionCycle(t *testing.T) {
	st := newSettingsState(config.Default())
	st.moveTab(1) // Shell
	st.moveTab(1) // Session
	if st.field != fieldNotice {
		t.Fatalf("session opens on %v", st.field)
	}
	if st.valueLabel(fieldNotice) != "Bottom left" {
		t.Fatalf("label %q", st.valueLabel(fieldNotice))
	}
	ids := config.NoticePositionIDs()
	for i := 0; i < len(ids); i++ {
		if st.edit.NoticePosition != ids[i] {
			t.Fatalf("step %d got %q want %q", i, st.edit.NoticePosition, ids[i])
		}
		st.nudge(1)
	}
	if st.edit.NoticePosition != config.NoticeBottomLeft {
		t.Fatalf("wrap %q", st.edit.NoticePosition)
	}
	view := st.render(100)
	if !strings.Contains(view, "Notices") || !strings.Contains(view, "Bottom left") {
		t.Fatalf("settings view missing notice row:\n%s", view)
	}
	_, paras := st.helpContent()
	if len(paras) == 0 || !strings.Contains(strings.Join(paras, " "), "center") {
		t.Fatalf("help %v", paras)
	}
}

func TestRainOpacityNudge(t *testing.T) {
	st := newSettingsState(config.Default())
	st.moveTab(1) // Shell: Intro, Ambient, Intensity
	st.moveField(1)
	st.moveField(1)
	if st.field != fieldRainOpacity {
		t.Fatalf("field %v", st.field)
	}
	start := st.edit.ShellMatrixOpacity
	st.nudge(1)
	if st.edit.ShellMatrixOpacity != start+rainOpacityStep && st.edit.ShellMatrixOpacity != 100 {
		// may clamp at 100
		if start < 100 && st.edit.ShellMatrixOpacity != start+rainOpacityStep {
			t.Fatalf("nudge+ got %d from %d", st.edit.ShellMatrixOpacity, start)
		}
	}
	st.edit.ShellMatrixOpacity = 10
	st.nudge(-1)
	if st.edit.ShellMatrixOpacity != 5 {
		t.Fatalf("nudge- got %d", st.edit.ShellMatrixOpacity)
	}
	st.edit.ShellMatrixOpacity = 3
	st.nudge(-1)
	if st.edit.ShellMatrixOpacity != 0 {
		t.Fatalf("clamp low got %d", st.edit.ShellMatrixOpacity)
	}
	val := st.valueLabel(fieldRainOpacity)
	if !strings.Contains(val, "%") {
		t.Fatalf("value label %q", val)
	}
}

func TestSettingsTabsAndGlassSliders(t *testing.T) {
	st := newSettingsState(config.Default())
	view := st.render(100)
	if !strings.Contains(view, "Look") || !strings.Contains(view, "Font") {
		t.Fatalf("look tab:\n%s", view)
	}
	if strings.Contains(view, "Notices") || strings.Contains(view, "Blur") {
		t.Fatalf("other pages leaked onto Look:\n%s", view)
	}
	if !strings.Contains(view, "tab") || !strings.Contains(view, "esc") {
		t.Fatalf("footer missing hints:\n%s", view)
	}

	st.moveTab(1)
	if st.edit.GlassBlur != config.GlassBlurDefault || st.edit.GlassRim != config.GlassRimDefault {
		t.Fatalf("defaults blur %d rim %d", st.edit.GlassBlur, st.edit.GlassRim)
	}
	for st.field != fieldGlassBlur {
		st.moveField(1)
	}
	st.nudge(1)
	if st.edit.GlassBlur != config.GlassBlurDefault+glassBlurStep {
		t.Fatalf("blur nudge %d", st.edit.GlassBlur)
	}
	st.edit.GlassBlur = 2
	st.nudge(-1)
	if st.edit.GlassBlur != 0 {
		t.Fatalf("blur clamp %d", st.edit.GlassBlur)
	}
	st.edit.GlassBlur = config.GlassBlurMax
	st.nudge(1)
	if st.edit.GlassBlur != config.GlassBlurMax {
		t.Fatalf("blur max %d", st.edit.GlassBlur)
	}
	for st.field != fieldGlassVeil {
		st.moveField(1)
	}
	st.nudge(1)
	if st.edit.GlassVeil != rainOpacityStep {
		t.Fatalf("veil %d", st.edit.GlassVeil)
	}
	view = st.render(100)
	if !strings.Contains(view, "Veil") || !strings.Contains(view, "Rim") {
		t.Fatalf("shell tab:\n%s", view)
	}
	if strings.Contains(view, "Font") {
		t.Fatalf("look row on shell tab:\n%s", view)
	}
}

func TestWindowsHostBackdropDisablesBlurSlider(t *testing.T) {
	config.TestingSetGlassHost(t, config.GlassHost{GOOS: "windows", WinBuild: 26100})
	st := newSettingsState(config.Normalize(config.Config{
		Backdrop:  config.BackdropGlass,
		GlassBlur: 36,
		GlassVeil: 0,
	}))
	st.moveTab(1)
	for st.field != fieldGlassBlur {
		st.moveField(1)
	}
	if !st.fieldIdle(fieldGlassBlur) {
		t.Fatal("Win11 HostBackdrop blur must be idle")
	}
	before := st.edit.GlassBlur
	st.nudge(1)
	if st.edit.GlassBlur != before {
		t.Fatalf("idle blur nudged %d → %d", before, st.edit.GlassBlur)
	}
	if st.valueLabel(fieldGlassBlur) != config.GlassBlurIdleHint {
		t.Fatalf("blur value %q", st.valueLabel(fieldGlassBlur))
	}
	if !st.fieldIdle(fieldGlassRim) {
		t.Fatal("rim is a no-op on Windows")
	}
	if st.valueLabel(fieldGlassRim) != config.GlassRimIdleHint {
		t.Fatalf("rim value %q", st.valueLabel(fieldGlassRim))
	}
	view := st.render(100)
	if !strings.Contains(view, config.GlassBlurIdleHint) {
		t.Fatalf("hint missing:\n%s", view)
	}
	_, paras := st.helpContent()
	if len(paras) == 0 || !strings.Contains(paras[0], "Windows Desktop Acrylic") {
		t.Fatalf("help %v", paras)
	}

	st.edit.GlassVeil = 40
	st.edit = config.Normalize(st.edit)
	if st.fieldIdle(fieldGlassBlur) {
		t.Fatal("veil>0 must enable blur (accent-acrylic)")
	}
	st.nudge(1)
	if st.edit.GlassBlur != before+glassBlurStep {
		t.Fatalf("live blur %d", st.edit.GlassBlur)
	}
}

func TestMacGlassSlidersStayLive(t *testing.T) {
	config.TestingSetGlassHost(t, config.GlassHost{GOOS: "darwin"})
	st := newSettingsState(config.Normalize(config.Config{
		Backdrop:  config.BackdropGlass,
		GlassBlur: 36,
		GlassVeil: 0,
		GlassRim:  25,
	}))
	if st.fieldIdle(fieldGlassBlur) || st.fieldIdle(fieldGlassRim) {
		t.Fatal("macOS glass sliders must stay live")
	}
}
