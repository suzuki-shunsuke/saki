// Package config reads saki's configuration file.
//
// The file belongs to a person rather than a repository, so it's in the person's home
// directory: $SAKI_CONFIG, else $XDG_CONFIG_HOME/saki/saki.yaml, else
// ~/.config/saki/saki.yaml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
)

// Config is the content of the configuration file.
type Config struct {
	// Assignee is the GitHub login written to assignees of the tasks the person creates.
	Assignee string `yaml:"assignee"`
}

// Location is where the configuration file is.
type Location struct {
	Path string
	// Explicit reports whether the path was given with SAKI_CONFIG rather than being the
	// default. A missing file is an error only when the path was given explicitly.
	Explicit bool
}

var errNoHome = errors.New("neither SAKI_CONFIG, XDG_CONFIG_HOME nor HOME is set")

// Locate returns where the configuration file is, reading the environment variables with
// getenv.
func Locate(getenv func(string) string) (*Location, error) {
	if p := getenv("SAKI_CONFIG"); p != "" {
		return &Location{Path: p, Explicit: true}, nil
	}
	if d := getenv("XDG_CONFIG_HOME"); d != "" {
		return &Location{Path: filepath.Join(d, "saki", "saki.yaml")}, nil
	}
	if d := getenv("HOME"); d != "" {
		return &Location{Path: filepath.Join(d, ".config", "saki", "saki.yaml")}, nil
	}
	return nil, errNoHome
}

// Read reads the configuration file at p. Unknown keys are ignored so that an older saki
// can read a file written for a newer one. The error wraps fs.ErrNotExist when the file
// doesn't exist.
func Read(p string) (*Config, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read the configuration file: %w", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse the configuration file %s: %w", p, err)
	}
	return cfg, nil
}
