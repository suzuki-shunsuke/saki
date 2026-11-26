package task_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/suzuki-shunsuke/saki/pkg/task"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// setupTasks creates a tasks directory with a task of each kind.
func setupTasks(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "full", "task.yaml"), `name: full
description: a task with every key
type: implementation
status: doing
tags:
  - saki
claude_sessions:
  - id: abc
    description: wrote the overview
parent_task: parent
sub_tasks:
  - sub
derived_from: origin
derived_tasks:
  - derived
repositories:
  - https://github.com/suzuki-shunsuke/saki
issues:
  - https://github.com/suzuki-shunsuke/saki/issues/1
pull_requests:
  - https://github.com/suzuki-shunsuke/saki/pull/2
unknown_key: ignored
`)
	// A missing key and null are both empty.
	writeFile(t, filepath.Join(dir, "null", "task.yaml"), "name: null-task\ntags: null\n")
	writeFile(t, filepath.Join(dir, "empty", "task.yaml"), "")
	// Files directly under the tasks directory aren't tasks.
	writeFile(t, filepath.Join(dir, ".gitkeep"), "")
	// Directories whose task.yaml can't be read are failures.
	if err := os.MkdirAll(filepath.Join(dir, "missing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dir", "task.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "invalid", "task.yaml"), "name: [\n")
	writeFile(t, filepath.Join(dir, "wrong_list", "task.yaml"), "tags: foo\n")
	writeFile(t, filepath.Join(dir, "wrong_session", "task.yaml"), "claude_sessions:\n  - abc\n")
	return dir
}

// base is the modification time the tests give to the files, so that the order doesn't depend
// on when the files happen to be written.
var base = time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC) //nolint:gochecknoglobals

// setMtime sets the modification time of a file.
func setMtime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestRead(t *testing.T) {
	t.Parallel()
	dir := setupTasks(t)
	setMtime(t, filepath.Join(dir, "empty", "task.yaml"), base)
	setMtime(t, filepath.Join(dir, "full", "task.yaml"), base.Add(2*time.Minute))
	setMtime(t, filepath.Join(dir, "null", "task.yaml"), base)
	result, err := task.Read(dir)
	if err != nil {
		t.Fatal(err)
	}

	// The most recently updated first, and then by the directory names.
	wantTasks := []*task.Task{
		{
			Name:        "full",
			Description: "a task with every key",
			Type:        "implementation",
			Status:      "doing",
			Tags:        []string{"saki"},
			ClaudeSessions: []*task.ClaudeSession{
				{ID: "abc", Description: "wrote the overview"},
			},
			ParentTask:   "parent",
			SubTasks:     []string{"sub"},
			DerivedFrom:  "origin",
			DerivedTasks: []string{"derived"},
			Repositories: []string{"https://github.com/suzuki-shunsuke/saki"},
			Issues:       []string{"https://github.com/suzuki-shunsuke/saki/issues/1"},
			PullRequests: []string{"https://github.com/suzuki-shunsuke/saki/pull/2"},
			Dir:          "full",
			UpdatedAt:    base.Add(2 * time.Minute),
		},
		{Name: "", Dir: "empty", UpdatedAt: base},
		{Name: "null-task", Dir: "null", UpdatedAt: base},
	}
	if diff := cmp.Diff(wantTasks, result.Tasks); diff != "" {
		t.Errorf("Tasks (-want +got):\n%s", diff)
	}

	gotFailures := make([]string, 0, len(result.Failures))
	for _, f := range result.Failures {
		if f.Err == nil {
			t.Errorf("failure %s has no error", f.Dir)
		}
		gotFailures = append(gotFailures, f.Dir)
	}
	wantFailures := []string{"dir", "invalid", "missing", "wrong_list", "wrong_session"}
	if diff := cmp.Diff(wantFailures, gotFailures); diff != "" {
		t.Errorf("Failures (-want +got):\n%s", diff)
	}
}

func TestReadUpdatedAt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(path string, mtime time.Time) {
		t.Helper()
		writeFile(t, filepath.Join(dir, path), "name: x\n")
		setMtime(t, filepath.Join(dir, path), mtime)
	}
	// A file in a subdirectory counts.
	write("a/task.yaml", base)
	write("a/images/memo.md", base.Add(10*time.Minute))
	// Hidden files, files in hidden directories, and backups don't count.
	write("b/task.yaml", base.Add(5*time.Minute+time.Second))
	write("b/.DS_Store", base.Add(time.Hour))
	write("b/.hidden/memo.md", base.Add(time.Hour))
	write("b/details.md~", base.Add(time.Hour))
	// Updated later than b within the same minute: compared by the minute, b comes first by
	// its name. Compared exactly, c would come first.
	write("c/task.yaml", base.Add(5*time.Minute+50*time.Second))
	// A symlink doesn't count, even if its target is newer.
	write("d/task.yaml", base)
	write("outside.md", base.Add(time.Hour))
	if err := os.Symlink(filepath.Join(dir, "outside.md"), filepath.Join(dir, "d", "link.md")); err != nil {
		t.Fatal(err)
	}

	result, err := task.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	type got struct {
		Dir       string
		UpdatedAt time.Time
	}
	gotTasks := make([]got, 0, len(result.Tasks))
	for _, tk := range result.Tasks {
		gotTasks = append(gotTasks, got{Dir: tk.Dir, UpdatedAt: tk.UpdatedAt})
	}
	want := []got{
		{Dir: "a", UpdatedAt: base.Add(10 * time.Minute)},
		{Dir: "b", UpdatedAt: base.Add(5*time.Minute + time.Second)},
		{Dir: "c", UpdatedAt: base.Add(5*time.Minute + 50*time.Second)},
		{Dir: "d", UpdatedAt: base},
	}
	if diff := cmp.Diff(want, gotTasks); diff != "" {
		t.Errorf("tasks (-want +got):\n%s", diff)
	}
}

func TestReadUnreadableSubdir(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root can read a directory without permissions")
	}
	dir := t.TempDir()
	write := func(path string, mtime time.Time) {
		t.Helper()
		writeFile(t, filepath.Join(dir, path), "name: x\n")
		setMtime(t, filepath.Join(dir, path), mtime)
	}
	write("a/task.yaml", base)
	write("a/locked/memo.md", base.Add(time.Hour))
	// Walked after the unreadable directory, so it's counted only if the walk goes on.
	write("a/next/memo.md", base.Add(10*time.Minute))
	locked := filepath.Join(dir, "a", "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(locked, 0o755)
	})

	result, err := task.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The files in the unreadable directory aren't counted, and the task isn't skipped.
	if len(result.Tasks) != 1 || len(result.Failures) != 0 {
		t.Fatalf("Tasks = %d, Failures = %d, want 1 and 0", len(result.Tasks), len(result.Failures))
	}
	if got, want := result.Tasks[0].UpdatedAt, base.Add(10*time.Minute); !got.Equal(want) {
		t.Errorf("UpdatedAt = %s, want %s", got, want)
	}
}

func TestReadNoDir(t *testing.T) {
	t.Parallel()
	if _, err := task.Read(filepath.Join(t.TempDir(), "tasks")); err == nil {
		t.Error("Read() returned no error for a missing tasks directory")
	}
}
