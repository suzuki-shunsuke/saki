// Package serve implements the 'saki serve' command.
// It wires the flags into the serve controller.
package serve

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/cobra-util/cobrautil"
	"github.com/suzuki-shunsuke/saki/pkg/claudesession"
	"github.com/suzuki-shunsuke/saki/pkg/cli/flag"
	"github.com/suzuki-shunsuke/saki/pkg/cli/skill"
	"github.com/suzuki-shunsuke/saki/pkg/controller/serve"
	"github.com/suzuki-shunsuke/saki/pkg/task"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// Args holds the flag values of the serve command.
type Args struct {
	*flag.GlobalFlags

	Port int
}

// Action runs the serve command with the parsed flag values.
type Action func(ctx context.Context, args *Args) error

// New creates the serve command. action replaces what the command does, so that a
// test can check the parsed flag values without starting the server; nil runs the
// server.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags, action Action) *cobra.Command {
	args := &Args{
		GlobalFlags: gFlags,
	}
	if action == nil {
		action = func(ctx context.Context, args *Args) error {
			return run(ctx, logger, args)
		}
	}
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start a web server listing the tasks",
		Long: `Start a web server listing the tasks.

It serves a page listing the tasks on 127.0.0.1. Every time the page is opened, it reads
task.yaml of every directory under the tasks directory of the current directory, so changes
to task.yaml show up on reload.
It stops on SIGINT (Ctrl-C) or SIGTERM.

Clicking a task's row focuses the herdr pane where Claude Code runs the task's session, the
one written last in claude_sessions among those with a pane. If no session has a pane, it
resumes the session written last (claude --resume) in a new herdr tab, in the directory the
session was last working in. This needs herdr's Claude Code integration
(herdr integration install claude), which tells herdr the session of each pane.

The form at the top of the page starts Claude Code in a new herdr tab. Claude Code is
told to run "saki skill show dev-flow" (or pr-review) and follow it, so saki needs to be
in PATH there. saki serve needs to run in a herdr pane.

` + skill.PermissionHelp + `

$ saki serve
$ saki serve --port 3000`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return action(cmd.Context(), args)
		},
	}
	fs := cmd.Flags()
	fs.IntVar(&args.Port, "port", serve.DefaultPort, "port to listen on")
	cobrautil.Envs(fs, "port", "SAKI_PORT")
	return cmd
}

func run(ctx context.Context, logger *slogutil.Logger, args *Args) error {
	if err := logger.SetLevel(args.LogLevel); err != nil {
		return fmt.Errorf("set log level: %w", err)
	}
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get the current directory: %w", err)
	}
	// Without a home directory, only CLAUDE_CONFIG_DIR tells where the sessions are.
	home, _ := os.UserHomeDir()
	return serve.Serve(ctx, logger.Logger, &serve.Param{ //nolint:wrapcheck
		TasksDir:        task.DefaultDir,
		Port:            args.Port,
		HerdrPaneID:     os.Getenv("HERDR_PANE_ID"),
		Dir:             dir,
		ClaudeConfigDir: claudesession.ConfigDir(os.Getenv, home),
	})
}
