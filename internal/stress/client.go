package stress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// discovery is {config}/ai/{pid}.json, written by aicontrol.Start.
type discovery struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func waitDiscovery(configDir string, pid int, timeout time.Duration, exited <-chan struct{}) (discovery, error) {
	path := filepath.Join(configDir, "ai", fmt.Sprintf("%d.json", pid))
	deadline := time.Now().Add(timeout)
	for {
		if raw, err := os.ReadFile(path); err == nil {
			var d discovery
			if json.Unmarshal(raw, &d) == nil && d.URL != "" {
				return d, nil
			}
		}
		if time.Now().After(deadline) {
			return discovery{}, fmt.Errorf("no control discovery file at %s after %s", path, timeout)
		}
		select {
		case <-exited:
			return discovery{}, fmt.Errorf("suzuri exited before its control server came up")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type client struct {
	d    discovery
	http *http.Client
}

func newClient(d discovery) *client {
	return &client{d: d, http: &http.Client{Timeout: 8 * time.Second}}
}

// callResult is one control round trip.
type callResult struct {
	Status int
	RTT    time.Duration
	Body   map[string]any
	Err    error
}

func (c *client) do(method, path string, body any) callResult {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.d.URL+path, rd)
	if err != nil {
		return callResult{Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+c.d.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	t0 := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return callResult{RTT: time.Since(t0), Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	res := callResult{Status: resp.StatusCode, RTT: time.Since(t0), Err: err}
	if err == nil {
		_ = json.Unmarshal(raw, &res.Body)
	}
	if res.Status != 200 && res.Err == nil {
		msg, _ := res.Body["error"].(string)
		res.Err = fmt.Errorf("http %d: %s", res.Status, msg)
	}
	return res
}

func (c *client) call(tool string, args map[string]any) callResult {
	if args == nil {
		args = map[string]any{}
	}
	return c.do("POST", "/v1/call", map[string]any{"tool": tool, "args": args})
}

func (c *client) stats() callResult { return c.do("GET", "/v1/stats", nil) }

// paneIDs lists leaf ids of the active tab from a layout body, and the focus.
func paneIDs(layout map[string]any) (ids []int, focus int) {
	if f, ok := layout["focus_pane"].(float64); ok {
		focus = int(f)
	}
	wins, _ := layout["windows"].([]any)
	for _, w := range wins {
		wm, _ := w.(map[string]any)
		tabs, _ := wm["tabs"].([]any)
		for _, t := range tabs {
			tm, _ := t.(map[string]any)
			if active, _ := tm["active"].(bool); !active {
				continue
			}
			collectLeaves(tm["tree"], &ids)
		}
	}
	return ids, focus
}

func collectLeaves(n any, out *[]int) {
	m, ok := n.(map[string]any)
	if !ok {
		return
	}
	if p, ok := m["pane"].(float64); ok {
		if p > 0 {
			*out = append(*out, int(p))
		}
		return
	}
	collectLeaves(m["a"], out)
	collectLeaves(m["b"], out)
}
