package guest

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// DefaultCatalogURL is optional. The embedded catalog is enough to install
// ladybird from a GitHub release or a local --from path.
const DefaultCatalogURL = "https://raw.githubusercontent.com/StephenSHorton/suzuri/master/site/public/catalog.v1.json"

// Catalog is the installable guest list.
type Catalog struct {
	Version int           `json:"version"`
	Guests  []CatalogItem `json:"guests"`
}

// CatalogItem is one guest Suzuri knows how to install.
type CatalogItem struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Protocol     int              `json:"protocol"`
	Capabilities []string         `json:"capabilities"`
	Args         []string         `json:"args"`
	Repo         string           `json:"repo"`
	Assets       map[string]Asset `json:"assets"`
}

// Asset names the GitHub release file for one GOOS/GOARCH.
type Asset struct {
	Name string `json:"name"`
}

func platformKey() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func (it CatalogItem) assetForThisPlatform() (Asset, bool) {
	if it.Assets == nil {
		return Asset{}, false
	}
	a, ok := it.Assets[platformKey()]
	return a, ok && a.Name != ""
}

func defaultCatalog() Catalog {
	return Catalog{
		Version: 1,
		Guests: []CatalogItem{{
			ID:           "ladybird",
			Name:         "Ladybird",
			Description:  "Independent web engine in a Suzuri pane.",
			Protocol:     1,
			Capabilities: []string{"pane", "navigate"},
			Args:         []string{"--temporary-profile"},
			Repo:         "StephenSHorton/suzuri-ladybird",
			Assets: map[string]Asset{
				"darwin/arm64": {Name: "suzuri-ladybird-macos-arm64.zip"},
				"darwin/amd64": {Name: "suzuri-ladybird-macos-amd64.zip"},
			},
		}},
	}
}

func parseCatalog(raw []byte) (Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return Catalog{}, fmt.Errorf("catalog: %w", err)
	}
	if c.Version != 0 && c.Version != 1 {
		return Catalog{}, fmt.Errorf("catalog: unsupported version %d", c.Version)
	}
	if c.Version == 0 {
		c.Version = 1
	}
	return c, nil
}

func (c Catalog) find(id string) (CatalogItem, bool) {
	want := strings.TrimSpace(strings.ToLower(id))
	for _, g := range c.Guests {
		if strings.EqualFold(g.ID, want) || strings.EqualFold(g.Name, want) {
			return g, true
		}
	}
	return CatalogItem{}, false
}

func loadCatalog() Catalog {
	if p := os.Getenv("SUZURI_GUEST_CATALOG"); p != "" {
		b, err := os.ReadFile(p)
		if err == nil {
			if c, err := parseCatalog(b); err == nil && len(c.Guests) > 0 {
				return c
			}
		}
	}
	return defaultCatalog()
}
