package transfer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ProgressFunc is called for progress / status events (may be from a goroutine).
type ProgressFunc func(Event)

// Client runs the transfer engine with --json.
type Client struct {
	// Binary is the engine path; empty → ResolveBinary().
	Binary string
	// ConfigDir overrides HATO_CONFIG_DIR; empty → transfer.ConfigDir().
	ConfigDir string
	// OnEvent is optional; called for every NDJSON event (including errors).
	OnEvent ProgressFunc
}

func (c *Client) binary() (string, error) {
	if c.Binary != "" {
		return c.Binary, nil
	}
	return ResolveBinary()
}

func (c *Client) configDir() (string, error) {
	if c.ConfigDir != "" {
		if err := os.MkdirAll(c.ConfigDir, 0o700); err != nil {
			return "", err
		}
		return c.ConfigDir, nil
	}
	return ConfigDir()
}

// Run starts the engine with args (without --json; added automatically).
// It streams NDJSON until the process exits. ctx cancel sends SIGINT (then
// SIGKILL after a short grace) so `send` can shut down cleanly (exit 130).
func (c *Client) Run(ctx context.Context, args ...string) error {
	bin, err := c.binary()
	if err != nil {
		return err
	}
	cfg, err := c.configDir()
	if err != nil {
		return err
	}

	full := append([]string{"--json"}, args...)
	//nolint:gosec // binary path is resolved by us; args are host-controlled.
	cmd := exec.Command(bin, full...)
	cmd.Env = append(os.Environ(), "HATO_CONFIG_DIR="+cfg, "HATO_OUTPUT=json")
	cmd.Stdin = nil
	// Engine is a console-subsystem binary. From the GUI host we must not
	// inherit/create a visible console (Windows would open a separate terminal).
	configureEngineCmd(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// Keep stderr off the parent's console handles; drain for diagnostics.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		buf := make([]byte, 0, 4*1024)
		sc.Buffer(buf, 256*1024)
		for sc.Scan() {
			// Host already surfaces engine errors via NDJSON; stderr is noise.
			_ = sc.Text()
		}
	}()

	// Forward cancel → SIGINT so the engine can emit "stopped" and exit 130.
	stopFwd := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Signal(syscall.SIGINT)
			// Escalate if the engine ignores SIGINT.
			t := time.NewTimer(3 * time.Second)
			defer t.Stop()
			select {
			case <-t.C:
				_ = cmd.Process.Kill()
			case <-stopFwd:
			}
		case <-stopFwd:
		}
	}()

	var scanErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanErr = c.scan(stdout)
	}()

	waitErr := cmd.Wait()
	close(stopFwd)
	wg.Wait()

	if scanErr != nil {
		return scanErr
	}
	if waitErr == nil {
		return nil
	}
	// Exit 130 = interrupt (normal for send after Ctrl+C / cancel).
	if ee, ok := waitErr.(*exec.ExitError); ok {
		if ee.ExitCode() == 130 {
			return nil
		}
		// Killed after grace period while ctx cancelled — treat as clean stop.
		if ctx.Err() != nil && (ee.ExitCode() == -1 || errors.Is(ctx.Err(), context.Canceled)) {
			return nil
		}
		return fmt.Errorf("transfer engine exit %d: %w", ee.ExitCode(), waitErr)
	}
	if ctx.Err() != nil {
		return nil
	}
	return waitErr
}

func (c *Client) scan(r io.Reader) error {
	sc := bufio.NewScanner(r)
	// Tickets can be long; raise token size.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		ev, err := ParseEvent(line)
		if err != nil {
			// Non-JSON noise on stdout is a protocol bug; surface it.
			return fmt.Errorf("engine stdout: %w (line=%q)", err, truncate(string(line), 120))
		}
		if c.OnEvent != nil {
			c.OnEvent(ev)
		}
		if ev.Event == "error" && ev.Message != "" {
			// Keep scanning; process exit code is authoritative. Still useful
			// for hosts that stop on first error event.
			continue
		}
	}
	return sc.Err()
}

// SendTicket prepares a file/folder and waits until ctx is cancelled (or the
// engine exits). The first ready event's ticket is returned via onReady.
func (c *Client) SendTicket(ctx context.Context, path string, onReady func(ticket string), relayOnly bool) error {
	args := []string{"send", path}
	if relayOnly {
		args = append(args, "--relay")
	}
	var readyOnce sync.Once
	prev := c.OnEvent
	c.OnEvent = func(ev Event) {
		if prev != nil {
			prev(ev)
		}
		if ev.Event == "ready" && ev.Ticket != "" {
			readyOnce.Do(func() {
				if onReady != nil {
					onReady(ev.Ticket)
				}
			})
		}
	}
	defer func() { c.OnEvent = prev }()
	return c.Run(ctx, args...)
}

// ReceiveTicket downloads with a ticket into dir (default ".").
func (c *Client) ReceiveTicket(ctx context.Context, ticket, dir string) error {
	if dir == "" {
		dir = "."
	}
	return c.Run(ctx, "receive", ticket, dir)
}

// Me returns identity info from the engine.
func (c *Client) Me(ctx context.Context) (Event, error) {
	var me Event
	var found bool
	prev := c.OnEvent
	c.OnEvent = func(ev Event) {
		if prev != nil {
			prev(ev)
		}
		if ev.Event == "me" {
			me = ev
			found = true
		}
	}
	defer func() { c.OnEvent = prev }()
	if err := c.Run(ctx, "me"); err != nil {
		return Event{}, err
	}
	if !found {
		return Event{}, fmt.Errorf("engine returned no me event")
	}
	return me, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
