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

func TestRainOpacityNudge(t *testing.T) {
	st := newSettingsState(config.Default())
	// Move to opacity field
	for st.field != fieldRainOpacity {
		st.moveField(1)
		if int(st.field) == 0 {
			t.Fatal("opacity field missing")
		}
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
