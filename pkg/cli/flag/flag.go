// Package flag provides the command line flags shared by saki's commands.
package flag

import (
	"github.com/spf13/pflag"
	"github.com/suzuki-shunsuke/cobra-util/cobrautil"
)

// GlobalFlags holds the global flag values of the root command.
type GlobalFlags struct {
	LogLevel string
}

// Set registers the global flags on fs. fs is the root command's persistent flag
// set, so the flags can be given either before or after the subcommand.
func (gf *GlobalFlags) Set(fs *pflag.FlagSet) {
	fs.StringVar(&gf.LogLevel, "log-level", "", "log level (debug, info, warn, error)")
	cobrautil.Envs(fs, "log-level", "SAKI_LOG_LEVEL")
}
