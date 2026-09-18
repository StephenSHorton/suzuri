package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

const tasksFileName = "tasks.json"

// TaskStatus is the lifecycle of a claimable workspace task.
type TaskStatus string

const (
	TaskTodo    TaskStatus = "todo"
	TaskClaimed TaskStatus = "claimed"
	TaskDone    TaskStatus = "done"
	TaskBlocked TaskStatus = "blocked"
)

// Task is a first-class claimable unit of work (persisted in tasks.json).
// Owner is a member_id (never the string "engine").
type Task struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Owner  string     `json:"owner,omitempty"`
	Status TaskStatus `json:"status"`
	Files  []string   `json:"files,omitempty"`
}

// NormalizeTaskStatus maps free text to a known task status.
func NormalizeTaskStatus(s string) (TaskStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "todo", "open":
		return TaskTodo, nil
	case "claimed", "claim", "in_progress", "in-progress":
		return TaskClaimed, nil
	case "done", "complete", "completed":
		return TaskDone, nil
	case "blocked", "stuck":
		return TaskBlocked, nil
	default:
		return "", fmt.Errorf("invalid task status %q (todo|claimed|done|blocked)", s)
	}
}

// ListTasks returns all tasks (empty if tasks.json is missing).
func (s *Store) ListTasks() ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return nil, err
	}
	return s.readTasksLocked()
}

// CreateTask appends a task. id may be explicit (E7, C1) or empty to auto-assign E1, E2, …
// Posts a system line on channel (default #general).
func (s *Store) CreateTask(title, id string, files []string, channel string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return Task{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, fmt.Errorf("title required")
	}
	tasks, err := s.readTasksLocked()
	if err != nil {
		return Task{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = nextAutoTaskID(tasks)
	} else {
		if err := validateTaskID(id); err != nil {
			return Task{}, err
		}
		if findTask(tasks, id) >= 0 {
			return Task{}, fmt.Errorf("task %s already exists", id)
		}
	}
	t := Task{
		ID:     id,
		Title:  title,
		Status: TaskTodo,
		Files:  copyStrings(files),
	}
	tasks = append(tasks, t)
	if err := s.writeTasksLocked(tasks); err != nil {
		return Task{}, err
	}
	_, _ = s.postSystemLocked(channel, fmt.Sprintf("task %s created: %s", t.ID, t.Title))
	return t, nil
}

// ClaimTask exclusively claims a task for memberID (a real member_id).
// A second claim by a different member fails. Same member is idempotent.
func (s *Store) ClaimTask(taskID, memberID, channel string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return Task{}, err
	}
	memberID, err := s.requireMemberIDLocked(memberID)
	if err != nil {
		return Task{}, err
	}
	tasks, err := s.readTasksLocked()
	if err != nil {
		return Task{}, err
	}
	i := findTask(tasks, taskID)
	if i < 0 {
		return Task{}, fmt.Errorf("task %q not found", strings.TrimSpace(taskID))
	}
	t := tasks[i]
	if t.Status == TaskDone {
		return Task{}, fmt.Errorf("task %s is done", t.ID)
	}
	if t.Status == TaskClaimed && t.Owner != "" && t.Owner != memberID {
		return Task{}, fmt.Errorf("task %s already claimed by %s", t.ID, t.Owner)
	}
	if t.Owner == memberID && t.Status == TaskClaimed {
		return t, nil
	}
	t.Owner = memberID
	t.Status = TaskClaimed
	tasks[i] = t
	if err := s.writeTasksLocked(tasks); err != nil {
		return Task{}, err
	}
	_, _ = s.postSystemLocked(channel, fmt.Sprintf("task %s claimed by @%s", t.ID, memberID))
	return t, nil
}

// AssignTask sets owner to memberID and status to claimed (coordinator assign).
// Records a mention of that member_id on the system line.
func (s *Store) AssignTask(taskID, memberID, channel string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return Task{}, err
	}
	memberID, err := s.requireMemberIDLocked(memberID)
	if err != nil {
		return Task{}, err
	}
	tasks, err := s.readTasksLocked()
	if err != nil {
		return Task{}, err
	}
	i := findTask(tasks, taskID)
	if i < 0 {
		return Task{}, fmt.Errorf("task %q not found", strings.TrimSpace(taskID))
	}
	t := tasks[i]
	if t.Owner == memberID && t.Status == TaskClaimed {
		return t, nil
	}
	t.Owner = memberID
	t.Status = TaskClaimed
	tasks[i] = t
	if err := s.writeTasksLocked(tasks); err != nil {
		return Task{}, err
	}
	_, _ = s.postSystemLocked(channel, fmt.Sprintf("task %s assigned to @%s", t.ID, memberID))
	return t, nil
}

