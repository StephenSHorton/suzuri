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
	for st.field != fieldNotice {
		st.moveField(1)
		if int(st.field) == 0 {
			t.Fatal("notice field missing")
		}
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
