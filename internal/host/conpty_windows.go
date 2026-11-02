//go:build windows

package host

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/applog"
)

// conPTY is a pseudoconsole with its shell, built on x/sys/windows so each
// handle can be released on its own (github.com/UserExistsError/conpty's Close
// tears the console, pipes and process down in one call, which the session
// actor's ordered close cannot use). It implements ptyBackend; only the actor
// goroutine calls those methods. read is the one exception: the session's
// reader goroutine calls it.
type conPTY struct {
	hpc    windows.Handle
	proc   windows.Handle
	thread windows.Handle
	pid    uint32
	in     windows.Handle // our end of the console's input pipe
	out    windows.Handle // our end of the console's output pipe

	// readMu: read holds it shared across ReadFile; ClosePipes takes it
	// exclusively, so the output handle is never closed (and its value never
	// reused) under a live ReadFile.
	readMu    sync.RWMutex
	outClosed bool
}

// startConPTY creates a pseudoconsole of cols×rows and starts commandLine on it.
// flags are CreatePseudoConsole flags (0 = none).
func startConPTY(commandLine string, cols, rows int, workDir string, env []string, flags uint32) (*conPTY, error) {
	var ptyIn, cmdIn, cmdOut, ptyOut windows.Handle
	if err := windows.CreatePipe(&ptyIn, &cmdIn, nil, 0); err != nil {
		return nil, fmt.Errorf("CreatePipe: %w", err)
	}
	if err := windows.CreatePipe(&cmdOut, &ptyOut, nil, 0); err != nil {
		closeAll(ptyIn, cmdIn)
		return nil, fmt.Errorf("CreatePipe: %w", err)
	}
	var hpc windows.Handle
	size := windows.Coord{X: int16(cols), Y: int16(rows)}
	if err := windows.CreatePseudoConsole(size, ptyIn, ptyOut, flags, &hpc); err != nil {
		closeAll(ptyIn, cmdIn, cmdOut, ptyOut)
		return nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}
	// The console holds its own copies of its pipe ends. Closing ours now means
	// the output pipe reaches EOF when the console goes away, so the reader
	// never hangs after the shell dies (v0.1.4 kept them open until Close).
	closeAll(ptyIn, ptyOut)

	pi, err := spawnOnConsole(hpc, commandLine, workDir, env)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		closeAll(cmdIn, cmdOut)
		return nil, err
	}
	return &conPTY{
		hpc:    hpc,
		proc:   pi.Process,
		thread: pi.Thread,
		pid:    pi.ProcessId,
		in:     cmdIn,
		out:    cmdOut,
	}, nil
}

func spawnOnConsole(hpc windows.Handle, commandLine, workDir string, env []string) (*windows.ProcessInformation, error) {
	cmdLine, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return nil, err
	}
	var dir *uint16
	if workDir != "" {
		if dir, err = windows.UTF16PtrFromString(workDir); err != nil {
			return nil, err
		}
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, fmt.Errorf("attribute list: %w", err)
	}
	defer attrs.Delete()
	// The attribute value is the HPCON itself, not a pointer to it.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&hpc)), unsafe.Sizeof(hpc)); err != nil {
		return nil, fmt.Errorf("attribute pseudoconsole: %w", err)
	}
	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	// Null std handles plus USESTDHANDLES: the child must not inherit ours.
	si.Flags |= windows.STARTF_USESTDHANDLES
	si.ProcThreadAttributeList = attrs.List()
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT)
	var envBlock *uint16
	if env != nil {
		flags |= windows.CREATE_UNICODE_ENVIRONMENT
		envBlock = envBlockUTF16(env)
	}
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cmdLine, nil, nil, false, flags, envBlock, dir, &si.StartupInfo, &pi); err != nil {
		return nil, fmt.Errorf("CreateProcess: %w", err)
	}
	return &pi, nil
}

// envBlockUTF16 builds a CREATE_UNICODE_ENVIRONMENT block: KEY=VAL\0...\0\0.
// (v0.1.4 widened each byte separately, which garbled non-ASCII values.)
func envBlockUTF16(env []string) *uint16 {
	var b []uint16
	for _, e := range env {
		b = append(b, utf16.Encode([]rune(e))...)
		b = append(b, 0)
	}
	if len(b) == 0 {
		b = append(b, 0)
	}
	b = append(b, 0)
	return &b[0]
}

func closeAll(hs ...windows.Handle) {
	for _, h := range hs {
		if h != 0 && h != windows.InvalidHandle {
			_ = windows.CloseHandle(h)
		}
	}
}

// read is ReadFile on the output pipe, with EOF for a closed or broken pipe.
func (c *conPTY) read(p []byte) (int, error) {
	c.readMu.RLock()
	defer c.readMu.RUnlock()
	if c.outClosed {
		return 0, io.EOF
	}
	var n uint32
	err := windows.ReadFile(c.out, p, &n, nil)
	if err != nil && (errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_OPERATION_ABORTED)) {
		err = io.EOF
	}
	return int(n), err
}

// waitProcess reports the shell's exit to the actor. The process handle stays
// open until ReleaseProcess, which the actor calls only after this returns.
func (c *conPTY) waitProcess(a *sessionActor) {
	_, _ = windows.WaitForSingleObject(c.proc, windows.INFINITE)
	var code uint32
	_ = windows.GetExitCodeProcess(c.proc, &code)
	a.ProcessExited(code)
}

// ptyBackend, called only on the actor goroutine.

func (c *conPTY) Write(p []byte) (int, error) {
	var n uint32
	err := windows.WriteFile(c.in, p, &n, nil)
	return int(n), err
}

func (c *conPTY) Resize(cols, rows int) error {
	if c.hpc == 0 {
		return errSessionClosed
	}
	return windows.ResizePseudoConsole(c.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (c *conPTY) CloseConsole() {
	if c.hpc == 0 {
		return
	}
	applog.Trail("conpty.Close enter", "pid", c.pid)
	windows.ClosePseudoConsole(c.hpc)
	c.hpc = 0
	applog.Trail("conpty.Close leave", "pid", c.pid)
}

func (c *conPTY) CancelRead() { _ = windows.CancelIoEx(c.out, nil) }

// pipeCloseWait bounds how long ClosePipes waits for a reader still inside
// ReadFile before it leaves the output handle open rather than close it under
// the read.
const pipeCloseWait = time.Second

func (c *conPTY) ClosePipes() {
	closeAll(c.in)
	c.in = 0
	deadline := time.Now().Add(pipeCloseWait)
	for !c.readMu.TryLock() {
		if time.Now().After(deadline) {
			applog.Trail("conpty pipes: reader still in ReadFile; output handle left open", "pid", c.pid)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	closeAll(c.out)
	c.outClosed = true
	c.readMu.Unlock()
}

func (c *conPTY) KillProcess() { _ = windows.TerminateProcess(c.proc, 1) }

func (c *conPTY) ReleaseProcess() {
	closeAll(c.proc, c.thread)
	c.proc, c.thread = 0, 0
}
