package serve_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/suzuki-shunsuke/saki/pkg/controller/serve"
	"github.com/suzuki-shunsuke/saki/pkg/herdr"
)

const (
	resumeSession = "0b7c0c8e-1d2f-4a3b-9c4d-5e6f7a8b9c0d"
	// noRecordSession has no record in the configuration directory.
	noRecordSession = "1b7c0c8e-1d2f-4a3b-9c4d-5e6f7a8b9c0d"
	// noCwdSession has a record without a working directory.
	noCwdSession = "2b7c0c8e-1d2f-4a3b-9c4d-5e6f7a8b9c0d"
)

// resumeTasksDir writes tasks whose sessions have no pane in the fake herdr.
func resumeTasksDir(t *testing.T) string {
	t.Helper()
	dir := emptyTasksDir(t)
	// The session written last is resumed, even when an earlier one is invalid.
	writeTask(t, dir, "one", "name: one\nclaude_sessions:\n  - id: s1\n  - id: "+resumeSession+"\n")
	writeTask(t, dir, "invalid", "name: invalid\nclaude_sessions:\n  - id: "+resumeSession+"\n  - id: \"x; rm -rf ~\"\n")
	writeTask(t, dir, "norecord", "name: norecord\nclaude_sessions:\n  - id: "+noRecordSession+"\n")
	writeTask(t, dir, "nocwd", "name: nocwd\nclaude_sessions:\n  - id: "+noCwdSession+"\n")
	writeTask(t, dir, "review_o_r_1", "name: review_o_r_1\ntype: pr-review\npull_requests:\n  - https://github.com/o/r/issues/2\n  - https://github.com/O/R/pull/1/files\nclaude_sessions:\n  - id: "+resumeSession+"\n")
	return dir
}

