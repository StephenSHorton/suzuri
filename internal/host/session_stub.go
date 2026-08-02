//go:build !windows && !unix

package host

import (
	"context"
	"errors"
	"io"
)

// Session is unavailable on this OS.
type Session struct{}

var errUnsupported = errors.New("suzuri does not support this operating system")

func DefaultShell() string { return "" }

func StartSession(commandLine string, cols, rows int, workDir string) (*Session, error) {
	return nil, errUnsupported
}

func (s *Session) Read(p []byte) (int, error)  { return 0, errUnsupported }
func (s *Session) Write(p []byte) (int, error) { return 0, errUnsupported }
func (s *Session) Resize(cols, rows int) error { return errUnsupported }
func (s *Session) Pid() int                    { return 0 }
func (s *Session) Wait(ctx context.Context) (uint32, error) {
	return 0, errUnsupported
}
func (s *Session) Close() error { return nil }
func (s *Session) CopyOutput(w io.Writer) error {
	return errUnsupported
}
