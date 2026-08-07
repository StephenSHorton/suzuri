package bridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to a running suzuri GUI bridge.
type Client struct {
	ep     Endpoint
	http   *http.Client
}

// Dial reads bridge.json and returns a client.
func Dial() (*Client, error) {
	ep, err := ReadEndpoint()
	if err != nil {
		return nil, err
	}
	return &Client{
		ep: ep,
		http: &http.Client{
			Timeout: 5 * time.Second,
		},
	}, nil
}

func (c *Client) get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, c.ep.URL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.ep.Token)
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("suzuri bridge unreachable (is the GUI running?): %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("bridge %s: %s: %s", path, res.Status, string(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// Status is a cheap ping.
func (c *Client) Status() (Status, error) {
	var s Status
	err := c.get("/v1/status", &s)
	return s, err
}

// Diag returns the full diagnostic snapshot with notes.
func (c *Client) Diag() (Snapshot, error) {
	var s Snapshot
	err := c.get("/v1/diag", &s)
	return s, err
}

// Snapshot returns the latest host snapshot.
func (c *Client) Snapshot() (Snapshot, error) {
	var s Snapshot
	err := c.get("/v1/snapshot", &s)
	return s, err
}

// Logs tails the GUI app log (host flushes first when reachable).
func (c *Client) Logs(lines int) (LogsResult, error) {
	if lines < 1 {
		lines = 150
	}
	var out LogsResult
	err := c.get(fmt.Sprintf("/v1/logs?lines=%d", lines), &out)
	return out, err
}

// Submit sends a line through the Warp bar path (block + echo arm + PTY).
func (c *Client) Submit(tabID *int, line string) (SubmitResult, error) {
	payload, _ := json.Marshal(SubmitRequest{TabID: tabID, Line: line})
	req, err := http.NewRequest(http.MethodPost, c.ep.URL+"/v1/submit", bytes.NewReader(payload))
	if err != nil {
		return SubmitResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.ep.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("suzuri bridge unreachable: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return SubmitResult{}, fmt.Errorf("submit: %s: %s", res.Status, string(body))
	}
	var out SubmitResult
	if err := json.Unmarshal(body, &out); err != nil {
		return SubmitResult{}, err
	}
	return out, nil
}

// Notes runs a notes bank op (list/get/create/update/delete) on the live GUI.
func (c *Client) Notes(req NotesRequest) (NotesResult, error) {
	payload, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, c.ep.URL+"/v1/notes", bytes.NewReader(payload))
	if err != nil {
		return NotesResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.ep.Token)
	httpReq.Header.Set("Content-Type", "application/json")
	// Writes can wait for UI-thread queue; give a little headroom.
	client := c.http
	if client.Timeout < 8*time.Second {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	res, err := client.Do(httpReq)
	if err != nil {
		return NotesResult{}, fmt.Errorf("suzuri bridge unreachable: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return NotesResult{}, fmt.Errorf("notes: %s: %s", res.Status, string(body))
	}
	var out NotesResult
	if err := json.Unmarshal(body, &out); err != nil {
		return NotesResult{}, err
	}
	return out, nil
}

// Workspace runs a workspace op on the live GUI (refreshes open panel).
func (c *Client) Workspace(req WorkspaceRequest) (WorkspaceResult, error) {
	payload, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, c.ep.URL+"/v1/workspace", bytes.NewReader(payload))
	if err != nil {
		return WorkspaceResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.ep.Token)
	httpReq.Header.Set("Content-Type", "application/json")
	client := c.http
	if client.Timeout < 8*time.Second {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	res, err := client.Do(httpReq)
	if err != nil {
		return WorkspaceResult{}, fmt.Errorf("suzuri bridge unreachable: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return WorkspaceResult{}, fmt.Errorf("workspace: %s: %s", res.Status, string(body))
	}
	var out WorkspaceResult
	if err := json.Unmarshal(body, &out); err != nil {
		return WorkspaceResult{}, err
	}
	return out, nil
}
