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
	got := sessionJob(task.DefaultDir, &task.Task{Dir: "one", Type: "implementation", PullRequests: []string{"https://github.com/o/r/pull/1"}})
	want := &launchJob{
		tabLabel:    "task one",
		agentPrefix: "session",
		prompt: `Run "saki skill show dev-flow" and follow the instructions. This pane is dedicated to this task. Don't open another pane. ` +
			"Work on the task in tasks/one in this new session. " +
			"Append this session's ID (the environment variable CLAUDE_CODE_SESSION_ID) to the end of claude_sessions in tasks/one/task.yaml.",
		started: "Started a new Claude Code session for one in a new tab",
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(launchJob{})); diff != "" {
		t.Errorf("job (-want +got):\n%s", diff)
	}
}

func TestSessionJobReview(t *testing.T) {
	t.Parallel()
	const prefix = `Run "saki skill show pr-review" and follow the instructions. This pane is dedicated to this task. Don't open another pane. `
	const appendSession = " Append this session's ID (the environment variable CLAUDE_CODE_SESSION_ID) to the end of claude_sessions in tasks/review_o_r_1/task.yaml."
	tests := []struct {
		name string
		prs  []string
		want *launchJob
	}{
		{
			// The tab has the label of the form's review, and the URL is built from its
			// parts: what followed the number isn't sent.
			name: "pull request",
			prs:  []string{"https://github.com/O/R/pull/1/files#diff"},
			want: &launchJob{
				tabLabel:    "review O/R#1",
				reuseTab:    true,
				agentPrefix: "session",
				prompt:      prefix + "Resume the review of https://github.com/O/R/pull/1 in tasks/review_o_r_1 in this new session." + appendSession,
				started:     "Started a new Claude Code session for review_o_r_1 in a new tab",
			},
		},
		{
			name: "the first URL of a pull request",
			prs:  []string{"https://github.com/o/r/issues/2", "https://github.com/o/r/pull/3", "https://github.com/o/r/pull/4"},
			want: &launchJob{
				tabLabel:    "review o/r#3",
				reuseTab:    true,
				agentPrefix: "session",
				prompt:      prefix + "Resume the review of https://github.com/o/r/pull/3 in tasks/review_o_r_1 in this new session." + appendSession,
				started:     "Started a new Claude Code session for review_o_r_1 in a new tab",
			},
		},
		{
			name: "no pull request",
			prs:  []string{"https://example.com/o/r/pull/1"},
			want: &launchJob{
				tabLabel:    "task review_o_r_1",
				agentPrefix: "session",
				prompt:      prefix + "Resume the review in tasks/review_o_r_1 in this new session." + appendSession,
				started:     "Started a new Claude Code session for review_o_r_1 in a new tab",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sessionJob(task.DefaultDir, &task.Task{Dir: "review_o_r_1", Type: task.TypePRReview, PullRequests: tt.prs})
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(launchJob{})); diff != "" {
				t.Errorf("job (-want +got):\n%s", diff)
			}
		})
	}
}
