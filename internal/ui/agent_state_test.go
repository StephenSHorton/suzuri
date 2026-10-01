//go:build windows || darwin

package ui

import (
	"testing"

	"github.com/hinshun/vt10x"
)

func TestReportAgentSetsBlockedOnThePane(t *testing.T) {
	pane := &tab{id: 3, title: "grok", sb: newScrollback()}
	pane.alive.Store(true)
	pane.term = vt10x.New(vt10x.WithSize(80, 24))
	pane.titleBusy.Store(true)
	pg := newPage(pane)
	pages := []*page{pg}
	active := 0
	s := aiSurface{pages: &pages, active: &active, tabs: []*tab{pane}}

	_, status, err := applyAICall(s, "report_agent", map[string]any{
		"pane_id": 3,
		"state":   "blocked",
	})
	if err != "" || status != 200 {
		t.Fatalf("report: status=%d err=%s", status, err)
	}
	if !pane.blocked() || pane.busy() || pg.anyBusy() || !pg.anyBlocked() {
		t.Fatal("blocked report should roll up ahead of the title-spinner guess")
	}
	v, ok := paneJSON(s, 3, 4)
	if !ok {
		t.Fatal("pane missing")
	}
	if v["agent_state"] != "blocked" || v["activity"] != "blocked" {
		t.Fatalf("snapshot=%v", v)
	}

	_, status, err = applyAICall(s, "report_agent", map[string]any{
		"pane_id": 3,
		"state":   "nope",
	})
	if status != 400 || err == "" {
		t.Fatalf("bad state: status=%d err=%s", status, err)
	}
	if !pane.blocked() {
		t.Fatal("rejected report must leave the previous state")
	}
}
