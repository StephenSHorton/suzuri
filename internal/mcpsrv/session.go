package mcpsrv

import (
	"os"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/StephenSHorton/suzuri/internal/workspace"
)

var (
	mintedSessionMu sync.Mutex
	mintedSession   string
)

// joinSessionID picks a session id so the model does not have to pass one.
// Preference: explicit arg → CallToolRequest / client _meta → MCP session →
// well-known env → process-stable minted id.
func joinSessionID(req *mcp.CallToolRequest, argsSession string) string {
	if s := strings.TrimSpace(argsSession); s != "" {
		return s
	}
	if req != nil && req.Params != nil {
		if s := sessionFromMeta(req.Params.Meta); s != "" {
			return s
		}
	}
	if req != nil && req.Session != nil {
		if s := strings.TrimSpace(req.Session.ID()); s != "" {
			return s
		}
	}
	for _, key := range []string{
		"SUZURI_SESSION_ID",
		"GROK_SESSION_ID",
		"GROK_CONVERSATION_ID",
		"MCP_SESSION_ID",
		"XAI_SESSION_ID",
	} {
		if s := strings.TrimSpace(os.Getenv(key)); s != "" {
			return s
		}
	}
	return fallbackMintedSession()
}

func sessionFromMeta(meta mcp.Meta) string {
	if meta == nil {
		return ""
	}
	for _, key := range []string{
		"session_id",
		"grok_session_id",
		"conversation_id",
		"mcp_session_id",
	} {
		v, ok := meta[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func fallbackMintedSession() string {
	mintedSessionMu.Lock()
	defer mintedSessionMu.Unlock()
	if mintedSession == "" {
		mintedSession = workspace.MintSessionID()
	}
	return mintedSession
}
