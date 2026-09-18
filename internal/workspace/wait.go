package workspace

import (
	"os"
	"path/filepath"
	"time"
)

const (
	defaultWaitTimeout = 60 * time.Second
	maxWaitTimeout     = 60 * time.Second
	waitPollInterval   = 150 * time.Millisecond
)

// ClampWaitTimeout applies the default (60s) and max (~60s) for workspace_wait.
func ClampWaitTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultWaitTimeout
	}
	if d > maxWaitTimeout {
		return maxWaitTimeout
	}
	return d
}

// Wait long-polls a channel's messages.jsonl until a message newer than sinceID
// exists or timeout elapses. An empty list on timeout is success.
//
// Polls with a sleep of waitPollInterval (never a tight loop). If sinceID is
// empty or unknown, the current tail is the cursor so existing history is not
// replayed.
func (s *Store) Wait(channel, sinceID string, timeout time.Duration) ([]Message, error) {
	timeout = ClampWaitTimeout(timeout)
	deadline := time.Now().Add(timeout)

	cursor, err := s.normalizeWaitCursor(channel, sinceID)
	if err != nil {
		return nil, err
	}

	path := s.messagesPath(channel)
	var lastSize int64 = -1
	var lastMod time.Time

	for {
		newer, err := s.messagesAfter(channel, cursor)
		if err != nil {
			return nil, err
		}
		if len(newer) > 0 {
			return newer, nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return []Message{}, nil
		}

		// Skip a full re-read next loop if the jsonl has not changed.
		if info, err := os.Stat(path); err == nil {
			lastSize = info.Size()
			lastMod = info.ModTime()
		}

		sleep := waitPollInterval
		if sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)

		if remaining = time.Until(deadline); remaining <= 0 {
			// One last look after the final sleep.
			newer, err = s.messagesAfter(channel, cursor)
			if err != nil {
				return nil, err
			}
			if len(newer) > 0 {
				return newer, nil
			}
			return []Message{}, nil
		}

		if info, err := os.Stat(path); err == nil {
			if info.Size() == lastSize && info.ModTime().Equal(lastMod) {
				continue
			}
		}
	}
}

func (s *Store) normalizeWaitCursor(channel, sinceID string) (string, error) {
	all, err := s.readAllMessages(channel)
	if err != nil {
		return "", err
	}
	if sinceID != "" {
		for _, m := range all {
			if m.ID == sinceID {
				return sinceID, nil
			}
		}
	}
	if len(all) == 0 {
		return "", nil
	}
	return all[len(all)-1].ID, nil
}

func (s *Store) messagesAfter(channel, sinceID string) ([]Message, error) {
	if sinceID == "" {
		// Empty channel: any first post is "new".
		return s.HistorySince(channel, maxHistoryCap, "", time.Time{})
	}
	return s.HistorySince(channel, maxHistoryCap, sinceID, time.Time{})
}

func (s *Store) messagesPath(channel string) string {
	channel = NormalizeChannel(channel)
	if channel == "" {
		channel = DefaultChannel
	}
	return filepath.Join(s.Root(), "channels", channel, messagesFileName)
}
