package mo

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeMo writes a script standing in for the mo command.
func fakeMo(t *testing.T, script string) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mo")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return &Client{command: path}
}

func TestClientOpen(t *testing.T) {
	t.Parallel()
	// The files are passed as separate arguments without a shell.
	c := fakeMo(t, `[ "$#" = 6 ] && [ "$1 $2 $3 $4" = "--no-open --json --target foo" ] && [ "$5" = '/t/foo/a b.md' ] && [ "$6" = '/t/foo/details.md' ] || exit 9
echo '{"files":[{"url":"http://localhost:6275/foo?file=1","path":"/t/foo/a b.md"},{"url":"http://localhost:6275/foo?file=2","path":"/t/foo/details.md"}]}'`)
	if err := c.Open(t.Context(), "foo", []string{"/t/foo/a b.md", "/t/foo/details.md"}); err != nil {
		t.Errorf("Open() = %v", err)
	}
}

func TestClientOpenPartial(t *testing.T) {
	t.Parallel()
	c := fakeMo(t, `echo '{"files":[{"url":"http://localhost:6275/foo?file=1","path":"/t/foo/foo.md"}]}'`)
	err := c.Open(t.Context(), "foo", []string{"/t/foo/foo.md", "/t/foo/details.md"})
	if _, ok := errors.AsType[*PartialError](err); !ok {
		t.Fatalf("Open() = %v, want a PartialError", err)
	}
	if got, want := err.Error(), "added 1 of 2 files"; got != want {
		t.Errorf("Open() = %q, want %q", got, want)
	}
}

func TestClientOpenError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		script  string
		wantMsg string
	}{
		{
			name: "mo error with usage",
			script: `echo 'Error: server did not become ready within 10s' >&2
echo 'Usage:' >&2
echo '  mo [flags] [files...]' >&2
exit 1`,
			wantMsg: "server did not become ready within 10s",
		},
		{
			name:    "no output",
			script:  `exit 2`,
			wantMsg: "run mo: exit status 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := fakeMo(t, tt.script).Open(t.Context(), "foo", []string{"/t/foo/foo.md"})
			if err == nil {
				t.Fatal("Open() returned no error")
			}
			if err.Error() != tt.wantMsg {
				t.Errorf("Open() = %q, want %q", err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestClientOpenNotInstalled(t *testing.T) {
	t.Parallel()
	c := &Client{command: "saki-test-no-such-mo-command"}
	if err := c.Open(t.Context(), "foo", []string{"/t/foo/foo.md"}); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Open() = %v, want ErrNotInstalled", err)
	}
}
