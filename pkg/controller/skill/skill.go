// Package skill prints the skills embedded in saki: the list of them, a skill's body,
// and the files a skill refers to.
package skill

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/goccy/go-yaml"
)

// skillFile is the file holding a skill's front matter and body.
const skillFile = "SKILL.md"

// Controller prints the skills in fsys, where each top-level directory is a skill.
type Controller struct {
	fsys fs.FS
}

// New creates a Controller reading the skills from fsys.
func New(fsys fs.FS) *Controller {
	return &Controller{fsys: fsys}
}

type frontMatter struct {
	Summary string `yaml:"summary"`
}

// List writes the name and the one-line summary of every skill.
func (c *Controller) List(w io.Writer) error {
	names, err := c.names()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) //nolint:mnd
	for _, name := range names {
		b, err := fs.ReadFile(c.fsys, path.Join(name, skillFile))
		if err != nil {
			return fmt.Errorf("read %s of the skill %s: %w", skillFile, name, err)
		}
		fm, _, err := split(b)
		if err != nil {
			return fmt.Errorf("parse %s of the skill %s: %w", skillFile, name, err)
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", name, fm.Summary); err != nil {
			return fmt.Errorf("write the skill list: %w", err)
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write the skill list: %w", err)
	}
	return nil
}

// Show writes the body of the skill name without its front matter, or, when p isn't
// empty, the file p of the skill as it is.
func (c *Controller) Show(w io.Writer, name, p string) error {
	names, err := c.names()
	if err != nil {
		return err
	}
	if !slices.Contains(names, name) {
		return fmt.Errorf("the skill %q isn't found. Available skills: %s", name, strings.Join(names, ", "))
	}
	if p == "" {
		b, err := fs.ReadFile(c.fsys, path.Join(name, skillFile))
		if err != nil {
			return fmt.Errorf("read %s of the skill %s: %w", skillFile, name, err)
		}
		_, body, err := split(b)
		if err != nil {
			return fmt.Errorf("parse %s of the skill %s: %w", skillFile, name, err)
		}
		return write(w, body)
	}
	files, err := c.files(name)
	if err != nil {
		return err
	}
	if !slices.Contains(files, p) {
		return fmt.Errorf("the file %q isn't found in the skill %s. Available files: %s", p, name, strings.Join(files, ", "))
	}
	b, err := fs.ReadFile(c.fsys, path.Join(name, p))
	if err != nil {
		return fmt.Errorf("read the file %s of the skill %s: %w", p, name, err)
	}
	return write(w, b)
}

func write(w io.Writer, b []byte) error {
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("write the output: %w", err)
	}
	return nil
}

// names returns the names of the skills, sorted.
func (c *Controller) names() ([]string, error) {
	entries, err := fs.ReadDir(c.fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read the skills: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// files returns the paths of the files a skill holds besides SKILL.md, relative to the
// skill's directory and sorted.
func (c *Controller) files(name string) ([]string, error) {
	var files []string
	err := fs.WalkDir(c.fsys, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, name+"/")
		if rel != skillFile {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list the files of the skill %s: %w", name, err)
	}
	return files, nil
}

var errNoFrontMatter = errors.New("the front matter isn't found")

// split splits SKILL.md into its front matter and its body. CRLF line endings are
// read as LF, so that a SKILL.md checked out with CRLF on Windows is parsed too.
func split(b []byte) (*frontMatter, []byte, error) {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	const sep = "---\n"
	if !bytes.HasPrefix(b, []byte(sep)) {
		return nil, nil, errNoFrontMatter
	}
	rest := b[len(sep):]
	idx := bytes.Index(rest, []byte("\n"+sep))
	if idx == -1 {
		return nil, nil, errNoFrontMatter
	}
	fm := &frontMatter{}
	if err := yaml.Unmarshal(rest[:idx+1], fm); err != nil {
		return nil, nil, fmt.Errorf("parse the front matter: %w", err)
	}
	body := bytes.TrimLeft(rest[idx+1+len(sep):], "\n")
	return fm, body, nil
}
