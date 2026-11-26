// Package mo calls the mo CLI, the Markdown viewer (https://github.com/k1LoW/mo), to open
// a task's documents in a group of its own.
package mo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// timeout limits a call of the mo CLI. When no mo server is running, mo starts one in the
// background and waits up to 10 seconds for it after a short probe. The timeout is longer so
// that mo's own error comes first.
const timeout = 15 * time.Second

// Addr is the address of the mo server. Only mo's default port is supported.
const Addr = "localhost:6275"

// defaultGroup is mo's group whose URL has no path.
const defaultGroup = "default"

// ErrNotInstalled means the mo command isn't found in PATH.
var ErrNotInstalled = errors.New("mo isn't installed")

// Client calls the mo CLI.
type Client struct {
	command string
}

// New creates a Client running the mo command in PATH.
func New() *Client {
	return &Client{command: "mo"}
}

// Open opens the files in the group without letting mo open a browser. files must be
// absolute paths. It fails unless mo reports all the files in the group: mo exits with 0
// even when the running server doesn't accept a file.
func (c *Client) Open(ctx context.Context, group string, files []string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append([]string{"--no-open", "--json", "--target", group}, files...)
	// The command is mo, the group is a task directory name found in the task list, and the
	// files are absolute paths. It runs without a shell, so the arguments can't inject a
	// command. Stdin is left nil (/dev/null), so mo doesn't read the files from stdin.
	cmd := exec.CommandContext(ctx, c.command, args...) //nolint:gosec
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrNotInstalled
		}
		if msg := ErrorMessage(stderr.String()); msg != "" {
			return &Error{Message: msg, Err: err}
		}
		return fmt.Errorf("run mo: %w", err)
	}
	added, err := CountAdded(stdout.Bytes(), group, files)
	if err != nil {
		return err
	}
	if added < len(files) {
		return &PartialError{Added: added, Total: len(files)}
	}
	return nil
}

// Error is an error mo printed. Its message is mo's message alone, without the exit status,
// so that it can be shown to the user. Err is the error of the command.
type Error struct {
	Message string
	Err     error
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) Unwrap() error {
	return e.Err
}

// PartialError means mo added only some of the files to the group.
type PartialError struct {
	Added int
	Total int
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("added %d of %d files", e.Added, e.Total)
}

// ErrorMessage returns the message of mo's error from its standard error, or "".
// mo prints "Error: <message>" followed by the usage, so the message is the line after
// "Error: ". Without it, the last non-empty line is used.
func ErrorMessage(stderr string) string {
	last := ""
	for line := range strings.Lines(stderr) {
		line = strings.TrimSpace(line)
		if msg, ok := strings.CutPrefix(line, "Error: "); ok {
			return msg
		}
		if line != "" {
			last = line
		}
	}
	return last
}

// CountAdded parses the output of "mo --json" and counts the files which mo reports in the
// group. The output has every file of every group when mo starts its server (it restores
// the last session), so the files are matched by both the path and the group.
func CountAdded(out []byte, group string, files []string) (int, error) {
	var v struct {
		Files []struct {
			URL  string `json:"url"`
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return 0, fmt.Errorf("parse the output of mo: %w", err)
	}
	wanted := make(map[string]bool, len(files))
	for _, f := range files {
		wanted[f] = true
	}
	added := 0
	for _, f := range v.Files {
		if !wanted[f.Path] || !inGroup(f.URL, group) {
			continue
		}
		// A file counts once even if mo reports it twice.
		wanted[f.Path] = false
		added++
	}
	return added, nil
}

// inGroup reports whether the URL of a file mo reports is in the group.
// mo's URL is http://<addr>/<group>?file=<id>, or http://<addr>/?file=<id> for the default
// group.
func inGroup(fileURL, group string) bool {
	u, err := url.Parse(fileURL)
	if err != nil {
		return false
	}
	p := strings.TrimPrefix(u.Path, "/")
	if p == "" {
		p = defaultGroup
	}
	return p == group
}

// URL returns the URL of the group's page.
func URL(group string) string {
	return "http://" + Addr + "/" + url.PathEscape(group)
}
