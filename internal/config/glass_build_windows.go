//go:build windows

package config

import "golang.org/x/sys/windows"

func probeWindowsBuildNumber() uint32 {
	info := windows.RtlGetVersion()
	if info == nil {
		return 0
	}
	return info.BuildNumber
}
