package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Dir is %LOCALAPPDATA%\suzuri (or temp fallback).
func Dir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "suzuri")
}

// EndpointPath is where the running GUI writes connection info.
func EndpointPath() string {
	return filepath.Join(Dir(), "bridge.json")
}

// WriteEndpoint publishes the loopback endpoint for MCP clients.
func WriteEndpoint(ep Endpoint) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return err
	}
	path := EndpointPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RemoveEndpoint clears the endpoint file (best-effort on GUI exit).
func RemoveEndpoint() {
	_ = os.Remove(EndpointPath())
}

// ReadEndpoint loads the GUI endpoint file.
func ReadEndpoint() (Endpoint, error) {
	b, err := os.ReadFile(EndpointPath())
	if err != nil {
		return Endpoint{}, fmt.Errorf("suzuri GUI not running (no %s): %w", EndpointPath(), err)
	}
	var ep Endpoint
	if err := json.Unmarshal(b, &ep); err != nil {
		return Endpoint{}, fmt.Errorf("parse bridge.json: %w", err)
	}
	if ep.URL == "" || ep.Token == "" {
		return Endpoint{}, fmt.Errorf("bridge.json incomplete")
	}
	return ep, nil
}
