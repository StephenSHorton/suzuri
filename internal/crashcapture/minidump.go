package crashcapture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Minidump layout (minidumpapiset.h): MINIDUMP_HEADER is
//
//	Signature 'MDMP', Version, NumberOfStreams, StreamDirectoryRva, ...
//
// The directory is NumberOfStreams entries of {StreamType, DataSize, Rva}.
// ExceptionStream (6) holds MINIDUMP_EXCEPTION_STREAM: ThreadId,
// __alignment, then MINIDUMP_EXCEPTION whose first field is ExceptionCode.
const (
	mdmpSignature   = 0x504d444d // "MDMP" little-endian
	exceptionStream = 6
	maxStreams      = 4096
)

// ErrNoException means the dump has no exception stream (a dump taken by
// hand, not by a crash).
var ErrNoException = errors.New("minidump has no exception stream")

// ReadExceptionCode returns the exception code (NTSTATUS) recorded in a
// Windows minidump.
func ReadExceptionCode(path string) (uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return exceptionCode(f)
}

func exceptionCode(r io.ReaderAt) (uint32, error) {
	var hdr [16]byte
	if _, err := r.ReadAt(hdr[:], 0); err != nil {
		return 0, fmt.Errorf("minidump header: %w", err)
	}
	le := binary.LittleEndian
	if le.Uint32(hdr[0:]) != mdmpSignature {
		return 0, errors.New("not a minidump (no MDMP signature)")
	}
	n := le.Uint32(hdr[8:])
	dirRVA := int64(le.Uint32(hdr[12:]))
	if n > maxStreams {
		return 0, fmt.Errorf("minidump claims %d streams", n)
	}
	var ent [12]byte
	for i := int64(0); i < int64(n); i++ {
		if _, err := r.ReadAt(ent[:], dirRVA+i*12); err != nil {
			return 0, fmt.Errorf("minidump directory: %w", err)
		}
		if le.Uint32(ent[0:]) != exceptionStream {
			continue
		}
		size := le.Uint32(ent[4:])
		rva := int64(le.Uint32(ent[8:]))
		if size < 12 {
			return 0, errors.New("minidump exception stream too short")
		}
		var code [4]byte
		if _, err := r.ReadAt(code[:], rva+8); err != nil {
			return 0, fmt.Errorf("minidump exception: %w", err)
		}
		return le.Uint32(code[:]), nil
	}
	return 0, ErrNoException
}

// StatusName names the NTSTATUS codes a crashing suzuri is likely to show.
func StatusName(code uint32) string {
	switch code {
	case 0xC0000005:
		return "STATUS_ACCESS_VIOLATION"
	case 0xC0000006:
		return "STATUS_IN_PAGE_ERROR"
	case 0xC000001D:
		return "STATUS_ILLEGAL_INSTRUCTION"
	case 0xC0000094:
		return "STATUS_INTEGER_DIVIDE_BY_ZERO"
	case 0xC00000FD:
		return "STATUS_STACK_OVERFLOW"
	case 0xC0000374:
		return "STATUS_HEAP_CORRUPTION"
	case 0xC0000409:
		return "STATUS_STACK_BUFFER_OVERRUN"
	case 0xC0000602:
		return "STATUS_FAIL_FAST_EXCEPTION"
	case 0x80000003:
		return "STATUS_BREAKPOINT"
	case 0xE06D7363:
		return "C++ exception"
	case 2:
		return "Go fatal error (exit 2)"
	}
	return ""
}
