package guest

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

func remove(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("missing guest id")
	}
	m, ok := installed(id)
	dir := InstallDir(id)
	man := ManifestPath(id)
	if !ok {
		if _, err := os.Stat(dir); err != nil && os.IsNotExist(err) {
			if _, err := os.Stat(man); err != nil && os.IsNotExist(err) {
				return fmt.Errorf("%s is not installed", id)
			}
		}
	}
	if m.Command != "" {
		killByPath(m.Command)
	}
	_ = os.Remove(man)
	_ = os.RemoveAll(dir)
	return nil
}

func killByPath(bin string) {
	if bin == "" {
		return
	}
	if runtime.GOOS == "windows" {
		return
	}
	out, err := exec.Command("ps", "-ax", "-o", "pid=,command=").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, bin) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 1 {
			continue
		}
		_ = exec.Command("kill", strconv.Itoa(pid)).Run()
	}
}
