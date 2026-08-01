//go:build !windows

package host

import (
	"context"
	"errors"
	"io"
)

// Session is unavailable off Windows in v0.
type Session struct{}

var errWindowsOnly = errors.New("suzuri v0 only supports Windows ConPTY")

func DefaultShell() string { return "" }

func StartSession(commandLine string, cols, rows int, workDir string) (*Session, error) {
	return nil, errWindowsOnly
}

func (s *Session) Read(p []byte) (int, error)  { return 0, errWindowsOnly }
func (s *Session) Write(p []byte) (int, error) { return 0, errWindowsOnly }
func (s *Session) Resize(cols, rows int) error { return errWindowsOnly }
func (s *Session) Pid() int                    { return 0 }
func (s *Session) Wait(ctx context.Context) (uint32, error) {
	return 0, errWindowsOnly
}
func (s *Session) Close() error { return nil }
func (s *Session) CopyOutput(w io.Writer) error {
	return errWindowsOnly
}
