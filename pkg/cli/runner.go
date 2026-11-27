// Package cli provides saki's command line interface.
// It defines the command structure and wires the commands into the controllers,
// which hold the logic.
package cli

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/cobra-util/cobrautil"
	"github.com/suzuki-shunsuke/saki/pkg/cli/assignee"
	"github.com/suzuki-shunsuke/saki/pkg/cli/flag"
	"github.com/suzuki-shunsuke/saki/pkg/cli/serve"
	"github.com/suzuki-shunsuke/saki/pkg/cli/skill"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// Run creates and executes saki's command line interface.
func Run(ctx context.Context, logger *slogutil.Logger, env *cobrautil.Env) error {
	// The context reaches the commands through ExecuteContext below, which is what
	// cobra hands to the actions as cmd.Context(); building the tree needs none.
	cmd := newCommand(logger, env, &flag.GlobalFlags{}, &actions{}) //nolint:contextcheck
	if err := cmd.ExecuteContext(ctx); err != nil {
		return err //nolint:wrapcheck
	}
	return nil
}

// actions replaces what the commands do. A nil field runs the real command. It
// exists so that a test can run the real command tree, with its global flags and
// environment variables, and check the values the commands receive.
type actions struct {
	serve    serve.Action
	assignee assignee.Action
}

// newCommand builds the command tree. It is separate from Run so that a test can
// run the real tree with its output captured and its actions replaced.
func newCommand(logger *slogutil.Logger, env *cobrautil.Env, gFlags *flag.GlobalFlags, acts *actions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "saki",
		Short: "A framework to work with coding agents",
		Long: `A framework to work with coding agents.

See https://github.com/suzuki-shunsuke/saki for details.`,
	}
	gFlags.Set(cmd.PersistentFlags())
	cmd.AddCommand(
		serve.New(logger, gFlags, acts.serve),
		skill.New(),
		assignee.New(env.Getenv, acts.assignee),
	)
	return cobrautil.Command(env, cmd, nil)
}
