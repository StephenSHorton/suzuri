package chromehost

import (
	"strings"
	"testing"
)

func TestWithHostEnvInjectsTransferBin(t *testing.T) {
	env := withHostEnv(
		[]string{"PATH=C:\\Windows"},
		`C:\cfg`,
		"0.9.131",
		`C:\app\suzuri-transfer.exe`,
	)
	got := map[string]string{}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			t.Fatalf("bad env entry %q", e)
		}
		got[k] = v
	}
	if got["SUZURI_CONFIG_DIR"] != `C:\cfg` {
		t.Fatalf("config dir: %q", got["SUZURI_CONFIG_DIR"])
	}
	if got["SUZURI_VERSION"] != "0.9.131" {
		t.Fatalf("version: %q", got["SUZURI_VERSION"])
	}
	if got[EnvTransferBin] != `C:\app\suzuri-transfer.exe` {
		t.Fatalf("transfer bin: %q", got[EnvTransferBin])
	}
	if got["PATH"] != `C:\Windows` {
		t.Fatalf("PATH dropped: %q", got["PATH"])
	}
}

func TestWithHostEnvKeepsExplicitTransferBin(t *testing.T) {
	env := withHostEnv(
		[]string{"SUZURI_TRANSFER_BIN=C:\\override\\suzuri-transfer.exe"},
		"cfg",
		"dev",
		`C:\app\suzuri-transfer.exe`,
	)
	got := ""
	for _, e := range env {
		if strings.HasPrefix(e, EnvTransferBin+"=") {
			got = strings.TrimPrefix(e, EnvTransferBin+"=")
		}
	}
	if got != `C:\override\suzuri-transfer.exe` {
		t.Fatalf("explicit env should win, got %q", got)
	}
}

func TestWithHostEnvSkipsEmptyTransferBin(t *testing.T) {
	env := withHostEnv(nil, "cfg", "dev", "")
	for _, e := range env {
		if strings.HasPrefix(e, EnvTransferBin+"=") {
			t.Fatalf("empty transfer bin should not be injected: %q", e)
		}
	}
}
