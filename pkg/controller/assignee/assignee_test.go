package assignee_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"strings"
	"testing"

	"github.com/suzuki-shunsuke/saki/pkg/config"
	"github.com/suzuki-shunsuke/saki/pkg/controller/assignee"
)

type fakeGitHub struct {
	login  string
	err    error
	called bool
}

func (f *fakeGitHub) Login(context.Context) (string, error) {
	f.called = true
	return f.login, f.err
}

const defaultPath = "/home/foo/.config/saki/saki.yaml"

func TestController_Get(t *testing.T) { //nolint:funlen
	t.Parallel()
	tests := []struct {
		name       string
		env        map[string]string
		cfg        *config.Config
		readErr    error
		gh         *fakeGitHub
		want       string
		wantErr    string
		wantGHCall bool
	}{
		{
			name: "configuration file",
			cfg:  &config.Config{Assignee: " octocat \n"},
			gh:   &fakeGitHub{login: "gh-user"},
			want: "octocat",
		},
		{
			name:       "empty assignee falls back to gh",
			cfg:        &config.Config{},
			gh:         &fakeGitHub{login: "gh-user"},
			want:       "gh-user",
			wantGHCall: true,
		},
		{
			name:       "missing default file falls back to gh",
			readErr:    fmt.Errorf("read: %w", fs.ErrNotExist),
			gh:         &fakeGitHub{login: "gh-user"},
			want:       "gh-user",
			wantGHCall: true,
		},
		{
			name:    "missing file given with SAKI_CONFIG is an error",
			env:     map[string]string{"SAKI_CONFIG": "/a/saki.yaml"},
			readErr: fmt.Errorf("read: %w", fs.ErrNotExist),
			gh:      &fakeGitHub{login: "gh-user"},
			wantErr: "read",
		},
		{
			name:    "broken file is an error",
			readErr: errors.New("parse the configuration file"),
			gh:      &fakeGitHub{login: "gh-user"},
			wantErr: "parse the configuration file",
		},
		{
			name:       "gh fails",
			cfg:        &config.Config{},
			gh:         &fakeGitHub{err: errors.New("run gh api /user: HTTP 403")},
			wantErr:    "run gh api /user: HTTP 403. Set assignee in " + defaultPath,
			wantGHCall: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{"HOME": "/home/foo"}
			maps.Copy(env, tt.env)
			var readPath string
			read := func(p string) (*config.Config, error) {
				readPath = p
				return tt.cfg, tt.readErr
			}
			got, err := assignee.New(func(k string) string { return env[k] }, read, tt.gh).Get(t.Context())
			if tt.gh.called != tt.wantGHCall {
				t.Errorf("gh called: want %v, got %v", tt.wantGHCall, tt.gh.called)
			}
			wantPath := defaultPath
			if p, ok := env["SAKI_CONFIG"]; ok {
				wantPath = p
			}
			if readPath != wantPath {
				t.Errorf("read %q, want %q", readPath, wantPath)
			}
			checkResult(t, got, err, tt.want, tt.wantErr)
		})
	}
}

func checkResult(t *testing.T, got string, err error, want, wantErr string) {
	t.Helper()
	if wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("the error should contain %q: %v", wantErr, err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestController_Get_noHome(t *testing.T) {
	t.Parallel()
	gh := &fakeGitHub{login: "gh-user"}
	read := func(string) (*config.Config, error) { return &config.Config{}, nil }
	if _, err := assignee.New(func(string) string { return "" }, read, gh).Get(t.Context()); err == nil {
		t.Fatal("an error should be returned")
	}
}
