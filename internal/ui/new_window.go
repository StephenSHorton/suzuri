package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/log"
)

// openNewWindow starts another suzuri process so the user gets a second OS window.
// Failures are logged only — the current window stays usable.
func openNewWindow() {
	if err := spawnNewWindow(); err != nil {
		log.Warn("new window failed", "err", err)
		return
	}
}

func spawnNewWindow() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	cmd := exec.Command(self)
	cmd.Dir = filepath.Dir(self)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the child so Unix does not leave a zombie; the child is independent.
	go func() { _ = cmd.Wait() }()
	log.Info("opened new window", "pid", cmd.Process.Pid, "path", self)
	return nil
}
