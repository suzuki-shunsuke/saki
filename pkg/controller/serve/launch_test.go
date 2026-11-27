package serve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/suzuki-shunsuke/saki/pkg/controller/serve"
	"github.com/suzuki-shunsuke/saki/pkg/herdr"
)

// agentNamePattern is the rule of herdr's agent names, with the name saki gives.
var agentNamePattern = regexp.MustCompile(`^(?:task|review|session)-[0-9]{8}-[0-9]{6}-[a-z2-7]{4}$`)

// The prompts are spelled out rather than taken from the package, so that the test pins
// the text the specification gives.
const (
	launchPromptPrefix = `Run "saki skill show dev-flow" and follow the instructions. This pane is dedicated to this task. Don't open another pane. Create a task:` + "\n"
	reviewPromptPrefix = `Run "saki skill show pr-review" and follow the instructions. This pane is dedicated to this task. Don't open another pane. Review `
)

func busy() error {
	return &herdr.Error{Code: herdr.CodePaneBusy, Message: "agent target pane w1:p9 is not an available shell"}
}

func postLaunch(t *testing.T, l *serve.Launcher, text string) focusResult {
	t.Helper()
	return postLaunchKind(t, l, text, "")
}

// postLaunchKind sends a launch request of the kind, or without one when kind is empty.
func postLaunchKind(t *testing.T, l *serve.Launcher, text, kind string) focusResult {
	t.Helper()
	h := serve.NewHandler(slog.New(slog.DiscardHandler), emptyTasksDir(t), nil, nil, nil, l)
	req := map[string]string{"text": text}
	if kind != "" {
		req["kind"] = kind
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	rec := postFocusPath(t, h, "/launch", string(body), "application/json", "http://127.0.0.1:8080")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := focusResult{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func newLauncher(f *fakeHerdr) *serve.Launcher {
	return &serve.Launcher{Herdr: f, PaneID: "w1:p1", Dir: "/repo/saki", RetryInterval: time.Millisecond, Timeout: time.Second}
}

func TestLaunch(t *testing.T) {
	t.Parallel()
	f := &fakeHerdr{startErrs: []error{busy(), busy()}}
	got := postLaunch(t, newLauncher(f), "Export as CSV\nthe second line")
	if diff := cmp.Diff(focusResult{Result: "started", Message: "Started Claude Code in a new tab"}, got); diff != "" {
		t.Errorf("response (-want +got):\n%s", diff)
	}
	// The workspace of saki serve's pane, then a tab in saki serve's directory; the start
	// is tried again while the shell isn't ready, and then the description is submitted.
	wantCalls := []string{
		"pane get w1:p1",
		"tab create w1 /repo/saki new task",
		"agent start w1:p9", "agent start w1:p9", "agent start w1:p9",
		"agent prompt",
	}
	if diff := cmp.Diff(wantCalls, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if want := launchPromptPrefix + "Export as CSV\nthe second line"; f.prompted != want {
		t.Errorf("prompt = %q, want %q", f.prompted, want)
	}
	if !strings.HasPrefix(f.agent, "task-") {
		t.Errorf("agent name = %q, want task-...", f.agent)
	}
}

func TestLaunchTaskKind(t *testing.T) {
	t.Parallel()
	// "task" is the same as no kind, even for the URL of a pull request.
	f := &fakeHerdr{}
	got := postLaunchKind(t, newLauncher(f), "https://github.com/aquaproj/aqua/pull/1", "task")
	if diff := cmp.Diff(focusResult{Result: "started", Message: "Started Claude Code in a new tab"}, got); diff != "" {
		t.Errorf("response (-want +got):\n%s", diff)
	}
	if want := launchPromptPrefix + "https://github.com/aquaproj/aqua/pull/1"; f.prompted != want {
		t.Errorf("prompt = %q, want %q", f.prompted, want)
	}
	if !strings.HasPrefix(f.agent, "task-") {
		t.Errorf("agent name = %q, want task-...", f.agent)
	}
	// Creating a task doesn't look for an existing tab.
	if slices.Contains(f.calls, "tab list") {
		t.Errorf("listed the tabs to create a task: %v", f.calls)
	}
}

func TestLaunchReview(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		text   string
		label  string
		prompt string
	}{
		{
			// What follows the number is dropped, and the owner and the repository are kept
			// as they are written.
			name:   "files",
			text:   "\n  https://github.com/AquaProj/Aqua/pull/1/files \n",
			label:  "review AquaProj/Aqua#1",
			prompt: reviewPromptPrefix + "https://github.com/AquaProj/Aqua/pull/1",
		},
		{name: "fragment", text: "https://github.com/a/b/pull/12#discussion_r1", label: "review a/b#12", prompt: reviewPromptPrefix + "https://github.com/a/b/pull/12"},
		{name: "query", text: "https://github.com/a/b/pull/12?w=1", label: "review a/b#12", prompt: reviewPromptPrefix + "https://github.com/a/b/pull/12"},
		{name: "BOM and NEL around", text: "\uFEFF\u0085https://github.com/a/b/pull/12\u0085\uFEFF", label: "review a/b#12", prompt: reviewPromptPrefix + "https://github.com/a/b/pull/12"},
		{name: "trailing slash", text: "https://github.com/a/b/pull/12/", label: "review a/b#12", prompt: reviewPromptPrefix + "https://github.com/a/b/pull/12"},
		{name: "dot repository", text: "https://github.com/aquaproj/.github/pull/1", label: "review aquaproj/.github#1", prompt: reviewPromptPrefix + "https://github.com/aquaproj/.github/pull/1"},
		{name: "repository with dots", text: "https://github.com/a/b.c_d-e/pull/3", label: "review a/b.c_d-e#3", prompt: reviewPromptPrefix + "https://github.com/a/b.c_d-e/pull/3"},
		{
			// A review has no length limit, because what follows the number isn't sent.
			name:   "long",
			text:   "https://github.com/a/b/pull/1/" + strings.Repeat("x", 10001),
			label:  "review a/b#1",
			prompt: reviewPromptPrefix + "https://github.com/a/b/pull/1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeHerdr{startErrs: []error{busy()}}
			got := postLaunchKind(t, newLauncher(f), tt.text, "review")
			want := focusResult{Result: "started", Message: "Started Claude Code to review the PR in a new tab"}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			// No tab of the pull request's review is found, so a tab is created.
			wantCalls := []string{
				"tab list",
				"pane get w1:p1",
				"tab create w1 /repo/saki " + tt.label,
				"agent start w1:p9", "agent start w1:p9",
				"agent prompt",
			}
			if diff := cmp.Diff(wantCalls, f.calls); diff != "" {
				t.Errorf("calls (-want +got):\n%s", diff)
			}
			if f.prompted != tt.prompt {
				t.Errorf("prompt = %q, want %q", f.prompted, tt.prompt)
			}
			if !strings.HasPrefix(f.agent, "review-") {
				t.Errorf("agent name = %q, want review-...", f.agent)
			}
		})
	}
}

func TestLaunchReviewExistingTab(t *testing.T) {
	t.Parallel()
	const url = "https://github.com/aquaproj/aqua/pull/1234"
	tests := []struct {
		name string
		text string
		tabs []herdr.Tab
		// focus is the tab focused, or "" when a new tab is created.
		focus   string
		message string
	}{
		{
			// A tab of another workspace is found too.
			name:    "same label",
			text:    url,
			tabs:    []herdr.Tab{{TabID: "w1:t1", Label: "new task", WorkspaceID: "w1"}, {TabID: "w2:t5", Label: "review aquaproj/aqua#1234", WorkspaceID: "w2"}},
			focus:   "w2:t5",
			message: "A tab is already reviewing aquaproj/aqua#1234: focused it",
		},
		{
			// The owner and the repository ignore case, and the message has the tab's.
			name:    "owner and repository in another case",
			text:    "https://github.com/AquaProj/Aqua/pull/1234/files",
			tabs:    []herdr.Tab{{TabID: "w1:t1", Label: "review aquaproj/aqua#1234", WorkspaceID: "w1"}},
			focus:   "w1:t1",
			message: "A tab is already reviewing aquaproj/aqua#1234: focused it",
		},
		{
			name:    "the first of two",
			text:    url,
			tabs:    []herdr.Tab{{TabID: "w1:t1", Label: "review aquaproj/aqua#1234", WorkspaceID: "w1"}, {TabID: "w1:t2", Label: "review aquaproj/aqua#1234", WorkspaceID: "w1"}},
			focus:   "w1:t1",
			message: "A tab is already reviewing aquaproj/aqua#1234: focused it",
		},
		{name: "another number", text: url, tabs: []herdr.Tab{{TabID: "w1:t1", Label: "review aquaproj/aqua#123"}}},
		{name: "another repository", text: url, tabs: []herdr.Tab{{TabID: "w1:t1", Label: "review aquaproj/aqua-registry#1234"}}},
		{name: "without review", text: url, tabs: []herdr.Tab{{TabID: "w1:t1", Label: "aquaproj/aqua#1234"}}},
		{name: "review in another case", text: url, tabs: []herdr.Tab{{TabID: "w1:t1", Label: "Review aquaproj/aqua#1234"}}},
		{name: "renamed", text: url, tabs: []herdr.Tab{{TabID: "w1:t1", Label: "review aquaproj/aqua#1234 (old)"}}},
		{
			// Only ASCII letters ignore case: the Kelvin sign isn't "k".
			name: "Kelvin sign",
			text: "https://github.com/kube/x/pull/1",
			tabs: []herdr.Tab{{TabID: "w1:t1", Label: "review \u212Aube/x#1"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeHerdr{tabs: tt.tabs}
			got := postLaunchKind(t, newLauncher(f), tt.text, "review")
			if tt.focus == "" {
				if got.Result != "started" || !slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "tab create ") }) {
					t.Errorf("response = %+v, calls = %v, want a new tab", got, f.calls)
				}
				return
			}
			if diff := cmp.Diff(focusResult{Result: "focused_existing", Message: tt.message}, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			// No tab is created, Claude Code isn't started, and nothing is typed.
			if diff := cmp.Diff([]string{"tab list", "tab focus " + tt.focus}, f.calls); diff != "" {
				t.Errorf("calls (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLaunchReviewTabFailure(t *testing.T) {
	t.Parallel()
	notRunning := &herdr.Error{Code: herdr.CodeServerNotRunning, Message: "not running"}
	tab := []herdr.Tab{{TabID: "w1:t1", Label: "review a/b#1", WorkspaceID: "w1"}}
	tests := []struct {
		name  string
		herdr *fakeHerdr
		want  focusResult
	}{
		{name: "list: not running", herdr: &fakeHerdr{tabsErr: notRunning}, want: focusResult{Result: "unavailable", Message: "herdr isn't running"}},
		{name: "list fails", herdr: &fakeHerdr{tabsErr: errors.New("boom")}, want: focusResult{Result: "error", Message: "Failed to list the herdr tabs: boom"}},
		{name: "focus: not running", herdr: &fakeHerdr{tabs: tab, focusTabErr: notRunning}, want: focusResult{Result: "unavailable", Message: "herdr isn't running"}},
		{name: "focus fails", herdr: &fakeHerdr{tabs: tab, focusTabErr: errors.New("boom")}, want: focusResult{Result: "error", Message: "Failed to focus the tab: boom"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := postLaunchKind(t, newLauncher(tt.herdr), "https://github.com/a/b/pull/1", "review")
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			// A duplicate review is worse than nothing, so no tab is created.
			if slices.ContainsFunc(tt.herdr.calls, func(c string) bool { return strings.HasPrefix(c, "tab create ") }) {
				t.Errorf("created a tab after failing: %v", tt.herdr.calls)
			}
		})
	}
}

// sendLaunch sends a launch request without failing the test, so that it can be called from
// another goroutine.
func sendLaunch(ctx context.Context, h http.Handler, body string) (focusResult, error) {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/launch", strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	got := focusResult{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		return got, fmt.Errorf("status %d: %w", rec.Code, err)
	}
	return got, nil
}

func TestLaunchReviewAtOnce(t *testing.T) {
	t.Parallel()
	// The first request is held while creating its tab, and the second is sent meanwhile.
	f := &fakeHerdr{tabListed: make(chan struct{}), createRelease: make(chan struct{})}
	h := serve.NewHandler(slog.New(slog.DiscardHandler), emptyTasksDir(t), nil, nil, nil, newLauncher(f))
	results := make(chan focusResult, 2)
	send := func() {
		go func() {
			got, err := sendLaunch(t.Context(), h, `{"text":"https://github.com/a/b/pull/1","kind":"review"}`)
			if err != nil {
				t.Error(err)
			}
			results <- got
		}()
	}
	send()
	<-f.tabListed
	send()
	// The second waits for the first to create its tab. Something not happening can only be
	// checked for a while: without the lock, this passes only when the second doesn't reach
	// ListTabs in time.
	select {
	case <-f.tabListed:
		t.Fatal("the second request listed the tabs while the first was creating its tab")
	case <-time.After(100 * time.Millisecond):
	}
	close(f.createRelease)
	<-f.tabListed
	got := []string{(<-results).Result, (<-results).Result}
	slices.Sort(got)
	if diff := cmp.Diff([]string{"focused_existing", "started"}, got); diff != "" {
		t.Errorf("results (-want +got):\n%s", diff)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	creates := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "tab create ") {
			creates++
		}
	}
	if creates != 1 || !slices.Contains(f.calls, "tab focus w1:tN") {
		t.Errorf("calls = %v, want one tab created and then focused", f.calls)
	}
}

func TestLaunchReviewInvalidURL(t *testing.T) {
	t.Parallel()
	invalid := focusResult{Result: "invalid_url", Message: "The pull request URL is invalid"}
	tests := []struct {
		name string
		text string
	}{
		{name: "empty", text: " \n"},
		{name: "issue", text: "https://github.com/a/b/issues/1"},
		{name: "text after", text: "https://github.com/a/b/pull/1 fix it"},
		{name: "text after an ideographic space", text: "https://github.com/a/b/pull/1\u3000fix it"},
		{name: "BOM inside", text: "https://github.com/a/b/pull/1/\uFEFFx"},
		{name: "NEL inside", text: "https://github.com/a/b/pull/1/\u0085x"},
		{name: "two URLs", text: "https://github.com/a/b/pull/1\nhttps://github.com/a/b/pull/2"},
		{name: "letters after the number", text: "https://github.com/a/b/pull/12abc"},
		{name: "http", text: "http://github.com/a/b/pull/1"},
		{name: "www", text: "https://www.github.com/a/b/pull/1"},
		{name: "upper case", text: "HTTPS://GitHub.com/a/b/pull/1"},
		{name: "upper case pull", text: "https://github.com/a/b/PULL/1"},
		{name: "long s", text: "httpſ://github.com/a/b/pull/1"},
		{name: "short form", text: "aquaproj/aqua#1234"},
		{name: "parent directories", text: "https://github.com/../../pull/1"},
		{name: "parent repository", text: "https://github.com/aquaproj/../pull/1"},
		{name: "current repository", text: "https://github.com/aquaproj/./pull/1"},
		{name: "percent in the owner", text: "https://github.com/a%2e/b/pull/1"},
		{name: "too long", text: strings.Repeat("\u3042", 10001)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeHerdr{}
			got := postLaunchKind(t, newLauncher(f), tt.text, "review")
			if diff := cmp.Diff(invalid, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if len(f.calls) != 0 {
				t.Errorf("called herdr %v for an invalid URL", f.calls)
			}
		})
	}
}

func TestLaunchReviewOutsideHerdr(t *testing.T) {
	t.Parallel()
	// The URL is checked before whether saki serve runs in herdr.
	l := newLauncher(&fakeHerdr{})
	l.PaneID = ""
	if got := postLaunchKind(t, l, "x", "review"); got.Result != "invalid_url" {
		t.Errorf("result = %q, want invalid_url", got.Result)
	}
	if got := postLaunchKind(t, l, "https://github.com/a/b/pull/1", "review"); got.Result != "outside_herdr" {
		t.Errorf("result = %q, want outside_herdr", got.Result)
	}
}

func TestLaunchNotStarted(t *testing.T) {
	t.Parallel()
	notRunning := &herdr.Error{Code: herdr.CodeServerNotRunning, Message: "not running"}
	tests := []struct {
		name     string
		text     string
		paneID   string
		herdr    *fakeHerdr
		want     focusResult
		prompted bool
	}{
		{name: "empty", text: " \n\t", herdr: &fakeHerdr{}, want: focusResult{Result: "empty", Message: "The task description is empty"}},
		{name: "too long", text: strings.Repeat("\u3042", 10001), herdr: &fakeHerdr{}, want: focusResult{Result: "too_long", Message: "The task description is too long"}},
		{name: "outside herdr", text: "x", paneID: "-", herdr: &fakeHerdr{}, want: focusResult{Result: "outside_herdr", Message: "saki serve isn't running in a herdr pane"}},
		{name: "herdr not running", text: "x", herdr: &fakeHerdr{workspaceErr: notRunning}, want: focusResult{Result: "unavailable", Message: "herdr isn't running"}},
		{name: "tab fails", text: "x", herdr: &fakeHerdr{createErr: errors.New("boom")}, want: focusResult{Result: "error", Message: "Failed to start Claude Code: boom"}},
		{
			name:  "blocked at startup",
			text:  "x",
			herdr: &fakeHerdr{startErrs: []error{&herdr.Error{Code: herdr.CodeAgentNotReady, Message: "not ready"}}},
			want:  focusResult{Result: "blocked", Message: "Claude Code is waiting for input in the new tab"},
		},
		{
			name:     "blocked at prompt",
			text:     "x",
			herdr:    &fakeHerdr{promptErr: &herdr.Error{Code: herdr.CodeAgentBlocked, Message: "blocked"}},
			want:     focusResult{Result: "blocked", Message: "Claude Code is waiting for input in the new tab"},
			prompted: true,
		},
		{
			// Only a busy pane is tried again; another error may come after "claude" was typed.
			name:  "other start error isn't tried again",
			text:  "x",
			herdr: &fakeHerdr{startErrs: []error{&herdr.Error{Code: "timeout", Message: "timed out"}, nil}},
			want:  focusResult{Result: "error", Message: "Failed to start Claude Code: timed out"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := newLauncher(tt.herdr)
			if tt.paneID == "-" {
				l.PaneID = ""
			}
			got := postLaunch(t, l, tt.text)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if (tt.herdr.prompted != "") != tt.prompted {
				t.Errorf("prompted = %q", tt.herdr.prompted)
			}
			if tt.herdr.starts > 1 {
				t.Errorf("started %d times", tt.herdr.starts)
			}
		})
	}
}

func TestLaunchTimesOutWhileBusy(t *testing.T) {
	t.Parallel()
	errs := make([]error, 1000)
	for i := range errs {
		errs[i] = busy()
	}
	f := &fakeHerdr{startErrs: errs}
	l := newLauncher(f)
	l.Timeout = 30 * time.Millisecond
	got := postLaunch(t, l, "x")
	want := focusResult{Result: "error", Message: "Failed to start Claude Code: agent target pane w1:p9 is not an available shell"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("response (-want +got):\n%s", diff)
	}
	if f.prompted != "" {
		t.Error("prompted although Claude Code didn't start")
	}
}

func TestLaunchRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		body        string
		contentType string
		origin      string
		want        int
	}{
		{name: "another origin", body: `{"text":"x"}`, contentType: "application/json", origin: "http://evil.example.com", want: http.StatusForbidden},
		{name: "form", body: "text=x", contentType: "application/x-www-form-urlencoded", want: http.StatusForbidden},
		{name: "invalid JSON", body: `{`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "unknown kind", body: `{"text":"x","kind":"deploy"}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "too large body", body: `{"text":"` + strings.Repeat("a", 300<<10) + `"}`, contentType: "application/json", want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeHerdr{}
			h := serve.NewHandler(slog.New(slog.DiscardHandler), emptyTasksDir(t), nil, nil, nil, newLauncher(f))
			rec := postFocusPath(t, h, "/launch", tt.body, tt.contentType, tt.origin)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if len(f.calls) != 0 {
				t.Errorf("called herdr %v on a rejected request", f.calls)
			}
		})
	}
}

func TestLaunchContinuesAfterDisconnect(t *testing.T) {
	t.Parallel()
	f := &fakeHerdr{startErrs: []error{busy()}}
	var logs bytes.Buffer
	h := serve.NewHandler(slog.New(slog.NewTextHandler(&logs, nil)), emptyTasksDir(t), nil, nil, nil, newLauncher(f))
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // The browser went away before starting finished.
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/launch", strings.NewReader(`{"text":"x"}`))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if f.prompted == "" {
		t.Errorf("starting stopped when the request was canceled: %v", f.calls)
	}
	// The browser is gone, so the result has to be in the log.
	if !strings.Contains(logs.String(), "result=started") {
		t.Errorf("the result isn't logged: %s", logs.String())
	}
}

// startTasksDir has the task "one", whose session is the one it was created in.
func startTasksDir(t *testing.T) string {
	t.Helper()
	dir := emptyTasksDir(t)
	writeTask(t, dir, "one", "name: one\nclaude_sessions:\n  - id: s1\n")
	writeTask(t, dir, "two", "name: two\n")
	return dir
}

func postStart(t *testing.T, h http.Handler, task string) focusResult {
	t.Helper()
	body, err := json.Marshal(map[string]string{"task": task})
	if err != nil {
		t.Fatal(err)
	}
	rec := postFocusPath(t, h, "/start", string(body), "application/json", "http://127.0.0.1:8080")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := focusResult{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStart(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		task string
	}{
		// The session the task was created in has a pane, but a new session is started
		// anyway.
		{name: "task with a session", task: "one"},
		{name: "task without a session", task: "two"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := startTasksDir(t)
			f := &fakeHerdr{
				agents:    []herdr.Agent{{PaneID: "w1:p5", Kind: "claude", SessionID: "s1"}},
				startErrs: []error{busy()},
			}
			h := serve.NewHandler(slog.New(slog.DiscardHandler), dir, nil, f, nil, newLauncher(f))
			got := postStart(t, h, tt.task)
			want := focusResult{Result: "started", Message: "Started a new Claude Code session for " + tt.task + " in a new tab"}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			wantCalls := []string{
				"pane get w1:p1",
				"tab create w1 /repo/saki task " + tt.task,
				"agent start w1:p9", "agent start w1:p9",
				"agent prompt",
			}
			if diff := cmp.Diff(wantCalls, f.calls); diff != "" {
				t.Errorf("calls (-want +got):\n%s", diff)
			}
			taskDir := filepath.ToSlash(filepath.Join(dir, tt.task))
			wantPrompt := `Run "saki skill show dev-flow" and follow the instructions. This pane is dedicated to this task. Don't open another pane. ` +
				"Work on the task in " + taskDir + " in this new session. " +
				"Append this session's ID (the environment variable CLAUDE_CODE_SESSION_ID) to the end of claude_sessions in " + taskDir + "/task.yaml."
			if f.prompted != wantPrompt {
				t.Errorf("prompt = %q, want %q", f.prompted, wantPrompt)
			}
			if !agentNamePattern.MatchString(f.agent) || !strings.HasPrefix(f.agent, "session-") {
				t.Errorf("agent name = %q, want session-...", f.agent)
			}
			if len(f.focused) != 0 {
				t.Errorf("focused %v instead of starting a new session", f.focused)
			}
		})
	}
}

func TestStartNotStarted(t *testing.T) {
	t.Parallel()
	notRunning := &herdr.Error{Code: herdr.CodeServerNotRunning, Message: "not running"}
	tests := []struct {
		name   string
		task   string
		paneID string
		herdr  *fakeHerdr
		want   focusResult
	}{
		{name: "unknown task", task: "three", herdr: &fakeHerdr{}, want: focusResult{Result: "unknown_task", Message: "three isn't found"}},
		{name: "path", task: "../one", herdr: &fakeHerdr{}, want: focusResult{Result: "unknown_task", Message: "../one isn't found"}},
		{name: "outside herdr", task: "one", paneID: "-", herdr: &fakeHerdr{}, want: focusResult{Result: "outside_herdr", Message: "saki serve isn't running in a herdr pane"}},
		{name: "herdr not running", task: "one", herdr: &fakeHerdr{workspaceErr: notRunning}, want: focusResult{Result: "unavailable", Message: "herdr isn't running"}},
		{
			name:  "blocked",
			task:  "one",
			herdr: &fakeHerdr{promptErr: &herdr.Error{Code: herdr.CodeAgentBlocked, Message: "blocked"}},
			want:  focusResult{Result: "blocked", Message: "Claude Code is waiting for input in the new tab"},
		},
		{name: "tab fails", task: "one", herdr: &fakeHerdr{createErr: errors.New("boom")}, want: focusResult{Result: "error", Message: "Failed to start Claude Code: boom"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := newLauncher(tt.herdr)
			if tt.paneID == "-" {
				l.PaneID = ""
			}
			h := serve.NewHandler(slog.New(slog.DiscardHandler), startTasksDir(t), nil, tt.herdr, nil, l)
			got := postStart(t, h, tt.task)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if tt.want.Result == "unknown_task" && len(tt.herdr.calls) != 0 {
				t.Errorf("called herdr %v for an unknown task", tt.herdr.calls)
			}
		})
	}
}

func TestStartRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		body        string
		contentType string
		origin      string
		want        int
	}{
		{name: "another origin", body: `{"task":"one"}`, contentType: "application/json", origin: "http://evil.example.com", want: http.StatusForbidden},
		{name: "form", body: "task=one", contentType: "application/x-www-form-urlencoded", want: http.StatusForbidden},
		{name: "invalid JSON", body: `{`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "no task", body: `{}`, contentType: "application/json", want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeHerdr{}
			h := serve.NewHandler(slog.New(slog.DiscardHandler), startTasksDir(t), nil, f, nil, newLauncher(f))
			rec := postFocusPath(t, h, "/start", tt.body, tt.contentType, tt.origin)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if len(f.calls) != 0 {
				t.Errorf("called herdr %v on a rejected request", f.calls)
			}
		})
	}
}

