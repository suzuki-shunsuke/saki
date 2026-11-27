// Package assignee implements the 'saki assignee' command.
package assignee

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/saki/pkg/config"
	"github.com/suzuki-shunsuke/saki/pkg/controller/assignee"
	"github.com/suzuki-shunsuke/saki/pkg/gh"
)

// Action runs the assignee command, writing the assignee to w.
type Action func(ctx context.Context, w io.Writer) error

// New creates the assignee command. getenv reads the environment variables locating the
// configuration file. action replaces what the command does, so that a test can run the
// command tree without gh; nil runs the real command.
func New(getenv func(string) string, action Action) *cobra.Command {
	if action == nil {
		action = func(ctx context.Context, w io.Writer) error {
			return run(ctx, w, getenv)
		}
	}
	return &cobra.Command{
		Use:   "assignee",
		Short: "Print the GitHub login to assign the tasks you create to",
		Long: `Print the GitHub login to assign the tasks you create to.

A coding agent following dev-flow or pr-review runs it when it creates a task, and writes
the login to assignees in the task's task.yaml. It tells whose task a task is when the
tasks directory is shared with git.

The login is decided in this order:

1. assignee in the configuration file
2. the login of the owner of gh's token, with "gh api /user"

It fails when neither gives a login. It also fails, without asking gh, when the file
given with SAKI_CONFIG doesn't exist or when the configuration file isn't valid YAML.

The configuration file is the first of these:

1. $SAKI_CONFIG
2. $XDG_CONFIG_HOME/saki/saki.yaml
3. ~/.config/saki/saki.yaml

  # The GitHub login written to assignees of the tasks you create
  assignee: octocat

Set assignee when gh's token isn't yours, such as a GitHub App installation access token
(gh api /user fails) or a machine user's token (it returns the machine user).

$ saki assignee
octocat`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return action(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func run(ctx context.Context, w io.Writer, getenv func(string) string) error {
	login, err := assignee.New(getenv, config.Read, gh.New()).Get(ctx)
	if err != nil {
		return fmt.Errorf("decide the assignee: %w", err)
	}
	fmt.Fprintln(w, login)
	return nil
}
