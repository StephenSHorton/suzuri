package workspace

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestClaimExclusive(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	a, err := s.Join("alpha", KindAgent, "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Join("bravo", KindAgent, "sess-b")
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask("write main.js", "", []string{"js/main.js"}, "general")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "E1" || task.Status != TaskTodo || task.Owner != "" {
		t.Fatalf("create: %+v", task)
	}

	claimed, err := s.ClaimTask(task.ID, a.ID, "general")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != TaskClaimed || claimed.Owner != a.ID {
		t.Fatalf("claim: %+v", claimed)
	}
	if claimed.Owner == "engine" {
		t.Fatal("owner must be member_id, not engine")
	}

	_, err = s.ClaimTask(task.ID, b.ID, "general")
	if err == nil {
		t.Fatal("expected second claim to fail")
	}
	if !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("error: %v", err)
	}

	// Same owner re-claim is idempotent.
	again, err := s.ClaimTask(task.ID, a.ID, "general")
	if err != nil || again.Owner != a.ID {
		t.Fatalf("reclaim: %+v %v", again, err)
	}

	hist, err := s.History("general", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !historyHas(hist, "system", "task E1 created") {
		t.Fatalf("missing create system line: %+v", hist)
	}
	if !historyHas(hist, "system", "task E1 claimed by @"+a.ID) {
		t.Fatalf("missing claim system line: %+v", hist)
	}
}

func TestAssignSetsOwnerMemberID(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	m, err := s.Join("implementer", KindAgent, "sess-assign")
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask("review auth", "C1", nil, "general")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "C1" {
		t.Fatalf("explicit id: %+v", task)
	}

	// Reject the string "engine".
	if _, err := s.AssignTask(task.ID, "engine", "general"); err == nil {
		t.Fatal("expected engine owner to be rejected")
	}

	got, err := s.AssignTask(task.ID, m.ID, "general")
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != m.ID {
		t.Fatalf("owner=%q want member_id %q", got.Owner, m.ID)
	}
	if got.Owner == "engine" {
		t.Fatal("owner must not be engine")
	}
	if got.Status != TaskClaimed {
		t.Fatalf("status=%q want claimed", got.Status)
	}

	list, err := s.ListTasks()
	if err != nil || len(list) != 1 || list[0].Owner != m.ID {
		t.Fatalf("list: %+v %v", list, err)
	}

	hist, err := s.History("general", 50)
	if err != nil {
		t.Fatal(err)
	}
	mention := "@" + m.ID
	if !historyHas(hist, "system", "task C1 assigned to "+mention) {
		t.Fatalf("assign should record mention %s: %+v", mention, hist)
	}

	// Apply op path
	r := Apply(s, Request{Op: OpTaskAssign, TaskID: "C1", MemberID: m.ID})
	if !r.OK || r.Task == nil || r.Task.Owner != m.ID {
		t.Fatalf("apply assign: %+v", r)
	}
}

func TestTaskAutoIDAndStatusSystem(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "ws"))
	m, err := s.Join("bot", KindAgent, "sess-id")
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.CreateTask("one", "", nil, "")
	if err != nil || t1.ID != "E1" {
		t.Fatalf("E1: %+v %v", t1, err)
	}
	t2, err := s.CreateTask("two", "", nil, "")
	if err != nil || t2.ID != "E2" {
		t.Fatalf("E2: %+v %v", t2, err)
	}
	if _, err := s.CreateTask("dup", "E1", nil, ""); err == nil {
		t.Fatal("expected duplicate id error")
	}
	if _, err := s.ClaimTask("E1", m.ID, ""); err != nil {
		t.Fatal(err)
	}
	done, err := s.SetTaskStatus("E1", TaskDone, "general")
	if err != nil || done.Status != TaskDone {
		t.Fatalf("done: %+v %v", done, err)
	}
	blocked, err := s.SetTaskStatus("E2", TaskBlocked, "general")
	if err != nil || blocked.Status != TaskBlocked {
		t.Fatalf("blocked: %+v %v", blocked, err)
	}
	hist, _ := s.History("general", 50)
	if !historyHas(hist, "system", "task E1 done") {
		t.Fatalf("missing done system: %+v", hist)
	}
	if !historyHas(hist, "system", "task E2 blocked") {
		t.Fatalf("missing blocked system: %+v", hist)
	}

	r := Apply(s, Request{Op: OpTaskList})
	if !r.OK || r.Count != 2 || len(r.Tasks) != 2 {
		t.Fatalf("list apply: %+v", r)
	}
}

func historyHas(hist []Message, kind, substr string) bool {
	for _, m := range hist {
		if m.Kind == kind && strings.Contains(m.Body, substr) {
			return true
		}
	}
	return false
}
