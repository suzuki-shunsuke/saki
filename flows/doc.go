// Package flows embeds the flows (dev-flow and pr-review) that saki gives to coding
// agents. Each flow is a directory holding SKILL.md and the files it refers to, and
// `saki skill show` prints them.
//
// The directory isn't named skills, because `gh skill install` also finds nested
// skills/*/SKILL.md and would see two skills of the same name: these and the thin
// skills under the repository's skills directory.
package flows

import "embed"

// FS holds the flows. Files whose names start with "." or "_", such as __pycache__,
// are left out by go:embed.
//
//go:embed dev-flow pr-review
var FS embed.FS
