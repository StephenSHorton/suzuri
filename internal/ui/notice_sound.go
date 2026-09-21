//go:build windows || darwin

package ui

import (
	"encoding/binary"
	"math"
)

func playNoticeSound(name string, urgency int) {
	if name == "silent" {
		return
	}
	kind := name
	switch kind {
	case "", "system":
		kind = "info"
		if urgency >= 2 {
			kind = "error"
		}
	case "warning":
		kind = "warn"
	}
	playWAV(synthTweet(kind))
}

func synthTweet(kind string) []byte {
	const rate = 44100
	n := rate * 130 / 1000
	f0, f1 := 880.0, 1480.0
	switch kind {
	case "error":
		f0, f1 = 620, 280
		n = rate * 150 / 1000
	case "warn":
		f0, f1 = 740, 980
	case "question":
		f0, f1 = 988, 1318
	}
	pcm := make([]int16, n)
	for i := range pcm {
		t := float64(i) / float64(len(pcm))
		freq := f0 + (f1-f0)*t
		env := 1.0
		if t < 0.04 {
			env = t / 0.04
		} else if t > 0.62 {
			env = (1 - t) / 0.38
		}
		phase := 2 * math.Pi * freq * float64(i) / float64(rate)
		s := math.Sin(phase)*0.62 + math.Sin(phase*2)*0.18
		pcm[i] = int16(s * env * 28000)
	}
	return wavPCM(pcm, rate)
}

func wavPCM(pcm []int16, rate int) []byte {
	dataBytes := len(pcm) * 2
	buf := make([]byte, 44+dataBytes)
	copy(buf[0:], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], uint32(36+dataBytes))
	copy(buf[8:], "WAVE")
	copy(buf[12:], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1)
	binary.LittleEndian.PutUint16(buf[22:], 1)
	binary.LittleEndian.PutUint32(buf[24:], uint32(rate))
	binary.LittleEndian.PutUint32(buf[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(buf[32:], 2)
	binary.LittleEndian.PutUint16(buf[34:], 16)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], uint32(dataBytes))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(buf[44+i*2:], uint16(s))
	}
	return buf
}
