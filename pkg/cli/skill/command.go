// Package skill implements the 'saki skill' command, which prints the skills
// (dev-flow and pr-review) embedded in saki.
package skill

import (
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/saki/flows"
	"github.com/suzuki-shunsuke/saki/pkg/controller/skill"
)

// PermissionHelp tells how to let Claude Code run `saki skill show` without asking
// each time. It is shared with the help of the serve command.
const PermissionHelp = `Claude Code asks for approval each time it runs "saki skill show" unless the
command is allowed. To allow it, add "Bash(saki skill show:*)" to permissions.allow
in the settings of Claude Code (e.g. ~/.claude/settings.json):

  {
    "permissions": {
      "allow": ["Bash(saki skill show:*)"]
    }
  }`

// New creates the skill command. It writes to the command's output, which is stdout
// unless a test sets it with SetOut.
func New() *cobra.Command {
	ctrl := skill.New(flows.FS)
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Print the skills embedded in saki",
		Long: `Print the skills embedded in saki.

saki embeds the skills dev-flow and pr-review. A coding agent reads a skill by running
"saki skill show <name>" and follows it, so the skill always matches the installed saki,
and you don't need to install the skill. saki serve starts Claude Code with a prompt
telling it to run the command.

$ saki skill list
$ saki skill show dev-flow
$ saki skill show dev-flow templates/design.md

` + PermissionHelp + `

To start a skill by hand, e.g. by typing /dev-flow in Claude Code, install the thin skill,
which only tells the agent to run "saki skill show":

  gh skill install suzuki-shunsuke/saki dev-flow --agent claude-code --scope user`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List the skills",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return ctrl.List(cmd.OutOrStdout())
			},
		},
		&cobra.Command{
			Use:   "show <name> [<path>]",
			Short: "Print a skill or a file of a skill",
			Long: `Print a skill or a file of a skill.

Without <path>, it prints the body of the skill. With <path>, it prints the file of the
skill that the body refers to, such as templates/design.md.

$ saki skill show dev-flow
$ saki skill show dev-flow templates/design.md`,
			Args: cobra.RangeArgs(1, 2), //nolint:mnd
			RunE: func(cmd *cobra.Command, args []string) error {
				p := ""
				if len(args) == 2 { //nolint:mnd
					p = args[1]
				}
				return ctrl.Show(cmd.OutOrStdout(), args[0], p)
			},
		},
	)
	return cmd
}
