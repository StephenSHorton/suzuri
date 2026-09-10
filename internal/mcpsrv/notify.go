package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const claudeChannelMethod = "notifications/claude/channel"

// notifyTransport wraps an MCP transport so the workspace watcher can push
// JSON-RPC notifications on the same connection grok-fork reads for --channels.
type notifyTransport struct {
	inner mcp.Transport
	mu    sync.Mutex
	conn  mcp.Connection
}

func newNotifyTransport(inner mcp.Transport) *notifyTransport {
	return &notifyTransport{inner: inner}
}

func (t *notifyTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	c, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.conn = c
	t.mu.Unlock()
	return c, nil
}

func (t *notifyTransport) Notify(method string, params any) error {
	t.mu.Lock()
	c := t.conn
	t.mu.Unlock()
	if c == nil {
		return fmt.Errorf("mcp session not connected")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	msg := &jsonrpc.Request{
		Method: method,
		Params: raw,
	}
	return c.Write(context.Background(), msg)
}

func channelNotificationParams(content string, meta map[string]string) map[string]any {
	if meta == nil {
		meta = map[string]string{}
	}
	return map[string]any{
		"content": content,
		"meta":    meta,
	}
}
