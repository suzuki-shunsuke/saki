package main

import (
	"github.com/suzuki-shunsuke/cobra-util/cobrautil"
	"github.com/suzuki-shunsuke/saki/pkg/cli"
)

var version = ""

func main() {
	cobrautil.Main("saki", version, cli.Run)
}
