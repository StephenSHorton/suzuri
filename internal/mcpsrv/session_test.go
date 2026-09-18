package mcpsrv

import (
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestJoinSessionIDPrefersExplicit(t *testing.T) {
	got := joinSessionID(nil, "  explicit-1  ")
	if got != "explicit-1" {
		t.Fatalf("got %q", got)
	}
}

func TestJoinSessionIDFromMeta(t *testing.T) {
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Meta: mcp.Meta{"session_id": "meta-sess"},
		},
	}
	got := joinSessionID(req, "")
	if got != "meta-sess" {
		t.Fatalf("got %q", got)
	}
}

func TestJoinSessionIDFromEnvThenMint(t *testing.T) {
	t.Setenv("GROK_SESSION_ID", "")
	t.Setenv("GROK_CONVERSATION_ID", "")
	t.Setenv("MCP_SESSION_ID", "")
	t.Setenv("XAI_SESSION_ID", "")
	t.Setenv("SUZURI_SESSION_ID", "env-sess")
	got := joinSessionID(nil, "")
	if got != "env-sess" {
		t.Fatalf("got %q want env-sess", got)
	}
}

func TestJoinSessionIDMintsStable(t *testing.T) {
	t.Setenv("GROK_SESSION_ID", "")
	t.Setenv("GROK_CONVERSATION_ID", "")
	t.Setenv("MCP_SESSION_ID", "")
	t.Setenv("XAI_SESSION_ID", "")
	_ = os.Unsetenv("SUZURI_SESSION_ID")
	a := fallbackMintedSession()
	b := fallbackMintedSession()
	if a == "" || a != b {
		t.Fatalf("minted %q vs %q", a, b)
	}
}
