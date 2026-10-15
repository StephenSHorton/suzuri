//go:build windows

package host

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsChildForeground is not a process group. ConPTY has no POSIX
// foreground pgid. tab.childOwnsTTY treats any positive ForegroundPGID other
// than the shell pid as "a program has the terminal". This sentinel means
// the shell has a live child. It is not a real pid: 0 is Idle and 4 is
// System, so 1 does not collide with a live shell. If it ever does,
// windowsForegroundID returns a different stable value so the comparison holds.
const windowsChildForeground = 1

// fgPollEvery is how often the background probe re-checks the shell's
// children. The UI asks many times per frame; it only ever reads the cache.
const fgPollEvery = 200 * time.Millisecond

// ForegroundPGID reports whether a program other than the shell is running
// under this pseudoconsole. There is no Windows foreground group:
// 0 means only the shell is there (the Warp bar keeps keys); a stable
// non-zero sentinel other than Pid means a child should take keystrokes.
//
// The UI calls this from paint, key, and PTY-ingest paths, so it must never
// block. It returns the last value from a background probe. The probe used to
// run inline and AttachConsole to the pseudoconsole: with an inline TUI such
// as Claude Code streaming output, that call waited on conhost while conhost
// waited on the UI thread to drain the pipe, and the window froze. Attaching
// also made this process a console client, so a ^C could reach it.
func (s *Session) ForegroundPGID() int {
	if s == nil {
		return 0
	}
	shell := s.Pid()
	if shell <= 0 {
		return 0
	}
	s.fgOnce.Do(func() { go s.pollForeground(shell) })
	return int(s.fgID.Load())
}

// pollForeground refreshes fgID until the session closes.
func (s *Session) pollForeground(shell int) {
	t := time.NewTicker(fgPollEvery)
	defer t.Stop()
	for {
		pids := descendantPIDs(uint32(shell))
		s.fgID.Store(int32(windowsForegroundID(shell, programOwnsConsoleKeys(pids, shell))))
		select {
		case <-s.closed:
			s.fgID.Store(0)
			return
		case <-t.C:
		}
	}
}

// programOwnsConsoleKeys is the pure key-owner decision. pids are the
// shell's descendant pids. One pid other than the shell means a foreground
// program is waiting. Only the shell, an empty list, or an unknown shell pid
// leaves keys with the bar.
func programOwnsConsoleKeys(pids []uint32, shell int) bool {
	if shell <= 0 {
		return false
	}
	for _, pid := range pids {
		if pid == 0 || int(pid) == shell {
			continue
		}
		return true
	}
	return false
}

// windowsForegroundID maps that decision onto the positive-id contract
// childOwnsTTY already uses. The same shell always gets the same sentinel.
func windowsForegroundID(shell int, program bool) int {
	if !program || shell <= 0 {
		return 0
	}
	if shell != windowsChildForeground {
		return windowsChildForeground
	}
	return windowsChildForeground + 1
}

type procLink struct {
	pid  uint32
	ppid uint32
}

// descendantPIDs walks a Toolhelp snapshot. It talks to the kernel only,
// never to the pseudoconsole's conhost.
func descendantPIDs(shell uint32) []uint32 {
	if shell == 0 {
		return nil
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var all []procLink
	err = windows.Process32First(snap, &pe)
	for err == nil {
		all = append(all, procLink{pid: pe.ProcessID, ppid: pe.ParentProcessID})
		err = windows.Process32Next(snap, &pe)
	}
	return descendantPIDsFrom(all, shell)
}

// descendantPIDsFrom returns processes whose parent chain reaches shell,
// not including the shell.
func descendantPIDsFrom(all []procLink, shell uint32) []uint32 {
	if shell == 0 {
		return nil
	}
	kids := make(map[uint32][]uint32, len(all))
	for _, l := range all {
		if l.pid == 0 {
			continue
		}
		kids[l.ppid] = append(kids[l.ppid], l.pid)
	}
	var out []uint32
	queue := append([]uint32(nil), kids[shell]...)
	seen := map[uint32]bool{shell: true}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if pid == 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
		queue = append(queue, kids[pid]...)
	}
	return out
}
