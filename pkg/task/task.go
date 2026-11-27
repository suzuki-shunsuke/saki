// Package task reads the tasks managed with the dev-flow skill.
// Each task is a directory under the tasks directory, and its metadata is in the
// directory's task.yaml.
package task

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// DefaultDir is the directory the tasks are read from.
const DefaultDir = "tasks"

// FileName is the name of the file holding a task's metadata.
const FileName = "task.yaml"

// TypePRReview is the type of a review of a pull request created by someone else, which
// is managed with the pr-review flow rather than dev-flow.
const TypePRReview = "pr-review"

// Task is the metadata of a task, read from its task.yaml.
type Task struct {
	Name           string           `yaml:"name"`
	Description    string           `yaml:"description"`
	Type           string           `yaml:"type"`
	Status         string           `yaml:"status"`
	Tags           []string         `yaml:"tags"`
	ClaudeSessions []*ClaudeSession `yaml:"claude_sessions"`
	ParentTask     string           `yaml:"parent_task"`
	SubTasks       []string         `yaml:"sub_tasks"`
	DerivedFrom    string           `yaml:"derived_from"`
	DerivedTasks   []string         `yaml:"derived_tasks"`
	Repositories   []string         `yaml:"repositories"`
	Issues         []string         `yaml:"issues"`
	PullRequests   []string         `yaml:"pull_requests"`

	// Dir is the task's directory name. It isn't read from task.yaml.
	Dir string `yaml:"-"`
	// UpdatedAt is the latest modification time of the files in the task's directory.
	// See updatedAt for the files counted. It isn't read from task.yaml.
	UpdatedAt time.Time `yaml:"-"`
}

// ClaudeSession is a Claude Code session related to a task.
type ClaudeSession struct {
	ID          string `yaml:"id"`
	Description string `yaml:"description"`
}

// Failure is a task directory whose task.yaml couldn't be read.
type Failure struct {
	// Dir is the task's directory name.
	Dir string
	Err error
}

// Result is the tasks read from a tasks directory.
type Result struct {
	// Tasks are the tasks read successfully, the most recently updated first.
	// UpdatedAt is compared by the minute, which is how it's displayed, and tasks updated in
	// the same minute are ordered by their directory names. Comparing the exact times would
	// list the tasks written by one git operation in the reverse order of their names,
	// because git writes the files in the order of their paths.
	Tasks []*Task
	// Failures are the task directories skipped because their task.yaml couldn't be read,
	// ordered by their directory names.
	Failures []*Failure
}

// errNotFile is returned when task.yaml exists but isn't a regular file.
var errNotFile = errors.New("task.yaml isn't a regular file")

// Read reads the task.yaml of every directory directly under dir.
// Files directly under dir are ignored.
// A directory whose task.yaml is missing, unreadable, or doesn't match the expected types
// doesn't fail the whole read; it's returned as a Failure instead.
func Read(dir string) (*Result, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read the tasks directory: %w", err)
	}
	// os.ReadDir returns the entries sorted by name, which gives the order of Failures.
	result := &Result{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t, err := readFile(filepath.Join(dir, entry.Name(), FileName))
		if err != nil {
			result.Failures = append(result.Failures, &Failure{Dir: entry.Name(), Err: err})
			continue
		}
		t.Dir = entry.Name()
		t.UpdatedAt = updatedAt(filepath.Join(dir, entry.Name()))
		result.Tasks = append(result.Tasks, t)
	}
	slices.SortFunc(result.Tasks, compareTasks)
	return result, nil
}

// compareTasks orders the tasks the most recently updated first, by the minute, and then by
// their directory names.
func compareTasks(a, b *Task) int {
	if c := b.UpdatedAt.Truncate(time.Minute).Compare(a.UpdatedAt.Truncate(time.Minute)); c != 0 {
		return c
	}
	return strings.Compare(a.Dir, b.Dir)
}

// updatedAt returns the latest modification time of the regular files under dir,
// including subdirectories.
// Files and directories whose names start with "." (such as .DS_Store and vim's swap files),
// files whose names end with "~" (editor backups), and symlinks aren't counted, so that
// files a file manager or an editor creates don't move a task up.
// Entries which can't be read are skipped rather than failing the task.
func updatedAt(dir string) time.Time {
	var latest time.Time
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return walkDirAction(path, dir, d, err)
		}
		if t, ok := modTime(d); ok && t.After(latest) {
			latest = t
		}
		return nil
	})
	return latest
}

// walkDirAction decides whether to walk into a directory, and skips an entry which couldn't
// be read. d is nil when root itself can't be read.
func walkDirAction(path, root string, d fs.DirEntry, err error) error {
	if d == nil || path == root {
		return nil
	}
	if d.IsDir() && (err != nil || strings.HasPrefix(d.Name(), ".")) {
		return fs.SkipDir
	}
	return nil
}

// modTime returns the modification time of a file, and false if the file isn't counted.
func modTime(d fs.DirEntry) (time.Time, bool) {
	name := d.Name()
	if !d.Type().IsRegular() || strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
		return time.Time{}, false
	}
	info, err := d.Info()
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func readFile(path string) (*Task, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat task.yaml: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errNotFile
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read task.yaml: %w", err)
	}
	t := &Task{}
	if err := yaml.Unmarshal(b, t); err != nil {
		return nil, fmt.Errorf("parse task.yaml: %w", err)
	}
	return t, nil
}
