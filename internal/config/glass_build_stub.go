//go:build !windows

package config

func probeWindowsBuildNumber() uint32 { return 0 }
