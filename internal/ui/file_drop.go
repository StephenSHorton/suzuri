package ui

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// extractDroppedPaths turns ebiten.DroppedFiles() (or any fs.FS of dropped
// entries) into absolute filesystem paths the transfer engine can open.
func extractDroppedPaths(fsys fs.FS) []string {
	if fsys == nil {
		return nil
	}
	var out []string
	_ = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || path == "." {
			return nil
		}
		// Only top-level drop roots (not every file inside a dropped folder).
		if strings.Contains(path, "/") || strings.Contains(path, "\\") {
			return nil
		}
		real := resolveDroppedRealPath(fsys, path, d)
		if real != "" {
			out = append(out, real)
		}
		if d.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	return out
}

func resolveDroppedRealPath(fsys fs.FS, virt string, d fs.DirEntry) string {
	f, err := fsys.Open(virt)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if of, ok := f.(*os.File); ok {
		if name := of.Name(); name != "" {
			if abs, err := filepath.Abs(name); err == nil {
				return abs
			}
			return name
		}
	}
	// Fallback: basename only (usually insufficient, but better than empty).
	if d != nil {
		return d.Name()
	}
	return virt
}
