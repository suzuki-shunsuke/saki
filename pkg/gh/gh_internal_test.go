package gh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH returns a Client running a shell script in place of gh.
func fakeGH(t *testing.T, script string) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return &Client{command: path}
}

func TestClient_Login(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		script  string
		want    string
		wantErr string
	}{
		{
			name:   "login",
			script: `[ "$*" = "api /user --jq .login" ] || exit 2; echo octocat`,
			want:   "octocat",
		},
		{
			name:    "failure with a message",
			script:  "echo 'HTTP 403: Resource not accessible by integration' >&2; exit 1",
			wantErr: "run gh api /user: HTTP 403: Resource not accessible by integration",
		},
		{
			name:    "failure without a message",
			script:  "exit 1",
			wantErr: "run gh api /user: exit status 1",
		},
		{
			name:    "empty login",
			script:  "echo",
			wantErr: "empty login",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := fakeGH(t, tt.script).Login(t.Context())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("the error should contain %q: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("want %q, got %q", tt.want, got)
			}
		})
	}
}

func TestClient_Login_notFound(t *testing.T) {
	t.Parallel()
	c := &Client{command: filepath.Join(t.TempDir(), "no-gh")}
	if _, err := c.Login(t.Context()); err == nil {
		t.Fatal("an error should be returned")
	}
}
