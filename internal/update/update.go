// Package update is suzuri's in-app updater (GitHub Releases), modeled on
// toru's check → download → verify → replace pattern, adapted for a portable
// single-exe Windows build (no NSIS installer).
//
// Flow:
//  1. GET /repos/{owner}/{repo}/releases/latest
//  2. Compare tag to the running version (-ldflags -X main.version)
//  3. Download the windows-amd64 .exe (or .zip), verify SHA256SUMS if present
//  4. Rename the running exe to .old (allowed on Windows), write the new exe,
//     relaunch, then exit so the new process can delete the .old file.
package update

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
)

const defaultRepo = "StephenSHorton/suzuri"

// Info describes an available update.
type Info struct {
	Version     string // tag without leading v
	Notes       string
	AssetURL    string
	AssetName   string
	SHA256      string
	PublishedAt string
}

// Service checks GitHub Releases and applies portable updates.
type Service struct {
	repo    string
	current string
	client  *http.Client
	busy    atomic.Bool
}

// New returns a Service for owner/repo and the running version string.
func New(repo, currentVersion string) *Service {
	if repo == "" {
		repo = defaultRepo
	}
	return &Service{
		repo:    repo,
		current: currentVersion,
		client:  &http.Client{Timeout: 45 * time.Second},
	}
}

// Current returns the running version.
func (s *Service) Current() string { return s.current }

// AutoUpdate checks and installs if newer. Errors are logged, never returned
// to the UI path — offline/dev builds must not block startup.
func (s *Service) AutoUpdate() {
	info, err := s.Check()
	if err != nil {
		log.Warn("update check failed", "err", err)
		return
	}
	if info == nil {
		log.Debug("update: already current", "version", s.current)
		return
	}
	log.Info("update: installing", "version", info.Version)
	if err := s.DownloadAndApply(*info); err != nil {
		log.Warn("update apply failed", "err", err)
	}
}

// Check returns update info, or nil if up to date / dev / no releases.
func (s *Service) Check() (*Info, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rel, err := s.latestRelease(ctx)
	if err != nil || rel == nil {
		return nil, err
	}
	if !isNewer(rel.TagName, s.current) {
		return nil, nil
	}

	var asset ghAsset
	var sumsURL string
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, "-windows-amd64.exe") ||
			(strings.HasSuffix(n, ".exe") && strings.Contains(n, "suzuri") && !strings.Contains(n, "installer")) {
			if asset.URL == "" || strings.HasSuffix(n, "-windows-amd64.exe") {
				asset = a
			}
		}
		if n == "sha256sums" {
			sumsURL = a.URL
		}
		// Prefer zip if that's all we have.
		if asset.URL == "" && strings.HasSuffix(n, "-windows-amd64.zip") {
			asset = a
		}
	}
	if asset.URL == "" {
		return nil, fmt.Errorf("release %s has no windows-amd64 asset", rel.TagName)
	}

	var sum string
	if sumsURL != "" {
		sum, _ = s.fetchChecksum(ctx, sumsURL, asset.Name)
	}

	return &Info{
		Version:     strings.TrimPrefix(rel.TagName, "v"),
		Notes:       rel.Body,
		AssetURL:    asset.URL,
		AssetName:   asset.Name,
		SHA256:      sum,
		PublishedAt: rel.PublishedAt,
	}, nil
}

// DownloadAndApply downloads, verifies, replaces the running exe, and relaunches.
func (s *Service) DownloadAndApply(info Info) error {
	if !s.busy.CompareAndSwap(false, true) {
		return nil
	}
	committed := false
	defer func() {
		if !committed {
			s.busy.Store(false)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	dir := filepath.Join(os.TempDir(), "suzuri-update")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dir, info.AssetName)
	if err := s.download(ctx, info.AssetURL, dst); err != nil {
		return err
	}
	if info.SHA256 != "" {
		got, err := sha256File(dst)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, info.SHA256) {
			_ = os.Remove(dst)
			return fmt.Errorf("checksum mismatch: got %s want %s", got, info.SHA256)
		}
	}

	newExe := dst
	if strings.HasSuffix(strings.ToLower(dst), ".zip") {
		extracted, err := extractExeFromZip(dst, dir)
		if err != nil {
			return err
		}
		newExe = extracted
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return err
	}

	// Windows allows renaming a running image. Move current → .old, copy new
	// into place, start it, then exit so the new process can delete .old.
	old := self + ".old"
	_ = os.Remove(old)
	if err := os.Rename(self, old); err != nil {
		return fmt.Errorf("rename running exe: %w", err)
	}
	if err := copyFile(newExe, self); err != nil {
		// Best-effort rollback.
		_ = os.Rename(old, self)
		return fmt.Errorf("install new exe: %w", err)
	}

	cmd := exec.Command(self)
	cmd.Dir = filepath.Dir(self)
	if err := cmd.Start(); err != nil {
		_ = os.Rename(old, self)
		return fmt.Errorf("relaunch: %w", err)
	}
	committed = true
	log.Info("update: relaunched", "version", info.Version, "path", self)
	// Caller should exit the process shortly after.
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

// CleanupOldBinary removes a leftover .old sibling left by a previous update.
func CleanupOldBinary() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	old := self + ".old"
	if _, err := os.Stat(old); err == nil {
		if err := os.Remove(old); err != nil {
			log.Debug("update: could not remove old binary", "path", old, "err", err)
		} else {
			log.Debug("update: removed old binary", "path", old)
		}
	}
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt string    `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

func (s *Service) latestRelease(ctx context.Context) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", s.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "suzuri-updater")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github releases: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.Draft || rel.Prerelease {
		return nil, nil
	}
	return &rel, nil
}

func (s *Service) fetchChecksum(ctx context.Context, url, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "suzuri-updater")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && filepath.Base(strings.TrimPrefix(fields[1], "*")) == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

func (s *Service) download(ctx context.Context, url, dst string) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "suzuri-updater")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(f, resp.Body)
	return err
}

func extractExeFromZip(zipPath, dir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	for _, f := range r.File {
		base := filepath.Base(f.Name)
		if !strings.EqualFold(base, "suzuri.exe") && !strings.HasSuffix(strings.ToLower(base), ".exe") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		out := filepath.Join(dir, "suzuri-new.exe")
		w, err := os.Create(out)
		if err != nil {
			_ = rc.Close()
			return "", err
		}
		_, err = io.Copy(w, rc)
		_ = w.Close()
		_ = rc.Close()
		if err != nil {
			return "", err
		}
		return out, nil
	}
	return "", fmt.Errorf("zip has no .exe")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isNewer(tag, current string) bool {
	if current == "" || current == "dev" {
		return false
	}
	return cmpSemver(strings.TrimPrefix(tag, "v"), strings.TrimPrefix(current, "v")) > 0
}

func cmpSemver(a, b string) int {
	na, prea := parseVer(a)
	nb, preb := parseVer(b)
	for i := 0; i < 3; i++ {
		if na[i] != nb[i] {
			if na[i] > nb[i] {
				return 1
			}
			return -1
		}
	}
	switch {
	case prea == "" && preb == "":
		return 0
	case prea == "":
		return 1
	case preb == "":
		return -1
	default:
		return strings.Compare(prea, preb)
	}
}

func parseVer(v string) ([3]int, string) {
	v = strings.SplitN(v, "+", 2)[0]
	num, pre, _ := strings.Cut(v, "-")
	var out [3]int
	for i, p := range strings.SplitN(num, ".", 3) {
		if i > 2 {
			break
		}
		n, _ := strconv.Atoi(strings.TrimSpace(p))
		out[i] = n
	}
	return out, pre
}