// claudeConfigDir writes the records of the sessions: resumeSession was last working in
// /work/last, after starting in /work/first.
func claudeConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	records := map[string]string{
		resumeSession: `{"type":"summary"}` + "\n" + `{"cwd":"/work/first"}` + "\n" + `{"cwd":"/work/mid"}` + "\n" + `{"cwd":"/work/last"}` + "\n",
		noCwdSession:  `{"type":"summary"}` + "\n",
	}
	for id, content := range records {
		p := filepath.Join(dir, "projects", "-work-first", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newResumeLauncher(t *testing.T, f *fakeHerdr, dirs ...string) *serve.Launcher {
	t.Helper()
	l := newLauncher(f)
	l.ConfigDir = claudeConfigDir(t)
	l.IsDir = func(p string) bool { return slices.Contains(dirs, p) }
	return l
}

func postFocusTask(t *testing.T, h http.Handler, task string) focusResult {
	t.Helper()
	body, err := json.Marshal(map[string]string{"task": task})
	if err != nil {
		t.Fatal(err)
	}
	rec := postFocus(t, h, string(body), "application/json", "http://127.0.0.1:8080")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := focusResult{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func newResumeHandler(t *testing.T, f *fakeHerdr, l *serve.Launcher) http.Handler {
	t.Helper()
	return serve.NewHandler(slog.New(slog.DiscardHandler), resumeTasksDir(t), nil, f, nil, l)
}

// noPaneAgents are agents of other sessions, so that no task's session has a pane.
func noPaneAgents() []herdr.Agent {
	return []herdr.Agent{{PaneID: "w1:p2", Kind: "claude", SessionID: "other"}}
}

func TestResume(t *testing.T) {
	t.Parallel()
	f := &fakeHerdr{agents: noPaneAgents(), startErrs: []error{busy()}}
	l := newResumeLauncher(t, f, "/work/first", "/work/last")
	got := postFocusTask(t, newResumeHandler(t, f, l), "one")
	if diff := cmp.Diff(focusResult{Result: "resumed", Message: "Resumed the session of one in a new tab"}, got); diff != "" {
		t.Errorf("response (-want +got):\n%s", diff)
	}
	// A tab in saki serve's workspace, in the directory the session was last working in;
	// the start is tried again while the shell isn't ready, and no prompt is sent.
	wantCalls := []string{
		"pane get w1:p1",
		"tab create w1 /work/last task one",
		"agent start w1:p9",
		"agent start w1:p9",
	}
	if diff := cmp.Diff(wantCalls, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"--resume", resumeSession}, f.args); diff != "" {
		t.Errorf("arguments (-want +got):\n%s", diff)
	}
	if !agentNamePattern.MatchString(f.agent) || f.agent[:7] != "resume-" {
		t.Errorf("agent name = %q", f.agent)
	}
}

type resumeCase struct {
	name  string
	task  string
	dirs  []string
	pane  string
	want  focusResult
	calls []string
}

// resumeCases are the cases which resume in another directory or don't resume.
func resumeCases() []resumeCase {
	return []resumeCase{
		{
			name:  "falls back to the first directory",
			task:  "one",
			dirs:  []string{"/work/first", "/work/mid"},
			pane:  "w1:p1",
			want:  focusResult{Result: "resumed_fallback", Message: "Resumed the session of one in /work/first, because /work/last no longer exists"},
			calls: []string{"pane get w1:p1", "tab create w1 /work/first task one", "agent start w1:p9"},
		},
		{
			name: "no directory",
			task: "one",
			dirs: []string{"/work/mid"},
			pane: "w1:p1",
			want: focusResult{Result: "no_dir", Message: "The directories of the session of one no longer exist"},
		},
		{
			name: "no working directory in the record",
			task: "nocwd",
			dirs: []string{"/work/first", "/work/last"},
			pane: "w1:p1",
			want: focusResult{Result: "no_dir", Message: "The directories of the session of nocwd no longer exist"},
		},
		{
			name: "no record",
			task: "norecord",
			dirs: []string{"/work/first", "/work/last"},
			pane: "w1:p1",
			want: focusResult{Result: "no_record", Message: "No record of the session " + noRecordSession + " of norecord"},
		},
		{
			name: "invalid session ID",
			task: "invalid",
			dirs: []string{"/work/first", "/work/last"},
			pane: "w1:p1",
			want: focusResult{Result: "invalid_session", Message: "The session ID of invalid is invalid: x; rm -rf ~"},
		},
		{
			name: "outside herdr",
			task: "one",
			dirs: []string{"/work/first", "/work/last"},
			want: focusResult{Result: "outside_herdr", Message: "saki serve isn't running in a herdr pane"},
		},
	}
}

func TestResumeNotStarted(t *testing.T) {
	t.Parallel()
	for _, tt := range resumeCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeHerdr{agents: noPaneAgents()}
			l := newResumeLauncher(t, f, tt.dirs...)
			l.PaneID = tt.pane
			got := postFocusTask(t, newResumeHandler(t, f, l), tt.task)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.calls, f.calls); diff != "" {
				t.Errorf("calls (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResumeFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		herdr *fakeHerdr
		want  focusResult
	}{
		{
			name:  "Claude Code waits for input",
			herdr: &fakeHerdr{startErrs: []error{&herdr.Error{Code: herdr.CodeAgentNotReady, Message: "blocked"}}},
			want:  focusResult{Result: "blocked", Message: "Claude Code is waiting for input in the new tab"},
		},
		{
			name:  "start fails",
			herdr: &fakeHerdr{startErrs: []error{errors.New("exec: herdr not found")}},
			want:  focusResult{Result: "error", Message: "Failed to resume the session: exec: herdr not found"},
		},
		{
			name:  "create fails",
			herdr: &fakeHerdr{createErr: &herdr.Error{Code: "invalid_cwd", Message: "no such directory"}},
			want:  focusResult{Result: "error", Message: "Failed to resume the session: no such directory"},
		},
		{
			name:  "herdr not running",
			herdr: &fakeHerdr{workspaceErr: &herdr.Error{Code: herdr.CodeServerNotRunning, Message: "not running"}},
			want:  focusResult{Result: "unavailable", Message: "herdr isn't running"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.herdr.agents = noPaneAgents()
			l := newResumeLauncher(t, tt.herdr, "/work/first", "/work/last")
			got := postFocusTask(t, newResumeHandler(t, tt.herdr, l), "one")
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResumeFocusesResumedPane(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// agent is what herdr recognizes in the resumed pane at the second click; nil
		// means the pane was closed.
		agent       *herdr.Agent
		wantFocused bool
	}{
		{
			// Claude Code waits at a dialog, and herdr hasn't recorded the session yet.
			name:        "no session recorded yet",
			agent:       &herdr.Agent{PaneID: "w1:p9", Kind: "claude"},
			wantFocused: true,
		},
		{
			name:        "the resumed session",
			agent:       &herdr.Agent{PaneID: "w1:p9", Kind: "claude", SessionID: resumeSession},
			wantFocused: true,
		},
		{
			name: "pane closed",
		},
		{
			name:  "Claude Code exited",
			agent: &herdr.Agent{PaneID: "w1:p9"},
		},
		{
			// Claude Code was started again in the pane, or cleared.
			name:  "another session",
			agent: &herdr.Agent{PaneID: "w1:p9", Kind: "claude", SessionID: "other"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeHerdr{agents: noPaneAgents(), startErrs: []error{&herdr.Error{Code: herdr.CodeAgentNotReady, Message: "blocked"}}}
			l := newResumeLauncher(t, f, "/work/first", "/work/last")
			h := newResumeHandler(t, f, l)
			if got := postFocusTask(t, h, "one"); got.Result != "blocked" {
				t.Fatalf("first click = %+v, want blocked", got)
			}
			f.mu.Lock()
			if tt.agent != nil {
				f.paneAgents = map[string]*herdr.Agent{"w1:p9": tt.agent}
			}
			f.calls = nil
			f.mu.Unlock()
			got := postFocusTask(t, h, "one")
			if tt.wantFocused {
				checkFocusedResumedPane(t, f, got)
				return
			}
			checkResumedAgain(t, f, got)
		})
	}
}

func TestResumeAlreadyStarting(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	f := &fakeHerdr{agents: noPaneAgents(), createRelease: release}
	l := newResumeLauncher(t, f, "/work/first", "/work/last")
	h := newResumeHandler(t, f, l)
	var first focusResult
	var wg sync.WaitGroup
	wg.Go(func() {
		first = postFocusTask(t, h, "one")
	})
	// Wait until the first click is creating the tab.
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		creating := slices.Contains(f.calls, "tab create w1 /work/last task one")
		f.mu.Unlock()
		if creating {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first click didn't create a tab")
		}
		time.Sleep(time.Millisecond)
	}
	got := postFocusTask(t, h, "one")
	if diff := cmp.Diff(focusResult{Result: "already_starting", Message: "The session of one is already starting"}, got); diff != "" {
		t.Errorf("second click (-want +got):\n%s", diff)
	}
	close(release)
	wg.Wait()
	if first.Result != "resumed" {
		t.Errorf("first click = %+v, want resumed", first)
	}
	// Once the first has finished, a click is accepted again: the fake herdr still has no
	// pane of the session, and the resumed pane is gone, so it's resumed again.
	if got := postFocusTask(t, h, "one"); got.Result != "resumed" {
		t.Errorf("third click = %+v, want resumed", got)
	}
}

func TestResumeReview(t *testing.T) {
	t.Parallel()
	// A review of pr-review has the label of the form's review tab.
	f := &fakeHerdr{agents: noPaneAgents()}
	l := newResumeLauncher(t, f, "/work/first", "/work/last")
	got := postFocusTask(t, newResumeHandler(t, f, l), "review_o_r_1")
	if diff := cmp.Diff(focusResult{Result: "resumed", Message: "Resumed the session of review_o_r_1 in a new tab"}, got); diff != "" {
		t.Errorf("response (-want +got):\n%s", diff)
	}
	wantCalls := []string{"tab list", "pane get w1:p1", "tab create w1 /work/last review O/R#1", "agent start w1:p9"}
	if diff := cmp.Diff(wantCalls, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}

	// A tab already reviewing the pull request is focused instead.
	f = &fakeHerdr{agents: noPaneAgents(), tabs: []herdr.Tab{{TabID: "w1:t5", Label: "review o/r#1", WorkspaceID: "w1"}}}
	l = newResumeLauncher(t, f, "/work/first", "/work/last")
	got = postFocusTask(t, newResumeHandler(t, f, l), "review_o_r_1")
	if diff := cmp.Diff(focusResult{Result: "focused_existing", Message: "A tab is already reviewing o/r#1: focused it"}, got); diff != "" {
		t.Errorf("response (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"tab list", "tab focus w1:t5"}, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func checkFocusedResumedPane(t *testing.T, f *fakeHerdr, got focusResult) {
	t.Helper()
	want := focusResult{Result: "focused_resumed", Message: "Focused the tab where the session of one was resumed"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("response (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"pane agent w1:p9"}, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"w1:p9"}, f.focused); diff != "" {
		t.Errorf("focused (-want +got):\n%s", diff)
	}
}

// checkResumedAgain checks that the resumed pane was forgotten, and the session was resumed
// in a new tab again.
func checkResumedAgain(t *testing.T, f *fakeHerdr, got focusResult) {
	t.Helper()
	if got.Result != "resumed" {
		t.Errorf("response = %+v, want resumed", got)
	}
	wantCalls := []string{"pane agent w1:p9", "pane get w1:p1", "tab create w1 /work/last task one", "agent start w1:p9"}
	if diff := cmp.Diff(wantCalls, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if len(f.focused) != 0 {
		t.Errorf("focused = %v, want none", f.focused)
	}
}

func TestResumeKeepsResumedPaneOnHerdrFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		set      func(f *fakeHerdr)
		want     focusResult
		wantCall []string
	}{
		{
			name: "pane get times out",
			set:  func(f *fakeHerdr) { f.paneAgentErr = errors.New("signal: killed") },
			want: focusResult{Result: "error", Message: "Failed to resume the session: signal: killed"},
		},
		{
			name: "herdr not running",
			set: func(f *fakeHerdr) {
				f.paneAgentErr = &herdr.Error{Code: herdr.CodeServerNotRunning, Message: "not running"}
			},
			want: focusResult{Result: "unavailable", Message: "herdr isn't running"},
		},
		{
			name: "focus fails",
			set: func(f *fakeHerdr) {
				f.paneAgents = map[string]*herdr.Agent{"w1:p9": {PaneID: "w1:p9", Kind: "claude"}}
				f.focusErr = errors.New("focus failed")
			},
			want: focusResult{Result: "error", Message: "Failed to resume the session: focus failed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeHerdr{agents: noPaneAgents(), startErrs: []error{&herdr.Error{Code: herdr.CodeAgentNotReady, Message: "blocked"}}}
			l := newResumeLauncher(t, f, "/work/first", "/work/last")
			h := newResumeHandler(t, f, l)
			if got := postFocusTask(t, h, "one"); got.Result != "blocked" {
				t.Fatalf("first click = %+v, want blocked", got)
			}
			f.mu.Lock()
			tt.set(f)
			f.calls = nil
			f.mu.Unlock()
			got := postFocusTask(t, h, "one")
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			// No tab is created, and the pane is still remembered once herdr is back.
			if diff := cmp.Diff([]string{"pane agent w1:p9"}, f.calls); diff != "" {
				t.Errorf("calls (-want +got):\n%s", diff)
			}
			f.mu.Lock()
			f.paneAgentErr = nil
			f.focusErr = nil
			f.paneAgents = map[string]*herdr.Agent{"w1:p9": {PaneID: "w1:p9", Kind: "claude"}}
			f.mu.Unlock()
			if got := postFocusTask(t, h, "one"); got.Result != "focused_resumed" {
				t.Errorf("click after herdr is back = %+v, want focused_resumed", got)
			}
		})
	}
}
