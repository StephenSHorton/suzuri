package iconstroke

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStrokeSameOnEveryKind(t *testing.T) {
	for _, size := range []int{10, 12, 16, 20, 24} {
		st := StrokeForSize(size)
		if st < 2 {
			t.Fatalf("size %d thins the X: %d", size, st)
		}
		for _, k := range []Kind{Min, Max, Restore, Close, Bell, Coffee} {
			ink := Raster(k, 40, 40, size, st)
			if !hasCoreAndFringe(ink) {
				t.Fatalf("%v size %d missing core/AA (%d px)", k, size, len(ink))
			}
		}
	}
}

func TestEachSlotDrawnOnce(t *testing.T) {
	_ = RenderStrip(StripOpts{DPI: 96, CaffeineOn: true, BellUnread: true})
	got := lastDrawCounts()
	for s, n := range got {
		if n != 1 {
			t.Fatalf("slot %d painted %d times, want 1", s, n)
		}
	}
}

func TestNoOffsetShadowCopy(t *testing.T) {
	for _, k := range []Kind{Min, Max, Close, Bell, Coffee} {
		ink := Raster(k, 50, 50, 16, StrokeForSize(16))
		if shiftedOverlap(ink, 1, 1) > 85 {
			t.Fatalf("%v looks like an offset second copy (%d%%)", k, shiftedOverlap(ink, 1, 1))
		}
	}
}

func TestWriteStripPreviews(t *testing.T) {
	dir := os.Getenv("ICONSTROKE_PNG_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dpi := range []int{96, 144} {
		img := RenderStrip(StripOpts{DPI: dpi, CaffeineOn: true, BellUnread: true})
		name := "caption_strip_100dpi.png"
		if dpi == 144 {
			name = "caption_strip_150dpi.png"
		}
		path := filepath.Join(dir, name)
		if err := WritePNG(path, img); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}

func hasCoreAndFringe(ink []Ink) bool {
	if len(ink) < 8 {
		return false
	}
	core, fringe := 0, 0
	for _, p := range ink {
		if p.A >= 200 {
			core++
		}
		if p.A > 0 && p.A < 180 {
			fringe++
		}
	}
	return core >= 4 && fringe >= 4
}

func shiftedOverlap(ink []Ink, dx, dy int) int {
	type xy struct{ x, y int }
	a := map[xy]struct{}{}
	b := map[xy]struct{}{}
	for _, p := range ink {
		if p.A < 80 {
			continue
		}
		a[xy{p.X, p.Y}] = struct{}{}
		b[xy{p.X + dx, p.Y + dy}] = struct{}{}
	}
	if len(a) == 0 {
		return 0
	}
	hit := 0
	for k := range a {
		if _, ok := b[k]; ok {
			hit++
		}
	}
	return hit * 100 / len(a)
}