// TestStartReview starts a session for a review of pr-review: its tab has the label of the
// form's review, so a tab already reviewing the pull request is focused instead.
func TestStartReview(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tabs []herdr.Tab
		// focus is the tab focused, or "" when a new tab is created.
		focus string
	}{
		{name: "new tab", tabs: []herdr.Tab{{TabID: "w1:t1", Label: "review aquaproj/aqua#123"}}},
		{name: "tab of the form", tabs: []herdr.Tab{{TabID: "w1:t1", Label: "review AquaProj/Aqua#1234"}}, focus: "w1:t1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := emptyTasksDir(t)
			writeTask(t, dir, "review_aquaproj_aqua_1234", "name: review_aquaproj_aqua_1234\ntype: pr-review\npull_requests:\n  - https://github.com/aquaproj/aqua/pull/1234\n")
			f := &fakeHerdr{tabs: tt.tabs, startErrs: []error{busy()}}
			h := serve.NewHandler(slog.New(slog.DiscardHandler), dir, nil, f, nil, newLauncher(f))
			got := postStart(t, h, "review_aquaproj_aqua_1234")
			if tt.focus != "" {
				want := focusResult{Result: "focused_existing", Message: "A tab is already reviewing AquaProj/Aqua#1234: focused it"}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("response (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff([]string{"tab list", "tab focus " + tt.focus}, f.calls); diff != "" {
					t.Errorf("calls (-want +got):\n%s", diff)
				}
				return
			}
			want := focusResult{Result: "started", Message: "Started a new Claude Code session for review_aquaproj_aqua_1234 in a new tab"}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			wantCalls := []string{
				"tab list",
				"pane get w1:p1",
				"tab create w1 /repo/saki review aquaproj/aqua#1234",
				"agent start w1:p9", "agent start w1:p9",
				"agent prompt",
			}
			if diff := cmp.Diff(wantCalls, f.calls); diff != "" {
				t.Errorf("calls (-want +got):\n%s", diff)
			}
			if !strings.HasPrefix(f.prompted, `Run "saki skill show pr-review" and follow the instructions.`) {
				t.Errorf("prompt = %q, want pr-review's", f.prompted)
			}
		})
	}
}
