package serve_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/suzuki-shunsuke/saki/pkg/controller/serve"
	"github.com/suzuki-shunsuke/saki/pkg/herdr"
)

// fakeHerdr stands in for herdr. Its methods may be called by requests running at once.
type fakeHerdr struct {
	mu sync.Mutex

	agents   []herdr.Agent
	listErr  error
	focusErr error
	focused  []string

	// For launching. calls records the calls in order.
	workspaceErr error
	createErr    error
	// startErrs are returned by the calls of StartAgent in order, then nil.
	startErrs []error
	promptErr error
	calls     []string
	starts    int
	prompted  string
	// agent is the name of the last agent started, and args its arguments.
	agent string
	args  []string

	// paneAgents are the agents PaneAgent returns by pane; a pane missing here is closed.
	paneAgents map[string]*herdr.Agent

	// tabs are the tabs ListTabs returns. CreateTab adds the tab it creates, as tab w1:tN.
	tabs        []herdr.Tab
	tabsErr     error
	focusTabErr error
	// tabListed, if not nil, is sent to when ListTabs is called.
	tabListed chan struct{}
	// createRelease, if not nil, holds CreateTab until it's closed, before the tab is added.
	createRelease chan struct{}
}

func (f *fakeHerdr) PaneWorkspace(_ context.Context, paneID string) (string, error) {
	f.record("pane get " + paneID)
	return "w1", f.workspaceErr
}

