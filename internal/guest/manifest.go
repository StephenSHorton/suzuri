package guest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GuestCommand is a palette row an installed guest may contribute.
type GuestCommand struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Desc  string `json:"desc,omitempty"`
}

// Manifest is the JSON chrome reads from guests/*.json.
type Manifest struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Command      string         `json:"command"`
	Args         []string       `json:"args,omitempty"`
	Protocol     int            `json:"protocol"`
	Capabilities []string       `json:"capabilities,omitempty"`
	Commands     []GuestCommand `json:"commands,omitempty"`
}

func writeManifest(path string, m Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readManifest(path string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	return m, nil
}

func listInstalled() []Manifest {
	dir := GuestsDir()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Manifest
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		m, err := readManifest(filepath.Join(dir, name))
		if err != nil || m.ID == "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

func installed(id string) (Manifest, bool) {
	m, err := readManifest(ManifestPath(id))
	if err != nil || m.ID == "" {
		return Manifest{}, false
	}
	return m, true
}
