// Package herdr calls the herdr CLI, the terminal workspace manager running the coding
// agents, to find the pane of a Claude Code session and focus it.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// timeout limits each call of the herdr CLI, except StartAgent.
const timeout = 5 * time.Second

// startMargin is how much longer than herdr's own timeout the process of "agent start" may
// run, so that herdr reports the timeout rather than the process being killed.
const startMargin = 5 * time.Second

// Error codes herdr returns.
const (
	// CodeServerNotRunning means herdr's server isn't running.
	CodeServerNotRunning = "server_not_running"
	// CodePaneBusy means the pane isn't at its interactive shell prompt, e.g. right after
	// it's created.
	CodePaneBusy = "agent_pane_busy"
	// CodeAgentNotReady means the agent stopped during startup, e.g. at a dialog.
	CodeAgentNotReady = "agent_not_ready"
	// CodeAgentBlocked means the agent is waiting at a dialog, so a prompt wasn't sent.
	CodeAgentBlocked = "agent_blocked"
)

// Agent is a coding agent herdr recognizes in a pane.
type Agent struct {
	PaneID string
	// Kind is the kind of the agent, such as "claude".
	Kind string
	// SessionID is the agent's session ID, empty unless an integration reported it.
	SessionID string
}

// Tab is a herdr tab.
type Tab struct {
	TabID       string
	Label       string
	WorkspaceID string
}

// Error is an error herdr returned.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

// Client calls the herdr CLI.
type Client struct {
	command string
}

// New creates a Client running the herdr command in PATH.
func New() *Client {
	return &Client{command: "herdr"}
}

// ListAgents returns the agents herdr recognizes.
func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	out, err := c.run(ctx, timeout, "agent", "list")
	if err != nil {
		return nil, err
	}
	return ParseAgents(out)
}

// Focus brings the pane to the front in herdr.
func (c *Client) Focus(ctx context.Context, paneID string) error {
	_, err := c.run(ctx, timeout, "agent", "focus", paneID)
	return err
}

// ListTabs returns the tabs of all workspaces, in herdr's order.
func (c *Client) ListTabs(ctx context.Context) ([]Tab, error) {
	out, err := c.run(ctx, timeout, "tab", "list")
	if err != nil {
		return nil, err
	}
	return ParseTabs(out)
}

// FocusTab brings the tab to the front in herdr, switching to its workspace.
func (c *Client) FocusTab(ctx context.Context, tabID string) error {
	_, err := c.run(ctx, timeout, "tab", "focus", tabID)
	return err
}

// PaneWorkspace returns the workspace the pane belongs to.
func (c *Client) PaneWorkspace(ctx context.Context, paneID string) (string, error) {
	out, err := c.run(ctx, timeout, "pane", "get", paneID)
	if err != nil {
		return "", err
	}
	var v struct {
		Result struct {
			Pane struct {
				WorkspaceID string `json:"workspace_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("parse the output of herdr pane get: %w", err)
	}
	if v.Result.Pane.WorkspaceID == "" {
		return "", errNoWorkspace
	}
	return v.Result.Pane.WorkspaceID, nil
}

// CreateTab creates a tab in the workspace, brings it to the front, and returns its pane.
func (c *Client) CreateTab(ctx context.Context, workspaceID, cwd, label string) (string, error) {
	out, err := c.run(ctx, timeout, "tab", "create", "--workspace", workspaceID, "--cwd", cwd, "--label", label, "--focus")
	if err != nil {
		return "", err
	}
	var v struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("parse the output of herdr tab create: %w", err)
	}
	if v.Result.RootPane.PaneID == "" {
		return "", errNoPane
	}
	return v.Result.RootPane.PaneID, nil
}

// StartAgent starts Claude Code named name in the pane, and waits up to wait until it's
// ready for input. herdr is given the wait, because its default is 30 seconds.
func (c *Client) StartAgent(ctx context.Context, name, paneID string, wait time.Duration) error {
	_, err := c.run(ctx, wait+startMargin, "agent", "start", name, "--kind", "claude", "--pane", paneID,
		"--timeout", strconv.FormatInt(wait.Milliseconds(), 10))
	return err
}

// Prompt submits the text to the agent. The text is one argument, without a shell.
func (c *Client) Prompt(ctx context.Context, name, text string) error {
	_, err := c.run(ctx, timeout, "agent", "prompt", name, text)
	return err
}

var (
	errNoWorkspace = errors.New("herdr pane get returned no workspace")
	errNoPane      = errors.New("herdr tab create returned no pane")
)

// run runs herdr without a shell. When herdr fails, it reads the error herdr writes to the
// standard error as JSON, and falls back to the output or the error of the command.
func (c *Client) run(ctx context.Context, limit time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	// The command is herdr, and it runs without a shell, so the arguments (pane IDs herdr
	// returned, the directory of saki serve, and the text of a prompt) can't inject a command.
	cmd := exec.CommandContext(ctx, c.command, args...) //nolint:gosec
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if herdrErr := parseError(stderr.Bytes()); herdrErr != nil {
			return nil, herdrErr
		}
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return nil, fmt.Errorf("%s: %w", s, err)
		}
		return nil, fmt.Errorf("run herdr: %w", err)
	}
	return stdout.Bytes(), nil
}

func parseError(b []byte) *Error {
	var v struct {
		Error *Error `json:"error"`
	}
	// Output which isn't the JSON error leaves v.Error nil.
	_ = json.Unmarshal(b, &v)
	if v.Error == nil || v.Error.Message == "" {
		return nil
	}
	return v.Error
}

// ParseAgents parses the output of "herdr agent list".
func ParseAgents(b []byte) ([]Agent, error) {
	var v struct {
		Result struct {
			Agents []struct {
				PaneID       string `json:"pane_id"`
				Agent        string `json:"agent"`
				AgentSession *struct {
					Value string `json:"value"`
				} `json:"agent_session"`
			} `json:"agents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("parse the output of herdr agent list: %w", err)
	}
	agents := make([]Agent, 0, len(v.Result.Agents))
	for _, a := range v.Result.Agents {
		agent := Agent{PaneID: a.PaneID, Kind: a.Agent}
		if a.AgentSession != nil {
			agent.SessionID = a.AgentSession.Value
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

// ParseTabs parses the output of "herdr tab list".
func ParseTabs(b []byte) ([]Tab, error) {
	var v struct {
		Result struct {
			Tabs []struct {
				TabID       string `json:"tab_id"`
				Label       string `json:"label"`
				WorkspaceID string `json:"workspace_id"`
			} `json:"tabs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("parse the output of herdr tab list: %w", err)
	}
	tabs := make([]Tab, 0, len(v.Result.Tabs))
	for _, t := range v.Result.Tabs {
		tabs = append(tabs, Tab{TabID: t.TabID, Label: t.Label, WorkspaceID: t.WorkspaceID})
	}
	return tabs, nil
}

// IsNotRunning reports whether the error means herdr's server isn't running.
func IsNotRunning(err error) bool {
	return HasCode(err, CodeServerNotRunning)
}

// HasCode reports whether the error is a herdr error with the code.
func HasCode(err error, code string) bool {
	var herdrErr *Error
	return errors.As(err, &herdrErr) && herdrErr.Code == code
}
