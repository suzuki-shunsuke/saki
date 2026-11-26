package cli

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/suzuki-shunsuke/cobra-util/cobrautil"
	"github.com/suzuki-shunsuke/saki/pkg/cli/flag"
	"github.com/suzuki-shunsuke/saki/pkg/cli/serve"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// program is the name of the program, which is the first element of the arguments.
const program = "saki"

// received records the flag values each command's action was called with.
type received struct {
	Serve *serve.Args
}

// execute runs the real command tree with args and env, with the actions replaced
// so that nothing is run but the values they receive are recorded.
func execute(t *testing.T, args []string, env map[string]string) (*received, string, error) {
	t.Helper()
	got := &received{}
	acts := &actions{
		serve: func(_ context.Context, a *serve.Args) error {
			got.Serve = a
			return nil
		},
	}
	logger := &slogutil.Logger{Logger: slog.New(slog.DiscardHandler)}
	cmd := newCommand(logger, &cobrautil.Env{
		Program: program,
		Version: "v1.2.3",
		Getenv: func(k string) string {
			return env[k]
		},
		Args: append([]string{program}, args...),
	}, &flag.GlobalFlags{}, acts)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	err := cmd.ExecuteContext(t.Context())
	return got, out.String(), err
}

func TestNewCommand_flags(t *testing.T) { //nolint:funlen
	t.Parallel()
	tests := []struct {
		name string
		args []string
		env  map[string]string
		exp  *received
	}{
		{
			name: "serve defaults",
			args: []string{"serve"},
			exp: &received{Serve: &serve.Args{
				GlobalFlags: &flag.GlobalFlags{},
				Port:        8080,
			}},
		},
		{
			name: "serve flags",
			args: []string{"serve", "--port", "3000", "--log-level", "debug"},
			exp: &received{Serve: &serve.Args{
				GlobalFlags: &flag.GlobalFlags{LogLevel: "debug"},
				Port:        3000,
			}},
		},
		{
			name: "serve environment variables",
			args: []string{"serve"},
			env:  map[string]string{"SAKI_PORT": "9000", "SAKI_LOG_LEVEL": "info"},
			exp: &received{Serve: &serve.Args{
				GlobalFlags: &flag.GlobalFlags{LogLevel: "info"},
				Port:        9000,
			}},
		},
		{
			name: "flags win over environment variables",
			args: []string{"serve", "--port", "3000"},
			env:  map[string]string{"SAKI_PORT": "9000"},
			exp: &received{Serve: &serve.Args{
				GlobalFlags: &flag.GlobalFlags{},
				Port:        3000,
			}},
		},
		{
			name: "an empty environment variable is unset",
			args: []string{"serve"},
			env:  map[string]string{"SAKI_PORT": ""},
			exp: &received{Serve: &serve.Args{
				GlobalFlags: &flag.GlobalFlags{},
				Port:        8080,
			}},
		},
		{
			name: "global flags before the subcommand",
			args: []string{"--log-level", "error", "serve"},
			exp: &received{Serve: &serve.Args{
				GlobalFlags: &flag.GlobalFlags{LogLevel: "error"},
				Port:        8080,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, _, err := execute(t, tt.args, tt.env)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.exp, got); diff != "" {
				t.Fatalf("(-want, +got)\n%s", diff)
			}
		})
	}
}

func TestNewCommand_errors(t *testing.T) {
	t.Parallel()
	// An error returned here makes cobrautil.Main exit with 1, since none of these
	// errors carries an exit code.
	tests := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{name: "unknown subcommand", args: []string{"foo"}},
		{name: "positional argument", args: []string{"serve", "foo"}},
		{name: "help after a subcommand", args: []string{"serve", "help"}},
		{name: "single dash long flag", args: []string{"serve", "-port", "3000"}},
		{name: "removed config flag", args: []string{"serve", "--config", "foo.yaml"}},
		{name: "removed subcommand", args: []string{"track-items"}},
		{name: "unknown flag", args: []string{"serve", "--foo"}},
		{name: "invalid value", args: []string{"serve", "--port", "abc"}},
		{name: "invalid environment variable", args: []string{"serve"}, env: map[string]string{"SAKI_PORT": "abc"}},
		{name: "h alias", args: []string{"h", "serve"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, _, err := execute(t, tt.args, tt.env)
			if err == nil {
				t.Fatal("an error should be returned")
			}
			if diff := cmp.Diff(&received{}, got); diff != "" {
				t.Fatalf("no action should run (-want, +got)\n%s", diff)
			}
		})
	}
}

func TestNewCommand_version(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		exp  string
	}{
		{name: "--version", args: []string{"--version"}, exp: "saki version v1.2.3\n"},
		{name: "-v", args: []string{"-v"}, exp: "saki version v1.2.3\n"},
		{name: "version", args: []string{"version"}, exp: "v1.2.3\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, out, err := execute(t, tt.args, nil)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.exp, out); diff != "" {
				t.Fatalf("(-want, +got)\n%s", diff)
			}
		})
	}
}

func TestNewCommand_help(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"--help"}, {"help", "serve"}, {"serve", "-h"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			got, out, err := execute(t, args, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "Usage:") {
				t.Fatalf("the help should be output: %q", out)
			}
			if diff := cmp.Diff(&received{}, got); diff != "" {
				t.Fatalf("no action should run (-want, +got)\n%s", diff)
			}
		})
	}
}
