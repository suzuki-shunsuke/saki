// Package gh runs the GitHub CLI.
package gh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// timeout limits a gh command, so that a hung network call doesn't block the agent.
const timeout = 30 * time.Second

// Client runs gh.
type Client struct {
	command string
}

// New creates a Client running the gh command in PATH.
func New() *Client {
	return &Client{command: "gh"}
}

var errEmptyLogin = errors.New("gh api /user returned an empty login")

// Login returns the login of the owner of gh's token, with "gh api /user".
func (c *Client) Login(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// The command and the arguments are fixed and there is no shell.
	cmd := exec.CommandContext(ctx, c.command, "api", "/user", "--jq", ".login") //nolint:gosec
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return "", fmt.Errorf("run gh api /user: %s: %w", s, err)
		}
		return "", fmt.Errorf("run gh api /user: %w", err)
	}
	login := strings.TrimSpace(stdout.String())
	if login == "" {
		return "", errEmptyLogin
	}
	return login, nil
}
