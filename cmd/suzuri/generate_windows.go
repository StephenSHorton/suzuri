//go:build windows

package main

// Regenerate PE icon resources after changing assets/icon/suzuri.ico:
//
//	go generate ./cmd/suzuri
//
//go:generate rsrc -arch amd64 -ico ../../assets/icon/suzuri.ico -o rsrc_windows_amd64.syso
//go:generate rsrc -arch arm64 -ico ../../assets/icon/suzuri.ico -o rsrc_windows_arm64.syso
