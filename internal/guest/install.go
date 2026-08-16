package guest

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// InstallOptions control where the helper comes from.
type InstallOptions struct {
	// From is an existing Ladybird.app, binary, or zip. Empty = download or discover.
	From string
}

func install(id string, opt InstallOptions) (Manifest, error) {
	cat := loadCatalog()
	item, ok := cat.find(id)
	if !ok {
		return Manifest{}, fmt.Errorf("unknown guest %q (catalog has: %s)", id, catalogIDs(cat))
	}
	if item.ID == "ladybird" && runtime.GOOS != "darwin" {
		return Manifest{}, fmt.Errorf("ladybird is macOS-only for now")
	}

	src := strings.TrimSpace(opt.From)
	if src == "" {
		var err error
		src, err = resolveSource(item)
		if err != nil {
			return Manifest{}, err
		}
	}

	abs, err := filepath.Abs(src)
	if err != nil {
		return Manifest{}, err
	}
	if _, err := os.Stat(abs); err != nil {
		return Manifest{}, fmt.Errorf("install source: %w", err)
	}

	dst := InstallDir(item.ID)
	_ = os.RemoveAll(dst)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return Manifest{}, err
	}

	bin, err := placeHelper(abs, dst)
	if err != nil {
		_ = os.RemoveAll(dst)
		return Manifest{}, err
	}
	if runtime.GOOS == "darwin" {
		_ = addMacRpath(bin)
	}

	m := Manifest{
		ID:           item.ID,
		Name:         item.Name,
		Command:      bin,
		Args:         append([]string(nil), item.Args...),
		Protocol:     item.Protocol,
		Capabilities: append([]string(nil), item.Capabilities...),
	}
	if m.Protocol == 0 {
		m.Protocol = 1
	}
	if err := writeManifest(ManifestPath(item.ID), m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func catalogIDs(c Catalog) string {
	var ids []string
	for _, g := range c.Guests {
		ids = append(ids, g.ID)
	}
	return strings.Join(ids, ", ")
}

func resolveSource(item CatalogItem) (string, error) {
	if p := os.Getenv("LADYBIRD"); p != "" {
		if st, err := os.Stat(p); err == nil && (st.IsDir() || st.Mode().IsRegular()) {
			return p, nil
		}
	}
	if root := os.Getenv("LADYBIRD_SOURCE_DIR"); root != "" {
		cand := filepath.Join(root, "Build", "release", "bin", "Ladybird.app")
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand, nil
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		cand := filepath.Join(home, "projects", "ladybird", "Build", "release", "bin", "Ladybird.app")
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand, nil
		}
	}
	if asset, ok := item.assetForThisPlatform(); ok && item.Repo != "" {
		p, err := downloadRelease(item.Repo, asset.Name)
		if err == nil {
			return p, nil
		}
		return "", fmt.Errorf("download %s: %w\n(hint: suzuri guest install %s --from /path/to/Ladybird.app)", item.ID, err, item.ID)
	}
	return "", fmt.Errorf("no %s helper for this platform; pass --from /path/to/Ladybird.app", item.ID)
}

func placeHelper(src, dst string) (string, error) {
	if strings.HasSuffix(strings.ToLower(src), ".zip") {
		if err := unzip(src, dst); err != nil {
			return "", err
		}
		return findLadybirdBinary(dst)
	}
	app := src
	if strings.HasSuffix(src, "/Ladybird") && strings.Contains(src, ".app/") {
		app = src[:strings.Index(src, ".app/")+4]
	}
	base := filepath.Base(app)
	if strings.EqualFold(base, "Ladybird.app") || strings.HasSuffix(strings.ToLower(base), ".app") {
		target := filepath.Join(dst, "Ladybird.app")
		if err := copyTree(app, target); err != nil {
			return "", err
		}
		return findLadybirdBinary(target)
	}
	target := filepath.Join(dst, filepath.Base(src))
	if err := copyFile(src, target); err != nil {
		return "", err
	}
	_ = os.Chmod(target, 0o755)
	return target, nil
}

func findLadybirdBinary(root string) (string, error) {
	var found string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if info.Name() == "Ladybird" && strings.Contains(p, "Contents/MacOS/") {
			found = p
			return io.EOF
		}
		return nil
	})
	if err != nil && err != io.EOF {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("no Ladybird binary under %s", root)
	}
	return found, nil
}

func addMacRpath(bin string) error {
	dir := filepath.Dir(bin)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		cmd := exec.Command("install_name_tool", "-add_rpath", "@executable_path/../lib", p)
		out, err := cmd.CombinedOutput()
		if err != nil && !strings.Contains(string(out), "would duplicate") {
			// Best-effort: a binary that already has the rpath, or is not Mach-O.
			continue
		}
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	st, err := in.Stat()
	if err == nil {
		_ = os.Chmod(dst, st.Mode())
	}
	return nil
}

func unzip(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	dest = filepath.Clean(dest)
	for _, f := range r.File {
		name := filepath.Clean(f.Name)
		if strings.HasPrefix(name, "..") {
			continue
		}
		path := filepath.Join(dest, name)
		if !strings.HasPrefix(path, dest+string(os.PathSeparator)) && path != dest {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		_ = out.Close()
		_ = rc.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
