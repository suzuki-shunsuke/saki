package flows_test

import (
	"bytes"
	"io/fs"
	"regexp"
	"testing"

	"github.com/suzuki-shunsuke/saki/flows"
	"github.com/suzuki-shunsuke/saki/pkg/controller/skill"
)

// TestFS_list checks that every skill is listed with a summary.
func TestFS_list(t *testing.T) {
	t.Parallel()
	list := &bytes.Buffer{}
	if err := skill.New(flows.FS).List(list); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dev-flow", "pr-review"} {
		if !regexp.MustCompile(`(?m)^` + name + ` +\S`).Match(list.Bytes()) {
			t.Errorf("%s should be listed with a summary:\n%s", name, list)
		}
	}
}

// ref matches "saki skill show <name> [<path>]" with a concrete name.
var ref = regexp.MustCompile("saki skill show ([a-z-]+)(?: ([A-Za-z0-9_./-]+[A-Za-z0-9_]))?")

// TestFS_references checks that every skill and file the skills refer to with
// "saki skill show <name> [<path>]" exists.
func TestFS_references(t *testing.T) {
	t.Parallel()
	ctrl := skill.New(flows.FS)
	n := 0
	for _, p := range files(t) {
		b, err := fs.ReadFile(flows.FS, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ref.FindAllSubmatch(b, -1) {
			n++
			if err := ctrl.Show(&bytes.Buffer{}, string(m[1]), string(m[2])); err != nil {
				t.Errorf("%s refers to %q: %v", p, m[0], err)
			}
		}
	}
	if n == 0 {
		t.Fatal("no reference is found")
	}
}

func files(t *testing.T) []string {
	t.Helper()
	var paths []string
	err := fs.WalkDir(flows.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}
