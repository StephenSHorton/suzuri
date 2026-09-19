package ui

import (
	"bytes"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestRewriteSGRKeepsCombinedFgBg(t *testing.T) {
	var s sgrState
	in := []byte("\x1b[38;2;192;202;245;48;2;26;27;38mX")
	got := s.rewrite(in)
	if s.faint {
		t.Fatalf("48;2 must not set faint: %q", got)
	}
	if !bytes.Contains(got, []byte("38;2;192;202;245")) {
		t.Fatalf("lost fg: %q", got)
	}
	if !bytes.Contains(got, []byte("48;2;26;27;38")) {
		t.Fatalf("lost bg (white-wash): %q", got)
	}
	term := vt10x.New(vt10x.WithSize(8, 2))
	if _, err := term.Write(got); err != nil {
		t.Fatal(err)
	}
	c := glyphToCell(term.Cell(0, 0))
	if c.BR < 20 || c.BR > 40 || c.FR < 170 {
		t.Fatalf("expected tokyonight-ish cell FR=%d,%d,%d BR=%d,%d,%d seq=%q",
			c.FR, c.FG, c.FB, c.BR, c.BG, c.BB, got)
	}
}

func TestRewriteSGRColonTruecolor(t *testing.T) {
	var s sgrState
	got := s.rewrite([]byte("\x1b[38:2:40:50:60mX"))
	if !bytes.Contains(got, []byte("\x1b[38;2;40;50;60m")) {
		t.Fatalf("colon truecolor not normalized: %q", got)
	}
	got = s.rewrite([]byte("\x1b[38:2::10:20:30mY"))
	if !bytes.Contains(got, []byte("38;2;10;20;30")) {
		t.Fatalf("ITU colon truecolor: %q", got)
	}
}

func TestRewriteSGRFaintDimsTruecolor(t *testing.T) {
	var s sgrState
	got := s.rewrite([]byte("\x1b[38;2;200;200;200m\x1b[2mY"))
	if !bytes.Contains(got, []byte("38;2;110;110;110")) && !bytes.Contains(got, []byte("38;2;109;109;109")) {
		// 200*55/100 = 110
		t.Fatalf("faint did not dim: %q", got)
	}
	term := vt10x.New(vt10x.WithSize(8, 2))
	if _, err := term.Write(got); err != nil {
		t.Fatal(err)
	}
	c := glyphToCell(term.Cell(0, 0))
	if c.FR > 160 {
		t.Fatalf("faint cell still bright FR=%d (got seq %q)", c.FR, got)
	}
}

func TestRewriteSGRFaintDefaultIsSecondary(t *testing.T) {
	var s sgrState
	got := s.rewrite([]byte("\x1b[2mZ"))
	term := vt10x.New(vt10x.WithSize(8, 2))
	if _, err := term.Write(append(got, 'Z')); err != nil {
		t.Fatal(err)
	}
	c := glyphToCell(term.Cell(0, 0))
	defR, _, _ := colorToRGB(vt10x.DefaultFG, false)
	if int(c.FR) >= int(defR) {
		t.Fatalf("faint default should be dimmer than body, FR=%d default=%d seq=%q", c.FR, defR, got)
	}
}

func TestRewriteSGRResetClearsFaint(t *testing.T) {
	var s sgrState
	_ = s.rewrite([]byte("\x1b[2m"))
	if !s.faint {
		t.Fatal("expected faint")
	}
	_ = s.rewrite([]byte("\x1b[0m"))
	if s.faint || s.hasFG {
		t.Fatalf("reset: %+v", s)
	}
}

func TestRewriteSGRUnderlineColorDoesNotSetFaint(t *testing.T) {
	var s sgrState
	got := s.rewrite([]byte("\x1b[58;2;122;162;247m/"))
	if s.faint {
		t.Fatalf("58;2 must not set faint: %q", got)
	}
	if !bytes.Contains(got, []byte("38;2;122;162;247")) {
		t.Fatalf("underline color should become ink when no FG: %q", got)
	}
}

func TestRewriteSGRUnderlineColorKeepsExistingFG(t *testing.T) {
	var s sgrState
	got := s.rewrite([]byte("\x1b[38;2;122;162;247;58;2;9;9;9m/"))
	if s.faint {
		t.Fatalf("58;2 must not set faint: %q", got)
	}
	if !bytes.Contains(got, []byte("38;2;122;162;247")) {
		t.Fatalf("lost skill FG: %q", got)
	}
	if bytes.Contains(got, []byte("38;2;9;9;9")) {
		t.Fatalf("underline color must not replace existing FG: %q", got)
	}
}

func TestRewriteSGRHoldsSplitColonTruecolor(t *testing.T) {
	var s sgrState
	got := s.rewrite([]byte("\x1b[38:2:122"))
	if bytes.Contains(got, []byte("38:2:122")) {
		t.Fatalf("incomplete CSI must be held, not passed through: %q", got)
	}
	got = s.rewrite([]byte(":162:247m/"))
	if !bytes.Contains(got, []byte("38;2;122;162;247")) {
		t.Fatalf("split colon truecolor not joined: %q", got)
	}
	term := vt10x.New(vt10x.WithSize(8, 2))
	if _, err := term.Write(got); err != nil {
		t.Fatal(err)
	}
	c := glyphToCell(term.Cell(0, 0))
	if c.FR != 122 || c.FG != 162 || c.FB != 247 {
		t.Fatalf("slash-skill teal lost: FR=%d,%d,%d seq=%q", c.FR, c.FG, c.FB, got)
	}
}

func TestSlashSkillTruecolorSurvivesCombinedSetColors(t *testing.T) {
	// Crossterm SetColors(fg, bg) for Grok slash overlay: skill teal on prompt bg.
	var s sgrState
	in := []byte("\x1b[38;2;122;162;247;48;2;20;20;20m/model")
	got := s.rewrite(in)
	term := vt10x.New(vt10x.WithSize(16, 2))
	if _, err := term.Write(got); err != nil {
		t.Fatal(err)
	}
	for x, want := range []rune("/model") {
		c := glyphToCell(term.Cell(x, 0))
		if c.Ch != want {
			t.Fatalf("cell %d ch=%q want %q", x, c.Ch, want)
		}
		if c.FR != 122 || c.FG != 162 || c.FB != 247 {
			t.Fatalf("cell %d slash highlight white-washed FR=%d,%d,%d seq=%q",
				x, c.FR, c.FG, c.FB, got)
		}
	}
}
