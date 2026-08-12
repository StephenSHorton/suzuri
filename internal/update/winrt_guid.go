package update

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"strings"
)

// rfc4122 name-space for WinRT parameterized interface IIDs.
// https://learn.microsoft.com/en-us/uwp/winrt-cref/winrt-type-system
var winrtPInterfaceNS = guid{
	data1: 0x11f47ad5,
	data2: 0x7b73,
	data3: 0x42c0,
	data4: [8]byte{0xab, 0xae, 0x87, 0x8b, 0x1e, 0x16, 0xad, 0xee},
}

type guid struct {
	data1 uint32
	data2 uint16
	data3 uint16
	data4 [8]byte
}

func (g guid) string() string {
	return formatGUID(g)
}

func formatGUID(g guid) string {
	var buf [36]byte
	hex.Encode(buf[0:8], []byte{
		byte(g.data1 >> 24), byte(g.data1 >> 16), byte(g.data1 >> 8), byte(g.data1),
	})
	buf[8] = '-'
	hex.Encode(buf[9:13], []byte{byte(g.data2 >> 8), byte(g.data2)})
	buf[13] = '-'
	hex.Encode(buf[14:18], []byte{byte(g.data3 >> 8), byte(g.data3)})
	buf[18] = '-'
	hex.Encode(buf[19:23], g.data4[0:2])
	buf[23] = '-'
	hex.Encode(buf[24:36], g.data4[2:8])
	return string(buf[:])
}

func parseGUID(s string) guid {
	s = strings.Trim(s, "{}")
	s = strings.ReplaceAll(s, "-", "")
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 16 {
		return guid{}
	}
	return guid{
		data1: binary.BigEndian.Uint32(raw[0:4]),
		data2: binary.BigEndian.Uint16(raw[4:6]),
		data3: binary.BigEndian.Uint16(raw[6:8]),
		data4: [8]byte{raw[8], raw[9], raw[10], raw[11], raw[12], raw[13], raw[14], raw[15]},
	}
}

func (g guid) putNetwork(b []byte) {
	binary.BigEndian.PutUint32(b[0:4], g.data1)
	binary.BigEndian.PutUint16(b[4:6], g.data2)
	binary.BigEndian.PutUint16(b[6:8], g.data3)
	copy(b[8:16], g.data4[:])
}

func guidFromNetwork(b []byte) guid {
	var d4 [8]byte
	copy(d4[:], b[8:16])
	return guid{
		data1: binary.BigEndian.Uint32(b[0:4]),
		data2: binary.BigEndian.Uint16(b[4:6]),
		data3: binary.BigEndian.Uint16(b[6:8]),
		data4: d4,
	}
}

// pinterfaceGUID is the WinRT v5 UUID for a parameterized interface signature.
func pinterfaceGUID(signature string) guid {
	var ns [16]byte
	winrtPInterfaceNS.putNetwork(ns[:])
	h := sha1.New()
	_, _ = h.Write(ns[:])
	_, _ = h.Write([]byte(signature))
	sum := h.Sum(nil)
	// RFC 4122 version 5 + variant
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return guidFromNetwork(sum[:16])
}

func rcSig(runtimeClass string, defaultIID guid) string {
	return "rc(" + runtimeClass + ";{" + defaultIID.string() + "})"
}

func pifaceSig(openIID guid, arg string) string {
	return "pinterface({" + openIID.string() + "};" + arg + ")"
}
