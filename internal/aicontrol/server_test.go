package aicontrol

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHelpAndToolsNoAuth(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	s, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := http.Get(s.URL + "/help")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), "SUZURI_CONTROL_TOKEN") {
		t.Fatalf("help: %s", b)
	}
	res2, err := http.Get(s.URL + "/tools")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(res2.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	tools, _ := payload["tools"].([]any)
	if len(tools) < 5 {
		t.Fatalf("tools=%v", payload)
	}
}

func TestLayoutRequiresToken(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	s, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := http.Get(s.URL + "/v1/layout")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestCallRoundtrip(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	s, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			job, ok := s.TryRecv()
			if !ok {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if job.Kind != KindCall || job.Tool != "layout" {
				job.Reply <- JSONErr(400, "unexpected")
				return
			}
			job.Reply <- JSONOK(map[string]any{"ok": true, "pid": 1})
			return
		}
	}()
	body, _ := json.Marshal(map[string]any{"tool": "layout", "args": map[string]any{}})
	req, _ := http.NewRequest(http.MethodPost, s.URL+"/v1/call", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d %s", res.StatusCode, b)
	}
	<-done
	env := PtyEnv()
	if len(env) != 2 {
		t.Fatalf("pty env %v", env)
	}
	path := filepath.Join(os.Getenv("LOCALAPPDATA"), "suzuri", "ai.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
