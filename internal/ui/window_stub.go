//go:build !windows

package ui

import (
	"errors"

	"github.com/StephenSHorton/suzuri/internal/host"
)

// Run is Windows-only in v0.
func Run(sess *host.Session) error {
	return errors.New("suzuri v0 UI only supports Windows")
}