func (f *fakeHerdr) CreateTab(_ context.Context, workspaceID, cwd, label string) (string, error) {
	f.record("tab create " + workspaceID + " " + cwd + " " + label)
	if f.createRelease != nil {
		<-f.createRelease
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr == nil {
		f.tabs = append(f.tabs, herdr.Tab{TabID: "w1:tN", Label: label, WorkspaceID: workspaceID})
	}
	return "w1:p9", f.createErr
}

func (f *fakeHerdr) ListTabs(context.Context) ([]herdr.Tab, error) {
	f.record("tab list")
	if f.tabListed != nil {
		f.tabListed <- struct{}{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.tabs), f.tabsErr
}

func (f *fakeHerdr) FocusTab(_ context.Context, tabID string) error {
	f.record("tab focus " + tabID)
	return f.focusTabErr
}

func (f *fakeHerdr) StartAgent(_ context.Context, name, paneID string, _ time.Duration, args ...string) error {
	f.record("agent start " + paneID)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agent = name
	f.args = args
	if !agentNamePattern.MatchString(name) {
		return errors.New("invalid agent name " + name)
	}
	f.starts++
	if f.starts <= len(f.startErrs) {
		return f.startErrs[f.starts-1]
	}
	return nil
}

func (f *fakeHerdr) Prompt(_ context.Context, _, text string) error {
	f.record("agent prompt")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompted = text
	return f.promptErr
}

func (f *fakeHerdr) PaneAgent(_ context.Context, paneID string) (*herdr.Agent, error) {
	f.record("pane agent " + paneID)
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.paneAgents[paneID]; ok {
		return a, nil
	}
	return nil, &herdr.Error{Code: "pane_not_found", Message: "pane " + paneID + " not found"}
}

func (f *fakeHerdr) ListAgents(context.Context) ([]herdr.Agent, error) {
	return f.agents, f.listErr
}

func (f *fakeHerdr) Focus(_ context.Context, paneID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focused = append(f.focused, paneID)
	return f.focusErr
}

func (f *fakeHerdr) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

type focusResult struct {
	Result  string `json:"result"`
	Message string `json:"message"`
}

func postFocus(t *testing.T, h http.Handler, body, contentType, origin string) *httptest.ResponseRecorder {
	t.Helper()
	return postFocusPath(t, h, "/focus", body, contentType, origin)
}

func postFocusPath(t *testing.T, h http.Handler, path, body, contentType, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func focusTasksDir(t *testing.T) string {
	t.Helper()
	dir := emptyTasksDir(t)
	writeTask(t, dir, "none", "name: none\n")
	writeTask(t, dir, "one", "name: one\nclaude_sessions:\n  - id: s1\n")
	writeTask(t, dir, "two", "name: two\nclaude_sessions:\n  - id: s1\n  - id: s2\n")
	return dir
}

type focusCase struct {
	name        string
	task        string
	herdr       *fakeHerdr
	want        focusResult
	wantFocused []string
}

// focusAgents are the agents the fake herdr lists in TestFocus.
func focusAgents() []herdr.Agent {
	return []herdr.Agent{
		{PaneID: "w1:p0", Kind: "saki", SessionID: "s2"},
		{PaneID: "w1:p1", Kind: "claude", SessionID: "s1"},
		{PaneID: "w1:p2", Kind: "claude"},
		{PaneID: "w1:p3", Kind: "claude", SessionID: "s2"},
		{PaneID: "w1:p4", Kind: "claude", SessionID: "s2"},
	}
}

func focusCases() []focusCase {
	return append(focusFoundCases(), focusNotFocusedCases()...)
}

// focusFoundCases are the cases which find a pane.
func focusFoundCases() []focusCase {
	agents := focusAgents()
	return []focusCase{
		{
			name:        "one session",
			task:        "one",
			herdr:       &fakeHerdr{agents: agents},
			want:        focusResult{Result: "focused", Message: "Focused the pane of one"},
			wantFocused: []string{"w1:p1"},
		},
		{
			// The session written last wins; other agents than claude are ignored, and the
			// first pane herdr lists wins among panes with the same session.
			name:        "the session written last",
			task:        "two",
			herdr:       &fakeHerdr{agents: agents},
			want:        focusResult{Result: "focused", Message: "Focused the pane of two"},
			wantFocused: []string{"w1:p3"},
		},
		{
			name:        "falls back to an earlier session with a pane",
			task:        "two",
			herdr:       &fakeHerdr{agents: agents[:2]},
			want:        focusResult{Result: "focused", Message: "Focused the pane of two"},
			wantFocused: []string{"w1:p1"},
		},
	}
}

// focusNotFocusedCases are the cases which don't focus a pane, or fail to.
func focusNotFocusedCases() []focusCase {
	agents := focusAgents()
	return []focusCase{
		{
			name:  "no session",
			task:  "none",
			herdr: &fakeHerdr{agents: agents},
			want:  focusResult{Result: "no_session", Message: "none has no Claude Code session"},
		},
		{
			name:  "no pane",
			task:  "one",
			herdr: &fakeHerdr{agents: agents[2:]},
			want:  focusResult{Result: "not_found", Message: "No herdr pane for the sessions of one"},
		},
		{
			name:  "unknown task",
			task:  "../one",
			herdr: &fakeHerdr{agents: agents},
			want:  focusResult{Result: "unknown_task", Message: "../one isn't found"},
		},
		{
			name:  "herdr not running",
			task:  "one",
			herdr: &fakeHerdr{listErr: &herdr.Error{Code: herdr.CodeServerNotRunning, Message: "not running"}},
			want:  focusResult{Result: "unavailable", Message: "herdr isn't running"},
		},
		{
			name:  "list fails",
			task:  "one",
			herdr: &fakeHerdr{listErr: errors.New("exec: herdr not found")},
			want:  focusResult{Result: "error", Message: "Failed to focus the pane: exec: herdr not found"},
		},
		{
			name:        "focus fails",
			task:        "one",
			herdr:       &fakeHerdr{agents: agents, focusErr: &herdr.Error{Code: "agent_not_found", Message: "agent w1:p1 not found"}},
			want:        focusResult{Result: "error", Message: "Failed to focus the pane: agent w1:p1 not found"},
			wantFocused: []string{"w1:p1"},
		},
	}
}

func TestFocus(t *testing.T) {
	t.Parallel()
	tests := focusCases()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := serve.NewHandler(slog.New(slog.DiscardHandler), focusTasksDir(t), nil, tt.herdr, nil, nil)
			body, err := json.Marshal(map[string]string{"task": tt.task})
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
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantFocused, tt.herdr.focused); diff != "" {
				t.Errorf("focused panes (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFocusRejected(t *testing.T) {
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
		{name: "text plain", body: `{"task":"one"}`, contentType: "text/plain", want: http.StatusForbidden},
		{name: "no content type", body: `{"task":"one"}`, want: http.StatusForbidden},
		{name: "invalid JSON", body: `{`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "no task", body: `{}`, contentType: "application/json; charset=utf-8", origin: "http://localhost:8080", want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeHerdr{agents: []herdr.Agent{{PaneID: "w1:p1", Kind: "claude", SessionID: "s1"}}}
			h := serve.NewHandler(slog.New(slog.DiscardHandler), focusTasksDir(t), nil, f, nil, nil)
			rec := postFocus(t, h, tt.body, tt.contentType, tt.origin)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if len(f.focused) != 0 {
				t.Errorf("focused %v on a rejected request", f.focused)
			}
		})
	}
}
