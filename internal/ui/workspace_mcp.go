//go:build windows || darwin

package ui

import (
	"github.com/StephenSHorton/suzuri/internal/bridge"
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/workspace"
)

// runWorkspaceOnChrome applies a workspace op (disk store) and refreshes the
// open workspace panel when visible.
func runWorkspaceOnChrome(m *chrome.Model, req bridge.WorkspaceRequest) bridge.WorkspaceResult {
	if m == nil {
		return bridge.WorkspaceResult{OK: false, Error: "no chrome model"}
	}
	r := workspace.Apply(nil, workspace.Request{
		Op:        workspace.Op(req.Op),
		Channel:   req.Channel,
		Body:      req.Body,
		Name:      req.Name,
		Kind:      req.Kind,
		MemberID:  req.MemberID,
		SessionID: req.SessionID,
		ReplyTo:   req.ReplyTo,
		Topic:     req.Topic,
		Limit:     req.Limit,
		FilePath:  req.FilePath,
		FileID:    req.FileID,
	})
	if m.WorkspaceOpen {
		*m = m.UpdateChrome(chrome.RefreshWorkspaceMsg{}).Model
	}
	return workspaceResultToBridge(r)
}

func workspaceResultToBridge(r workspace.Result) bridge.WorkspaceResult {
	out := bridge.WorkspaceResult{
		OK:        r.OK,
		Path:      r.Path,
		Error:     r.Error,
		Status:    r.Status,
		Count:     r.Count,
		LocalPath: r.LocalPath,
	}
	if r.Member != nil {
		m := bridge.WorkspaceMember{
			ID:        r.Member.ID,
			Name:      r.Member.Name,
			Kind:      string(r.Member.Kind),
			SessionID: r.Member.SessionID,
			JoinedAt:  r.Member.JoinedAt,
			LastSeen:  r.Member.LastSeen,
		}
		out.Member = &m
	}
	if r.Members != nil {
		out.Members = make([]bridge.WorkspaceMember, 0, len(r.Members))
		for _, m := range r.Members {
			out.Members = append(out.Members, bridge.WorkspaceMember{
				ID:        m.ID,
				Name:      m.Name,
				Kind:      string(m.Kind),
				SessionID: m.SessionID,
				JoinedAt:  m.JoinedAt,
				LastSeen:  m.LastSeen,
			})
		}
	}
	if r.Channel != nil {
		ch := bridge.WorkspaceChannel{
			ID:        r.Channel.ID,
			Name:      r.Channel.Name,
			CreatedAt: r.Channel.CreatedAt,
			Topic:     r.Channel.Topic,
		}
		out.Channel = &ch
	}
	if r.Channels != nil {
		out.Channels = make([]bridge.WorkspaceChannel, 0, len(r.Channels))
		for _, ch := range r.Channels {
			out.Channels = append(out.Channels, bridge.WorkspaceChannel{
				ID:        ch.ID,
				Name:      ch.Name,
				CreatedAt: ch.CreatedAt,
				Topic:     ch.Topic,
			})
		}
	}
	if r.File != nil {
		f := mapFileRef(*r.File)
		out.File = &f
	}
	if r.Message != nil {
		msg := mapMessage(*r.Message)
		out.Message = &msg
	}
	if r.Messages != nil {
		out.Messages = make([]bridge.WorkspaceMessage, 0, len(r.Messages))
		for _, msg := range r.Messages {
			out.Messages = append(out.Messages, mapMessage(msg))
		}
	}
	return out
}

func mapFileRef(f workspace.FileRef) bridge.WorkspaceFile {
	return bridge.WorkspaceFile{
		ID:      f.ID,
		Name:    f.Name,
		Bytes:   f.Bytes,
		SHA256:  f.SHA256,
		RelPath: f.RelPath,
	}
}

func mapMessage(msg workspace.Message) bridge.WorkspaceMessage {
	out := bridge.WorkspaceMessage{
		ID:       msg.ID,
		Channel:  msg.Channel,
		TS:       msg.TS,
		FromID:   msg.FromID,
		FromName: msg.FromName,
		FromKind: string(msg.FromKind),
		Kind:     msg.Kind,
		Body:     msg.Body,
		ReplyTo:  msg.ReplyTo,
	}
	if msg.File != nil {
		f := mapFileRef(*msg.File)
		out.File = &f
	}
	return out
}
