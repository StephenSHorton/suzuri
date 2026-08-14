package workspace

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	leasesFileName  = "leases.json"
	defaultLeaseTTL = 10 * time.Minute
)

// Lease is an exclusive hold on a workspace-relative path (persisted in leases.json).
type Lease struct {
	Path     string    `json:"path"`
	MemberID string    `json:"member_id"`
	Until    time.Time `json:"until"`
}

// ListLeases returns active (non-expired) path leases.
func (s *Store) ListLeases() ([]Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return nil, err
	}
	leases, err := s.readLeasesLocked()
	if err != nil {
		return nil, err
	}
	return activeLeases(leases, time.Now().UTC()), nil
}

// AcquireLease takes an exclusive lease on path for memberID.
// A second lease on the same path fails unless steal is true (steal posts a system line).
// ttl is a duration string such as "10m" (default 10m).
func (s *Store) AcquireLease(relPath, memberID, ttl string, steal bool, channel string) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return Lease{}, err
	}
	relPath, err := normalizeLeasePath(relPath)
	if err != nil {
		return Lease{}, err
	}
	memberID, err = s.requireMemberIDLocked(memberID)
	if err != nil {
		return Lease{}, err
	}
	dur, err := parseLeaseTTL(ttl)
	if err != nil {
		return Lease{}, err
	}
	now := time.Now().UTC()
	leases, err := s.readLeasesLocked()
	if err != nil {
		return Lease{}, err
	}
	leases = activeLeases(leases, now)

	var prev *Lease
	idx := -1
	for i := range leases {
		if leases[i].Path == relPath {
			cp := leases[i]
			prev = &cp
			idx = i
			break
		}
	}

	until := now.Add(dur)
	if prev != nil {
		if prev.MemberID == memberID {
			leases[idx].Until = until
			if err := s.writeLeasesLocked(leases); err != nil {
				return Lease{}, err
			}
			return leases[idx], nil
		}
		if !steal {
			return Lease{}, fmt.Errorf("path %s already leased by %s until %s (pass steal=true to take it)",
				relPath, prev.MemberID, prev.Until.Format(time.RFC3339))
		}
		leases[idx] = Lease{Path: relPath, MemberID: memberID, Until: until}
		if err := s.writeLeasesLocked(leases); err != nil {
			return Lease{}, err
		}
		_, _ = s.postSystemLocked(channel, fmt.Sprintf("stole lease %s from @%s (now @%s)", relPath, prev.MemberID, memberID))
		return leases[idx], nil
	}

	lease := Lease{Path: relPath, MemberID: memberID, Until: until}
	leases = append(leases, lease)
	if err := s.writeLeasesLocked(leases); err != nil {
		return Lease{}, err
	}
	return lease, nil
}

func (s *Store) readLeasesLocked() ([]Lease, error) {
	var leases []Lease
	p := filepath.Join(s.rootLocked(), leasesFileName)
	if err := readJSON(p, &leases); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Lease{}, nil
		}
		return nil, err
	}
	if leases == nil {
		leases = []Lease{}
	}
	return leases, nil
}

func (s *Store) writeLeasesLocked(leases []Lease) error {
	if leases == nil {
		leases = []Lease{}
	}
	return writeJSON(filepath.Join(s.rootLocked(), leasesFileName), leases)
}

func activeLeases(leases []Lease, now time.Time) []Lease {
	out := leases[:0]
	for _, l := range leases {
		if l.Until.After(now) && l.Path != "" && l.MemberID != "" {
			out = append(out, l)
		}
	}
	// Copy so we don't alias the original backing array after filter.
	if len(out) == 0 {
		return []Lease{}
	}
	cp := make([]Lease, len(out))
	copy(cp, out)
	return cp
}

func normalizeLeasePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" {
		return "", fmt.Errorf("path required")
	}
	p = path.Clean(p)
	p = strings.TrimPrefix(p, "./")
	if p == "" || p == "." || p == "/" {
		return "", fmt.Errorf("invalid path")
	}
	if strings.HasPrefix(p, "../") || p == ".." || strings.Contains(p, "/../") {
		return "", fmt.Errorf("invalid path")
	}
	return p, nil
}

func parseLeaseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultLeaseTTL, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid ttl %q (use 10m, 1h, 30s)", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("ttl must be positive")
	}
	if d > 24*time.Hour {
		return 0, fmt.Errorf("ttl too long (max 24h)")
	}
	return d, nil
}
