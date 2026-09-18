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
		Op:         workspace.Op(req.Op),
		Channel:    req.Channel,
		Body:       req.Body,
		Name:       req.Name,
		Kind:       req.Kind,
		MemberID:   req.MemberID,
		SessionID:  req.SessionID,
		ReplyTo:    req.ReplyTo,
		Topic:      req.Topic,
		Limit:      req.Limit,
		SinceID:    req.SinceID,
		AfterTS:    req.AfterTS,
		Since:      req.Since,
		Timeout:    req.Timeout,
		FilePath:   req.FilePath,
		FileID:     req.FileID,
		Status:     req.Status,
		StatusNote: req.StatusNote,
		Role:       req.Role,
		TaskID:     req.TaskID,
		Title:      req.Title,
		Files:      req.Files,
		TaskStatus: req.TaskStatus,
		Path:       req.Path,
		TTL:        req.TTL,
		Steal:      req.Steal,
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
		SessionID: r.SessionID,
		MemberID:  r.MemberID,
	}
	if r.Task != nil {
		t := mapTask(*r.Task)
		out.Task = &t
	}
	if r.Tasks != nil {
		out.Tasks = make([]bridge.WorkspaceTask, 0, len(r.Tasks))
		for _, t := range r.Tasks {
			out.Tasks = append(out.Tasks, mapTask(t))
		}
	}
	if r.Lease != nil {
		l := mapLease(*r.Lease)
		out.Lease = &l
	}
	if r.Leases != nil {
		out.Leases = make([]bridge.WorkspaceLease, 0, len(r.Leases))
		for _, l := range r.Leases {
			out.Leases = append(out.Leases, mapLease(l))
		}
	}
	if r.Member != nil {
		m := mapMember(*r.Member)
		out.Member = &m
	}
	if r.Members != nil {
		out.Members = make([]bridge.WorkspaceMember, 0, len(r.Members))
		for _, m := range r.Members {
			out.Members = append(out.Members, mapMember(m))
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

func mapMember(m workspace.Member) bridge.WorkspaceMember {
	st := string(m.Status)
	if st == "" {
		st = string(workspace.AvailIdle)
	}
	return bridge.WorkspaceMember{
		ID:           m.ID,
		Name:         m.Name,
		Kind:         string(m.Kind),
		SessionID:    m.SessionID,
		Role:         string(m.Role),
		Status:       st,
		StatusNote:   m.StatusNote,
		JoinedAt:     m.JoinedAt,
		LastSeen:     m.LastSeen,
		Polling:      m.Polling,
		Stale:        m.Stale,
		PresenceNote: m.PresenceNote,
	}
}

func mapTask(t workspace.Task) bridge.WorkspaceTask {
	return bridge.WorkspaceTask{
		ID:     t.ID,
		Title:  t.Title,
		Owner:  t.Owner,
		Status: string(t.Status),
		Files:  t.Files,
	}
}

func mapLease(l workspace.Lease) bridge.WorkspaceLease {
	return bridge.WorkspaceLease{
		Path:     l.Path,
		MemberID: l.MemberID,
		Until:    l.Until,
	}
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
		Mentions: msg.Mentions,
	}
	if msg.File != nil {
		f := mapFileRef(*msg.File)
		out.File = &f
	}
	return out
}
