package claudesession_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suzuki-shunsuke/saki/pkg/claudesession"
)

const id = "0b7c0c8e-1d2f-4a3b-9c4d-5e6f7a8b9c0d"

func TestIsValidID(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		id:                                     true,
		"0B7C0C8E-1D2F-4A3B-9C4D-5E6F7A8B9C0D": true,
		"":                                     false,
		"s1":                                   false,
		"0b7c0c8e-1d2f-4a3b-9c4d-5e6f7a8b9c0d; x": false,
		"../0b7c0c8e-1d2f-4a3b-9c4d-5e6f7a8b9c0":  false,
		"0b7c0c8e-1d2f-4a3b-9c4d-5e6f7a8b9c0g":    false,
	}
	for in, want := range tests {
		if got := claudesession.IsValidID(in); got != want {
			t.Errorf("IsValidID(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestConfigDir(t *testing.T) {
	t.Parallel()
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "CLAUDE_CONFIG_DIR" {
				return v
			}
			return ""
		}
	}
	if got := claudesession.ConfigDir(env("/c"), "/home/u"); got != "/c" {
		t.Errorf("ConfigDir() = %q, want /c", got)
	}
	if got := claudesession.ConfigDir(env(""), "/home/u"); got != filepath.Join("/home/u", ".claude") {
		t.Errorf("ConfigDir() = %q, want /home/u/.claude", got)
	}
	if got := claudesession.ConfigDir(env(""), ""); got != "" {
		t.Errorf("ConfigDir() = %q, want empty", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFind(t *testing.T) {
	t.Parallel()
	// A glob's special characters in the directory don't matter.
	configDir := filepath.Join(t.TempDir(), "[claude]")
	if _, err := claudesession.Find(configDir, id); !errors.Is(err, claudesession.ErrNotFound) {
		t.Errorf("Find() without projects = %v, want ErrNotFound", err)
	}
	older := filepath.Join(configDir, "projects", "-a", id+".jsonl")
	newer := filepath.Join(configDir, "projects", "-b", id+".jsonl")
	writeFile(t, older, "{}\n")
	writeFile(t, newer, "{}\n")
	writeFile(t, filepath.Join(configDir, "projects", "-c", "other.jsonl"), "{}\n")
	now := time.Now()
	if err := os.Chtimes(older, now, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, now, now); err != nil {
		t.Fatal(err)
	}
	got, err := claudesession.Find(configDir, id)
	if err != nil || got != newer {
		t.Errorf("Find() = %q, %v, want %q", got, err, newer)
	}
	if _, err := claudesession.Find(configDir, "1b7c0c8e-1d2f-4a3b-9c4d-5e6f7a8b9c0d"); !errors.Is(err, claudesession.ErrNotFound) {
		t.Errorf("Find() of another session = %v, want ErrNotFound", err)
	}
	if _, err := claudesession.Find(configDir, "../x"); err == nil || errors.Is(err, claudesession.ErrNotFound) {
		t.Errorf("Find() of an invalid ID = %v, want an error", err)
	}
	if _, err := claudesession.Find("", id); !errors.Is(err, claudesession.ErrNotFound) {
		t.Errorf("Find() without a config dir = %v, want ErrNotFound", err)
	}
}

func TestDirs(t *testing.T) {
	t.Parallel()
	// A line longer than the block lastDir reads, so that a line spans blocks.
	long := `{"type":"user","message":"` + strings.Repeat("x", 200<<10) + `","cwd":"/long"}`
	tests := []struct {
		name      string
		content   string
		wantFirst string
		wantLast  string
	}{
		{
			name:      "first and last",
			content:   `{"type":"summary"}` + "\n" + `{"cwd":"/a"}` + "\n" + `{"cwd":"/b"}` + "\n" + `{"cwd":"/c"}` + "\n",
			wantFirst: "/a",
			wantLast:  "/c",
		},
		{
			// "cwd" in a tool's output isn't the top-level key.
			name:      "cwd not at the top level",
			content:   `{"cwd":"/a"}` + "\n" + `{"cwd":"/b"}` + "\n" + `{"toolUseResult":{"stdout":"\"cwd\":\"/x\""}}` + "\n" + `{"message":{"cwd":"/y"}}` + "\n",
			wantFirst: "/a",
			wantLast:  "/b",
		},
		{
			name:      "no newline at the end",
			content:   `{"cwd":"/a"}` + "\n" + `{"cwd":"/b"}`,
			wantFirst: "/a",
			wantLast:  "/b",
		},
		{
			name:      "long lines",
			content:   long + "\n" + `{"cwd":"/a"}` + "\n" + long + "\n" + strings.Repeat(`{"type":"x"}`+"\n", 10000),
			wantFirst: "/long",
			wantLast:  "/long",
		},
		{
			name:    "no cwd",
			content: `{"type":"summary"}` + "\n" + `{"cwd":""}` + "\n" + "not json\n",
		},
		{
			name: "empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), id+".jsonl")
			writeFile(t, path, tt.content)
			first, last, err := claudesession.Dirs(path)
			if err != nil {
				t.Fatal(err)
			}
			if first != tt.wantFirst || last != tt.wantLast {
				t.Errorf("Dirs() = %q, %q, want %q, %q", first, last, tt.wantFirst, tt.wantLast)
			}
		})
	}
	if _, _, err := claudesession.Dirs(filepath.Join(t.TempDir(), "none.jsonl")); err == nil {
		t.Error("Dirs() of a missing file returned no error")
	}
}
