//go:build windows

package host

import (
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsChildForeground is not a process group. ConPTY has no POSIX
// foreground pgid. tab.childOwnsTTY treats any positive ForegroundPGID other
// than the shell pid as "a program has the terminal". This sentinel means
// some other console process is attached. It is not a real pid: 0 is Idle
// and 4 is System, so 1 does not collide with a live shell. If it ever does,
// windowsForegroundID returns a different stable value so the comparison holds.
const windowsChildForeground = 1

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole         = kernel32.NewProc("AttachConsole")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	// AttachConsole is process-wide. Tabs must not interleave it.
	consoleAttachMu sync.Mutex
)

// ForegroundPGID reports whether a console program other than the shell is
// attached to this pseudoconsole. There is no Windows foreground group:
// 0 means only the shell is there (the Warp bar keeps keys); a stable
// non-zero sentinel other than Pid means a child should take keystrokes.
func (s *Session) ForegroundPGID() int {
	if s == nil {
		return 0
	}
	shell := s.Pid()
	if shell <= 0 {
		return 0
	}
	pids, ok := pseudoconsoleProcessList(uint32(shell))
	if !ok {
		// Host already has a console, or the shell has no console yet.
		// Descendants of the shell are the same "extra process" signal.
		pids = descendantPIDs(uint32(shell))
	}
	return windowsForegroundID(shell, programOwnsConsoleKeys(pids, shell))
}

// programOwnsConsoleKeys is the pure key-owner decision. pids is
// GetConsoleProcessList for the pseudoconsole (host pid already removed)
// or the shell's descendant pids. One pid other than the shell means a
// foreground program is waiting. Only the shell, an empty list, or an
// unknown shell pid leaves keys with the bar.
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

// pseudoconsoleProcessList is GetConsoleProcessList for the shell's console.
// The host briefly attaches, because that API only sees the caller's console.
// The returned list omits this process (it shows up only while attached).
// ok is false when we must not attach (already on a console) or the query failed.
func pseudoconsoleProcessList(shell uint32) ([]uint32, bool) {
	if shell == 0 {
		return nil, false
	}
	consoleAttachMu.Lock()
	defer consoleAttachMu.Unlock()
	if processHasConsole() {
		return nil, false
	}
	r, _, _ := procAttachConsole.Call(uintptr(shell))
	if r == 0 {
		return nil, false
	}
	defer procFreeConsole.Call()
	pids := getConsoleProcessList()
	if len(pids) == 0 {
		return nil, false
	}
	return dropPID(pids, uint32(os.Getpid())), true
}

// processHasConsole reports whether this process is already a console client.
// A zero-length query is invalid; one slot is enough to see success. Do not
// FreeConsole here — that would drop a real parent console.
func processHasConsole() bool {
	var one uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&one)), 1)
	return n != 0
}

func getConsoleProcessList() []uint32 {
	buf := make([]uint32, 16)
	for range 4 {
		n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		count := int(n)
		if count <= 0 {
			return nil
		}
		if count <= len(buf) {
			out := make([]uint32, count)
			copy(out, buf[:count])
			return out
		}
		buf = make([]uint32, count)
	}
	return nil
}

func dropPID(pids []uint32, skip uint32) []uint32 {
	if skip == 0 {
		return pids
	}
	out := make([]uint32, 0, len(pids))
	for _, pid := range pids {
		if pid != skip {
			out = append(out, pid)
		}
	}
	return out
}

type procLink struct {
	pid  uint32
	ppid uint32
}

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
// not including the shell. Used when GetConsoleProcessList cannot run.
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
