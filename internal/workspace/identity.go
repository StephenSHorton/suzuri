package workspace

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Role is a claimed workspace function. Distinct from display Name.
type Role string

const (
	RoleNone    Role = ""
	RolePM      Role = "pm"
	RoleEngine  Role = "engine"
	RoleContent Role = "content"
)

// NormalizeRole maps free text to pm|engine|content or empty.
// Unknown values are an error (role is a closed set, unlike availability).
func NormalizeRole(s string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none", "clear":
		return RoleNone, nil
	case "pm":
		return RolePM, nil
	case "engine":
		return RoleEngine, nil
	case "content":
		return RoleContent, nil
	default:
		return "", fmt.Errorf("invalid role %q (want pm|engine|content)", strings.TrimSpace(s))
	}
}

// MintSessionID returns a unique session id for join when the client did not
// supply one (MCP injects this so the model does not have to remember).
func MintSessionID() string {
	return newID("sess")
}

// uniqueDisplayName returns want, or want-2 / want-3 / … if that name is taken
// by another member. exceptID is ignored (the member keeping their own name).
func uniqueDisplayName(want string, members []Member, exceptID string) string {
	base := strings.TrimSpace(want)
	if base == "" {
		base = "member"
	}
	taken := make(map[string]bool, len(members))
	for _, m := range members {
		if m.ID == exceptID {
			continue
		}
		taken[strings.ToLower(m.Name)] = true
	}
	if !taken[strings.ToLower(base)] {
		return base
	}
	for i := 2; i < 10000; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !taken[strings.ToLower(cand)] {
			return cand
		}
	}
	return base + "-" + newID("n")
}

// ResolveMentions maps @display-name tokens in body to member ids.
// Exact name match (case-insensitive). Longest name wins so @engine-2 is
// distinct from @engine. Order follows first appearance in the body.
func ResolveMentions(body string, members []Member) []string {
	if body == "" || len(members) == 0 {
		return nil
	}
	type pair struct {
		name string
		id   string
	}
	names := make([]pair, 0, len(members))
	for _, m := range members {
		n := strings.TrimSpace(m.Name)
		if n == "" || m.ID == "" {
			continue
		}
		names = append(names, pair{name: strings.ToLower(n), id: m.ID})
	}
	sort.SliceStable(names, func(i, j int) bool {
		return len(names[i].name) > len(names[j].name)
	})
	seen := make(map[string]bool)
	var out []string
	for _, tok := range mentionTokens(body) {
		tok = strings.ToLower(tok)
		for _, n := range names {
			if n.name == tok {
				if !seen[n.id] {
					seen[n.id] = true
					out = append(out, n.id)
				}
				break
			}
		}
	}
	return out
}

func mentionTokens(body string) []string {
	runes := []rune(body)
	var out []string
	for i := 0; i < len(runes); {
		if runes[i] == '@' && (i == 0 || unicode.IsSpace(runes[i-1])) {
			j := i + 1
			for j < len(runes) && isMentionRune(runes[j]) {
				j++
			}
			if j > i+1 {
				out = append(out, string(runes[i+1:j]))
			}
			i = j
			continue
		}
		i++
	}
	return out
}

func isMentionRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
}

// ClaimRole sets memberID's Role. Non-empty roles are exclusive among live
// members (anyone still in members.json). Idempotent if the member already
// holds the role. Passing empty / none / clear releases the role.
func (s *Store) ClaimRole(memberID, role string) (Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return Member{}, err
	}
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return Member{}, fmt.Errorf("member_id required")
	}
	r, err := NormalizeRole(role)
	if err != nil {
		return Member{}, err
	}
	members, err := s.readMembersLocked()
	if err != nil {
		return Member{}, err
	}
	idx := -1
	for i, m := range members {
		if m.ID == memberID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Member{}, fmt.Errorf("member not found")
	}
	if r != RoleNone {
		for i, m := range members {
			if i == idx {
				continue
			}
			if m.Role == r {
				return Member{}, fmt.Errorf("role %q already held by %s", r, m.Name)
			}
		}
	}
	members[idx].Role = r
	members[idx].LastSeen = time.Now().UTC()
	if err := s.writeMembersLocked(members); err != nil {
		return Member{}, err
	}
	return members[idx], nil
}
