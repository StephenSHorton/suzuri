// Package mcpsrv is the spawn-on-demand stdio MCP server for suzuri.
//
// Grok (or any MCP client) starts `suzuri mcp` when it needs tools. This process
// attaches to a *running* suzuri GUI via the loopback bridge and exits when the
// client closes stdin. See KB: "MCP does not require an always-on background process".
package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/bridge"
	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// RunStdio starts the MCP server on stdin/stdout. Logs go to stderr only.
func RunStdio() error {
	// Never write protocol noise to stdout.
	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[suzuri-mcp] "+format+"\n", args...)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "suzuri",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "suzuri_status",
		Description: "Check whether the suzuri GUI is running and the MCP bridge is reachable. Prefer this first.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		c, err := bridge.Dial()
		if err != nil {
			return textResult(map[string]any{
				"ok":      false,
				"message": err.Error(),
				"hint":    "Launch suzuri.exe (the GUI). The MCP process is spawn-on-demand and attaches to the live window.",
			}), nil, nil
		}
		st, err := c.Status()
		if err != nil {
			return textResult(map[string]any{"ok": false, "message": err.Error()}), nil, nil
		}
		return textResult(st), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "suzuri_diag",
		Description: "Full diagnostic report: tabs, viewport text, command blocks, input bar, echo-filter state, PTY tail, and notes (e.g. dual command display). Use when debugging what the user sees.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		c, err := bridge.Dial()
		if err != nil {
			return errResult(err), nil, nil
		}
		s, err := c.Diag()
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(s), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "suzuri_snapshot",
		Description: "Structured snapshot of the live host (tabs, viewport lines, blocks, input draft). Same payload as diag without extra notes pass.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		c, err := bridge.Dial()
		if err != nil {
			return errResult(err), nil, nil
		}
		s, err := c.Snapshot()
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(s), nil, nil
	})

	type submitArgs struct {
		Line  string `json:"line" jsonschema:"command line to submit via the Warp input bar"`
		TabID *int   `json:"tab_id,omitempty" jsonschema:"optional tab id; default is the active tab"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "suzuri_submit",
		Description: "Submit a command through suzuri's Warp bar path (pushes a command block, arms echo suppress, writes to ConPTY). Requires the GUI to be running.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args submitArgs) (*mcp.CallToolResult, any, error) {
		c, err := bridge.Dial()
		if err != nil {
			return errResult(err), nil, nil
		}
		res, err := c.Submit(args.TabID, args.Line)
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(res), nil, nil
	})

	type logsArgs struct {
		Lines *int `json:"lines,omitempty" jsonschema:"how many trailing log lines to return (default 150, max 5000)"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "suzuri_logs",
		Description: "Tail suzuri's application log file (%LOCALAPPDATA%\\suzuri\\suzuri.log): bridge events, tab lifecycle, panics. " +
			"For shell session output / what the user sees, use suzuri_diag (viewport + pty_tail) instead. " +
			"Works even if the GUI is down (reads the file directly).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args logsArgs) (*mcp.CallToolResult, any, error) {
		n := 150
		if args.Lines != nil && *args.Lines > 0 {
			n = *args.Lines
		}
		// Prefer bridge so the GUI flushes its open log handle first.
		if c, err := bridge.Dial(); err == nil {
			res, err := c.Logs(n)
			if err == nil {
				return textResult(res), nil, nil
			}
		}
		// Fallback: direct file read (GUI crashed / not running).
		path, lines, err := applog.Tail(n)
		if err != nil {
			return textResult(map[string]any{
				"ok":      false,
				"path":    path,
				"error":   err.Error(),
				"message": "could not read suzuri.log",
			}), nil, nil
		}
		return textResult(bridge.LogsResult{
			OK:    true,
			Path:  path,
			Lines: lines,
			Count: len(lines),
		}), nil, nil
	})

	// --- Notes bank (Ctrl+Shift+M) ---
	// Prefer live GUI (flushes open editor); fall back to notes.json on disk.

	type notesListArgs struct{}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "suzuri_notes_list",
		Description: "List all suzuri notes (Ctrl+Shift+M bank): id, title, body, updated, active. Prefers the live GUI; falls back to notes.json if the GUI is down.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ notesListArgs) (*mcp.CallToolResult, any, error) {
		return notesTool(bridge.NotesRequest{Op: bridge.NotesOpList}), nil, nil
	})

	type notesGetArgs struct {
		ID string `json:"id,omitempty" jsonschema:"note id; omit or empty for the active note"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "suzuri_notes_get",
		Description: "Get one suzuri note by id (full body). Omit id for the currently active note.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args notesGetArgs) (*mcp.CallToolResult, any, error) {
		return notesTool(bridge.NotesRequest{Op: bridge.NotesOpGet, ID: args.ID}), nil, nil
	})

	type notesCreateArgs struct {
		Title string `json:"title,omitempty" jsonschema:"optional title"`
		Body  string `json:"body,omitempty" jsonschema:"note body text"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "suzuri_notes_create",
		Description: "Create a new suzuri note (becomes active). Title optional; body is the full text. Syncs the open notes UI when the GUI is running.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args notesCreateArgs) (*mcp.CallToolResult, any, error) {
		title, body := args.Title, args.Body
		return notesTool(bridge.NotesRequest{
			Op:    bridge.NotesOpCreate,
			Title: &title,
			Body:  &body,
		}), nil, nil
	})

	type notesUpdateArgs struct {
		ID        string  `json:"id,omitempty" jsonschema:"note id; omit for active note"`
		Title     *string `json:"title,omitempty" jsonschema:"new title; omit to leave unchanged; empty string clears stored title"`
		Body      *string `json:"body,omitempty" jsonschema:"new body; omit to leave unchanged"`
		SetActive bool    `json:"set_active,omitempty" jsonschema:"make this note the active one"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "suzuri_notes_update",
		Description: "Update an existing suzuri note (partial: title and/or body). Omit id for the active note. set_active makes it the current note.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args notesUpdateArgs) (*mcp.CallToolResult, any, error) {
		if args.Title == nil && args.Body == nil && !args.SetActive {
			return errResult(fmt.Errorf("notes_update: provide title, body, and/or set_active")), nil, nil
		}
		return notesTool(bridge.NotesRequest{
			Op:        bridge.NotesOpUpdate,
			ID:        args.ID,
			Title:     args.Title,
			Body:      args.Body,
			SetActive: args.SetActive,
		}), nil, nil
	})

	type notesDeleteArgs struct {
		ID string `json:"id,omitempty" jsonschema:"note id; omit for active note"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "suzuri_notes_delete",
		Description: "Delete a suzuri note by id (omit id = active). The last remaining note is cleared rather than removed.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args notesDeleteArgs) (*mcp.CallToolResult, any, error) {
		return notesTool(bridge.NotesRequest{Op: bridge.NotesOpDelete, ID: args.ID}), nil, nil
	})

	logf("stdio MCP ready (attach to GUI via %s)", bridge.EndpointPath())
	return server.Run(context.Background(), &mcp.StdioTransport{})
}

// notesTool prefers the live bridge (UI-thread, flushes editor); falls back to notes.json.
func notesTool(req bridge.NotesRequest) *mcp.CallToolResult {
	if c, err := bridge.Dial(); err == nil {
		res, err := c.Notes(req)
		if err == nil {
			return textResult(res)
		}
		// Fall through to disk if bridge call fails mid-flight.
	}
	off := chrome.ApplyNotesDiskOp(string(req.Op), req.ID, req.Title, req.Body, req.SetActive)
	return textResult(off)
}

func textResult(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		b = []byte(fmt.Sprintf("%v", v))
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(b)},
		},
	}
}

func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: "Error: " + err.Error()},
		},
	}
}
