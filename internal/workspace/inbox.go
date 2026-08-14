package workspace

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// mentionToken matches @name / @member_id tokens in a message body.
var mentionToken = regexp.MustCompile(`@[A-Za-z0-9_.-]+`)

// Inbox returns mentions of memberID and assignments to them, newer than
// sinceID when set. This is the default poll target (not the whole channel).
// Scans every channel. Does not mutate tasks/leases — only reads mention /
// assign / to fields if they already exist on messages, plus @name in the body.
func (s *Store) Inbox(memberID, sinceID string, limit int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return nil, err
	}
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return nil, fmt.Errorf("member_id required")
	}
	members, err := s.readMembersLocked()
	if err != nil {
		return nil, err
	}
	var member *Member
	for i := range members {
		if members[i].ID == memberID || strings.EqualFold(members[i].Name, memberID) {
			cp := members[i]
			member = &cp
			break
		}
	}
	if member == nil {
		return nil, fmt.Errorf("member not found")
	}
	now := time.Now().UTC()
	for i := range members {
		if members[i].ID == member.ID {
			members[i].LastSeen = now
			_ = s.writeMembersLocked(members)
			break
		}
	}

	chs, err := s.listChannelsLocked()
	if err != nil {
		return nil, err
	}

	var cursorTS time.Time
	cursorFound := sinceID == ""
	var matched []Message
	for _, ch := range chs {
		msgs, err := s.readAllMessagesLocked(ch.ID)
		if err != nil {
			return nil, err
		}
		for _, msg := range msgs {
			if sinceID != "" && msg.ID == sinceID {
				cursorFound = true
				cursorTS = msg.TS
			}
			if messageTargetsMember(msg, *member) {
				matched = append(matched, msg)
			}
		}
	}

	if sinceID != "" && cursorFound {
		out := make([]Message, 0, len(matched))
		for _, msg := range matched {
			if msg.ID == sinceID {
				continue
			}
			if !cursorTS.IsZero() && msg.TS.Before(cursorTS) {
				continue
			}
			out = append(out, msg)
		}
		matched = out
	}

	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].TS.Equal(matched[j].TS) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].TS.Before(matched[j].TS)
	})

	incremental := sinceID != "" && cursorFound
	return clampHistory(matched, limit, incremental), nil
}

// messageTargetsMember reports whether msg mentions or assigns member.
func messageTargetsMember(msg Message, member Member) bool {
	id := strings.ToLower(strings.TrimSpace(member.ID))
	name := strings.ToLower(strings.TrimSpace(member.Name))
	if id == "" && name == "" {
		return false
	}
	match := func(v string) bool {
		lv := strings.ToLower(strings.TrimSpace(v))
		return lv != "" && (lv == id || lv == name)
	}

	for _, v := range msg.Mentions {
		if match(v) {
			return true
		}
	}
	for _, v := range msg.MentionIDs {
		if match(v) {
			return true
		}
	}
	for _, v := range msg.MentionMemberIDs {
		if match(v) {
			return true
		}
	}
	for _, v := range []string{msg.Assign, msg.AssignTo, msg.To, msg.ToID} {
		if match(v) {
			return true
		}
	}
	for _, tok := range mentionToken.FindAllString(msg.Body, -1) {
		if match(strings.TrimPrefix(tok, "@")) {
			return true
		}
	}
	return false
}
