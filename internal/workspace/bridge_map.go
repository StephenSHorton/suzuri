package workspace

// ToBridgeMap returns a plain map suitable for JSON MCP/bridge responses
// without importing the bridge package (avoids import cycles).
func (r Result) ToMap() map[string]any {
	out := map[string]any{
		"ok":   r.OK,
		"path": r.Path,
	}
	if r.Error != "" {
		out["error"] = r.Error
	}
	if r.Status != nil {
		out["status"] = r.Status
	}
	if r.Member != nil {
		out["member"] = r.Member
	}
	if r.Members != nil {
		out["members"] = r.Members
		out["count"] = r.Count
	}
	if r.Channel != nil {
		out["channel"] = r.Channel
	}
	if r.Channels != nil {
		out["channels"] = r.Channels
		out["count"] = r.Count
	}
	if r.Message != nil {
		out["message"] = r.Message
	}
	if r.Messages != nil {
		out["messages"] = r.Messages
		out["count"] = r.Count
	}
	if r.File != nil {
		out["file"] = r.File
	}
	if r.LocalPath != "" {
		out["local_path"] = r.LocalPath
	}
	if r.Count > 0 && out["count"] == nil {
		out["count"] = r.Count
	}
	if r.SessionID != "" {
		out["session_id"] = r.SessionID
	}
	if r.MemberID != "" {
		out["member_id"] = r.MemberID
	}
	return out
}