// SetTaskStatus updates a task's status (todo|claimed|done|blocked).
// claimed requires an existing owner. Posts a system line on done/blocked.
func (s *Store) SetTaskStatus(taskID string, status TaskStatus, channel string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return Task{}, err
	}
	st, err := NormalizeTaskStatus(string(status))
	if err != nil {
		return Task{}, err
	}
	tasks, err := s.readTasksLocked()
	if err != nil {
		return Task{}, err
	}
	i := findTask(tasks, taskID)
	if i < 0 {
		return Task{}, fmt.Errorf("task %q not found", strings.TrimSpace(taskID))
	}
	t := tasks[i]
	if st == TaskClaimed && t.Owner == "" {
		return Task{}, fmt.Errorf("task %s has no owner; claim or assign first", t.ID)
	}
	if t.Status == st {
		return t, nil
	}
	t.Status = st
	tasks[i] = t
	if err := s.writeTasksLocked(tasks); err != nil {
		return Task{}, err
	}
	switch st {
	case TaskDone:
		_, _ = s.postSystemLocked(channel, fmt.Sprintf("task %s done", t.ID))
	case TaskBlocked:
		_, _ = s.postSystemLocked(channel, fmt.Sprintf("task %s blocked", t.ID))
	}
	return t, nil
}

func (s *Store) readTasksLocked() ([]Task, error) {
	var tasks []Task
	path := filepath.Join(s.rootLocked(), tasksFileName)
	if err := readJSON(path, &tasks); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Task{}, nil
		}
		return nil, err
	}
	if tasks == nil {
		tasks = []Task{}
	}
	return tasks, nil
}

func (s *Store) writeTasksLocked(tasks []Task) error {
	if tasks == nil {
		tasks = []Task{}
	}
	return writeJSON(filepath.Join(s.rootLocked(), tasksFileName), tasks)
}

func (s *Store) requireMemberIDLocked(memberID string) (string, error) {
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return "", fmt.Errorf("member_id required")
	}
	if strings.EqualFold(memberID, "engine") {
		return "", fmt.Errorf("owner must be a member_id, not %q", memberID)
	}
	members, err := s.readMembersLocked()
	if err != nil {
		return "", err
	}
	for _, m := range members {
		if m.ID == memberID {
			return memberID, nil
		}
	}
	return "", fmt.Errorf("member id not found; call workspace_join first")
}

func (s *Store) postSystemLocked(channel, body string) (Message, error) {
	channel = NormalizeChannel(channel)
	if channel == "" {
		channel = DefaultChannel
	}
	if err := s.ensureChannelLocked(channel, ""); err != nil {
		return Message{}, err
	}
	sys := Member{ID: "system", Name: "system", Kind: KindHuman}
	return s.postLocked(channel, sys, "system", body, "", nil)
}

func findTask(tasks []Task, id string) int {
	id = strings.TrimSpace(id)
	if id == "" {
		return -1
	}
	for i, t := range tasks {
		if t.ID == id {
			return i
		}
	}
	return -1
}

func nextAutoTaskID(tasks []Task) string {
	max := 0
	for _, t := range tasks {
		if len(t.ID) < 2 || (t.ID[0] != 'E' && t.ID[0] != 'e') {
			continue
		}
		n, err := strconv.Atoi(t.ID[1:])
		if err != nil || n <= 0 {
			continue
		}
		if n > max {
			max = n
		}
	}
	return fmt.Sprintf("E%d", max+1)
}

func validateTaskID(id string) error {
	if id == "" {
		return fmt.Errorf("invalid task id")
	}
	if len(id) > 32 {
		return fmt.Errorf("task id too long")
	}
	for i, r := range id {
		if i == 0 {
			if !unicode.IsLetter(r) {
				return fmt.Errorf("invalid task id %q", id)
			}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("invalid task id %q", id)
	}
	return nil
}

func copyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
