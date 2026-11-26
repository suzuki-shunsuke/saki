package herdr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeHerdr writes a script standing in for the herdr command.
func fakeHerdr(t *testing.T, script string) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return &Client{command: path}
}

func TestClientListAgents(t *testing.T) {
	t.Parallel()
	c := fakeHerdr(t, `[ "$1 $2" = "agent list" ] || exit 9
echo '{"result":{"agents":[{"agent":"claude","pane_id":"w1:p1","agent_session":{"value":"s1"}}]}}'`)
	agents, err := c.ListAgents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].PaneID != "w1:p1" || agents[0].SessionID != "s1" {
		t.Errorf("ListAgents() = %+v", agents)
	}
}

func TestClientFocusPassesPaneID(t *testing.T) {
	t.Parallel()
	// The pane ID is passed as one argument without a shell.
	c := fakeHerdr(t, `[ "$#" = 3 ] && [ "$1 $2" = "agent focus" ] && [ "$3" = 'w1:p1; echo x' ] || exit 9`)
	if err := c.Focus(t.Context(), "w1:p1; echo x"); err != nil {
		t.Errorf("Focus() = %v", err)
	}
}

func TestClientError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		script     string
		wantMsg    string
		notRunning bool
	}{
		{
			name:    "herdr error",
			script:  `echo '{"error":{"code":"agent_not_found","message":"agent w1:p9 not found"},"id":"x"}' >&2; exit 1`,
			wantMsg: "agent w1:p9 not found",
		},
		{
			name:       "server not running",
			script:     `echo '{"error":{"code":"server_not_running","message":"herdr server is not running"}}' >&2; exit 1`,
			wantMsg:    "herdr server is not running",
			notRunning: true,
		},
		{
			name:    "not JSON",
			script:  `echo 'error: unexpected argument' >&2; exit 2`,
			wantMsg: "error: unexpected argument: exit status 2",
		},
		{
			name:    "no output",
			script:  `exit 3`,
			wantMsg: "run herdr: exit status 3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := fakeHerdr(t, tt.script).Focus(t.Context(), "w1:p1")
			if err == nil {
				t.Fatal("Focus() returned no error")
			}
			if err.Error() != tt.wantMsg {
				t.Errorf("error = %q, want %q", err.Error(), tt.wantMsg)
			}
			if IsNotRunning(err) != tt.notRunning {
				t.Errorf("IsNotRunning() = %v, want %v", IsNotRunning(err), tt.notRunning)
			}
		})
	}
}

func TestClientMissingCommand(t *testing.T) {
	t.Parallel()
	c := &Client{command: filepath.Join(t.TempDir(), "no-herdr")}
	_, err := c.ListAgents(t.Context())
	if err == nil || errors.As(err, new(*Error)) {
		t.Errorf("ListAgents() = %v, want a non-herdr error", err)
	}
}

func TestClientLaunchCommands(t *testing.T) {
	t.Parallel()
	// Each command checks its arguments; the prompt text is one argument, even with a
	// newline and shell syntax.
	c := fakeHerdr(t, `case "$1 $2" in
"pane get") [ "$3" = w1:p1 ] || exit 9; echo '{"result":{"pane":{"pane_id":"w1:p1","workspace_id":"w3"}}}' ;;
"tab create") [ "$*" = "tab create --workspace w3 --cwd /repo/saki --label new task --focus" ] || exit 9
  echo '{"result":{"root_pane":{"pane_id":"w3:p7"},"tab":{"tab_id":"w3:t2"},"type":"tab_created"}}' ;;
"agent start") [ "$*" = "agent start task-x --kind claude --pane w3:p7 --timeout 1500" ] || exit 9 ;;
"agent prompt") [ "$#" = 4 ] && [ "$3" = task-x ] && [ "$4" = "$(printf '/dev-flow x\n$(echo y); z')" ] || exit 9 ;;
*) exit 9 ;;
esac`)
	ctx := t.Context()
	ws, err := c.PaneWorkspace(ctx, "w1:p1")
	if err != nil || ws != "w3" {
		t.Fatalf("PaneWorkspace() = %q, %v", ws, err)
	}
	pane, err := c.CreateTab(ctx, "w3", "/repo/saki", "new task")
	if err != nil || pane != "w3:p7" {
		t.Fatalf("CreateTab() = %q, %v", pane, err)
	}
	if err := c.StartAgent(ctx, "task-x", "w3:p7", 1500*time.Millisecond); err != nil {
		t.Errorf("StartAgent() = %v", err)
	}
	if err := c.Prompt(ctx, "task-x", "/dev-flow x\n$(echo y); z"); err != nil {
		t.Errorf("Prompt() = %v", err)
	}
}

func TestClientTabCommands(t *testing.T) {
	t.Parallel()
	// "tab list" lists every workspace's tabs, so it has no --workspace. The tab ID is
	// passed as one argument.
	c := fakeHerdr(t, `case "$1 $2" in
"tab list") [ "$#" = 2 ] || exit 9
  echo '{"result":{"tabs":[{"tab_id":"w3:t2","label":"review a/b#1","workspace_id":"w3"}]}}' ;;
"tab focus") [ "$#" = 3 ] && [ "$3" = 'w3:t2; echo x' ] || exit 9 ;;
*) exit 9 ;;
esac`)
	tabs, err := c.ListTabs(t.Context())
	if err != nil || len(tabs) != 1 || tabs[0].TabID != "w3:t2" || tabs[0].Label != "review a/b#1" {
		t.Fatalf("ListTabs() = %+v, %v", tabs, err)
	}
	if err := c.FocusTab(t.Context(), "w3:t2; echo x"); err != nil {
		t.Errorf("FocusTab() = %v", err)
	}
}

func TestHasCode(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("wrapped: %w", &Error{Code: CodePaneBusy, Message: "busy"})
	if !HasCode(err, CodePaneBusy) || HasCode(err, CodeAgentBlocked) || HasCode(errors.New("x"), CodePaneBusy) {
		t.Error("HasCode() is wrong")
	}
}
