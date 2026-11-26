package serve

import (
	"bytes"
	"errors"
	"log/slog"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/suzuki-shunsuke/saki/pkg/task"
)

var warnedDir = regexp.MustCompile(`task_dir=(\S+)`)

// warnedDirs returns the task directories warned about in the logs, and resets the logs.
func warnedDirs(logs *bytes.Buffer) []string {
	matches := warnedDir.FindAllStringSubmatch(logs.String(), -1)
	dirs := make([]string, 0, len(matches))
	for _, m := range matches {
		dirs = append(dirs, m[1])
	}
	logs.Reset()
	return dirs
}

func failure(dir, msg string) *task.Failure {
	return &task.Failure{Dir: dir, Err: errors.New(msg)} //nolint:err113
}

func TestFailureReporter(t *testing.T) {
	t.Parallel()
	logs := &bytes.Buffer{}
	r := newFailureReporter(slog.New(slog.NewTextHandler(logs, nil)), []*task.Failure{
		failure("a", "missing"),
		failure("b", "invalid"),
	})
	steps := []struct {
		name     string
		failures []*task.Failure
		want     []string
	}{
		{name: "startup", failures: nil, want: []string{"a", "b"}},
		{
			name:     "the same failures aren't warned again",
			failures: []*task.Failure{failure("a", "missing"), failure("b", "invalid")},
			want:     []string{},
		},
		{
			name:     "a changed error and a new failure are warned",
			failures: []*task.Failure{failure("a", "invalid"), failure("b", "invalid"), failure("c", "missing")},
			want:     []string{"a", "c"},
		},
		{
			name:     "a task which became readable is forgotten",
			failures: []*task.Failure{failure("a", "invalid"), failure("c", "missing")},
			want:     []string{},
		},
		{
			name:     "a task which became unreadable again is warned again",
			failures: []*task.Failure{failure("a", "invalid"), failure("b", "invalid"), failure("c", "missing")},
			want:     []string{"b"},
		},
	}
	for i, step := range steps {
		if i > 0 {
			r.report(step.failures)
		}
		if diff := cmp.Diff(step.want, warnedDirs(logs)); diff != "" {
			t.Errorf("%s: warned tasks (-want +got):\n%s", step.name, diff)
		}
	}
}

func TestShortGitHubRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url  string
		want string
		ok   bool
	}{
		{url: "https://github.com/suzuki-shunsuke/saki", want: "suzuki-shunsuke/saki", ok: true},
		{url: "https://github.com/suzuki-shunsuke/saki/issues/1", want: "suzuki-shunsuke/saki#1", ok: true},
		{url: "https://github.com/suzuki-shunsuke/saki/pull/105", want: "suzuki-shunsuke/saki#105", ok: true},
		// One trailing slash is allowed.
		{url: "https://github.com/suzuki-shunsuke/saki/", want: "suzuki-shunsuke/saki", ok: true},
		{url: "https://github.com/suzuki-shunsuke/saki/pull/105/", want: "suzuki-shunsuke/saki#105", ok: true},
		{url: "https://github.com/suzuki-shunsuke/saki//"},
		{url: "http://github.com/suzuki-shunsuke/saki"},
		{url: "https://github.com/suzuki-shunsuke"},
		{url: "https://github.com/suzuki-shunsuke/saki/issues"},
		{url: "https://github.com/suzuki-shunsuke/saki/blob/main/README.md"},
		{url: "https://github.com/suzuki-shunsuke/saki/pull/105/files"},
		{url: "https://github.com/suzuki-shunsuke/saki/issues/1#issuecomment-2"},
		{url: "https://github.com/suzuki-shunsuke/saki?tab=readme"},
		{url: "https://github.com/suzuki-shunsuke/saki/pull/abc"},
		{url: "https://example.com/suzuki-shunsuke/saki"},
		{url: "https://github.com.example.com/suzuki-shunsuke/saki"},
	}
	for _, tt := range tests {
		got, ok := shortGitHubRef(tt.url)
		if got != tt.want || ok != tt.ok {
			t.Errorf("shortGitHubRef(%q) = (%q, %v), want (%q, %v)", tt.url, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSessionJob(t *testing.T) {
	t.Parallel()
	// saki serve reads the tasks directory "tasks" relative to the directory Claude Code
	// runs in, so the prompt names the task by that relative path.
	got := sessionJob(task.DefaultDir, "one")
	want := &launchJob{
		tabLabel:    "task one",
		agentPrefix: "session",
		prompt: `Run "saki skill show dev-flow" and follow the instructions. This pane is dedicated to this task. Don't open another pane. ` +
			"Work on the task in tasks/one in this new session. " +
			"Add this session's ID (the environment variable CLAUDE_CODE_SESSION_ID) to claude_sessions in tasks/one/task.yaml.",
		started: "Started a new Claude Code session for one in a new tab",
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(launchJob{})); diff != "" {
		t.Errorf("job (-want +got):\n%s", diff)
	}
}
