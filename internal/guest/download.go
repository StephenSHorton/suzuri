package guest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

func downloadRelease(repo, assetName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rel, err := latestHelperRelease(ctx, repo)
	if err != nil {
		return "", err
	}
	var url string
	for _, a := range rel.Assets {
		if a.Name == assetName {
			url = a.URL
			break
		}
	}
	if url == "" {
		return "", fmt.Errorf("release %s has no %s", rel.TagName, assetName)
	}
	dir, err := os.MkdirTemp("", "suzuri-guest-")
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, assetName)
	if err := downloadFile(ctx, url, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func latestHelperRelease(ctx context.Context, repo string) (*ghRelease, error) {
	// /releases (not /latest) so a prerelease helper still installs.
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=5", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "suzuri-guest")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no releases on %s", repo)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("github: %s %s", resp.Status, b)
	}
	var rels []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, err
	}
	for i := range rels {
		if rels[i].Draft {
			continue
		}
		return &rels[i], nil
	}
	return nil, fmt.Errorf("no published release on %s", repo)
}

func downloadFile(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "suzuri-guest")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}
