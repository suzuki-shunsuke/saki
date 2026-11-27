// Package assignee decides the GitHub login written to assignees of the tasks the person
// creates: assignee in the configuration file, else the login of the owner of gh's token.
package assignee

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/suzuki-shunsuke/saki/pkg/config"
)

// GitHub returns the login of the owner of the token.
type GitHub interface {
	Login(ctx context.Context) (string, error)
}

// Controller decides the assignee.
type Controller struct {
	getenv     func(string) string
	readConfig func(string) (*config.Config, error)
	gh         GitHub
}

// New creates a Controller. getenv reads the environment variables locating the
// configuration file, readConfig reads the file, and gh is asked when the file doesn't
// set assignee.
func New(getenv func(string) string, readConfig func(string) (*config.Config, error), gh GitHub) *Controller {
	return &Controller{getenv: getenv, readConfig: readConfig, gh: gh}
}

// Get returns the assignee.
//
// A missing file at the default location falls back to gh, but a missing file given with
// SAKI_CONFIG and a broken file are errors: falling back would hide the mistake.
func (c *Controller) Get(ctx context.Context) (string, error) {
	loc, err := config.Locate(c.getenv)
	if err != nil {
		return "", fmt.Errorf("locate the configuration file: %w", err)
	}
	cfg, err := c.readConfig(loc.Path)
	switch {
	case err == nil:
		if a := strings.TrimSpace(cfg.Assignee); a != "" {
			return a, nil
		}
	case errors.Is(err, fs.ErrNotExist) && !loc.Explicit:
	default:
		return "", err
	}
	login, err := c.gh.Login(ctx)
	if err != nil {
		return "", fmt.Errorf("%w. Set assignee in %s to decide the assignee without gh", err, loc.Path)
	}
	return login, nil
}
