// Package claudesession reads the records Claude Code keeps of its sessions, to resume a
// session in the directory it was working in.
//
// Claude Code writes a session to <config dir>/projects/<the directory it started in, with
// "/" and others replaced by "-">/<session ID>.jsonl. Each line is a JSON object, and most
// have "cwd", the directory Claude Code was working in at the time.
package claudesession

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// ErrNotFound means no record of the session was found.
var ErrNotFound = errors.New("no record of the session")

// idPattern is the form of a session ID, a UUID. Only an ID of this form is put in a path
// or passed to Claude Code.
var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsValidID reports whether id has the form of a session ID.
func IsValidID(id string) bool {
	return idPattern.MatchString(id)
}

// ConfigDir returns Claude Code's configuration directory: CLAUDE_CONFIG_DIR, or .claude
// in the home directory. It's empty when neither is known.
func ConfigDir(getenv func(string) string, home string) string {
	if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	if home != "" {
		return filepath.Join(home, ".claude")
	}
	return ""
}

// Find returns the path of the record of the session id in configDir. When records of the
// session are in several directories, the one updated last wins. id must be valid (see
// IsValidID). The directories are listed rather than globbed, so that configDir may hold
// characters special to a glob.
func Find(configDir, id string) (string, error) {
	if !IsValidID(id) {
		return "", fmt.Errorf("invalid session ID: %s", id)
	}
	if configDir == "" {
		return "", ErrNotFound
	}
	projects := filepath.Join(configDir, "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("read the directory of the session records: %w", err)
	}
	found := newestRecord(projects, entries, id)
	if found == "" {
		return "", ErrNotFound
	}
	return found, nil
}

// newestRecord returns the record of the session id updated last in the directories
// entries of projects, or "".
func newestRecord(projects string, entries []os.DirEntry, id string) string {
	var found string
	var modTime time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(projects, e.Name(), id+".jsonl")
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if found == "" || info.ModTime().After(modTime) {
			found = p
			modTime = info.ModTime()
		}
	}
	return found
}

// Dirs returns the first and the last working directory recorded in the record at path,
// or empty strings when none is recorded. Only "cwd" at the top level of a line counts:
// the output of a tool may hold the text "cwd" too. A record can be tens of megabytes, so
// the first is read from the start and the last from the end, each stopping at the first
// line found.
func Dirs(path string) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("open the session record: %w", err)
	}
	defer f.Close()
	first, err := firstDir(f)
	if err != nil {
		return "", "", err
	}
	if first == "" {
		return "", "", nil
	}
	info, err := f.Stat()
	if err != nil {
		return "", "", fmt.Errorf("get the size of the session record: %w", err)
	}
	last, err := lastDir(f, info.Size())
	if err != nil {
		return "", "", err
	}
	return first, last, nil
}

const cwdKey = `"cwd"`

// lineDir returns the top-level "cwd" of the line, or "" when it has none.
func lineDir(line []byte) string {
	// Most lines without the key are skipped without parsing them.
	if !bytes.Contains(line, []byte(cwdKey)) {
		return ""
	}
	var v struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(line, &v); err != nil {
		return ""
	}
	return v.Cwd
}

// firstDir reads lines from the start without a limit on their length, because a line can
// be megabytes long.
func firstDir(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if d := lineDir(line); d != "" {
			return d, nil
		}
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("read the session record: %w", err)
		}
	}
}

// blockSize is how much lastDir reads from the end at a time.
const blockSize = 64 << 10

// lastDir reads lines from the end of r, size bytes long, a block at a time.
func lastDir(r io.ReaderAt, size int64) (string, error) {
	// rest is the part read so far which hasn't been split into lines: it starts in the
	// middle of a line, unless pos is 0.
	var rest []byte
	pos := size
	for pos > 0 {
		n := min(int64(blockSize), pos)
		pos -= n
		block := make([]byte, n, n+int64(len(rest)))
		if _, err := r.ReadAt(block, pos); err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read the session record: %w", err)
		}
		rest = append(block, rest...)
		for {
			i := bytes.LastIndexByte(rest, '\n')
			if i < 0 {
				break
			}
			if d := lineDir(rest[i+1:]); d != "" {
				return d, nil
			}
			rest = rest[:i]
		}
	}
	return lineDir(rest), nil
}
