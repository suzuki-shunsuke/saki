package skill_test

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
	"github.com/suzuki-shunsuke/saki/pkg/controller/skill"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"foo/SKILL.md":          {Data: []byte("---\nname: foo\nsummary: Do foo\ndescription: long\n---\n\n## Foo\n\nbody\n")},
		"foo/templates/a.md":    {Data: []byte("template a\n")},
		"foo/scripts/b.py":      {Data: []byte("print(1)\n")},
		"barbaz/SKILL.md":       {Data: []byte("---\nname: barbaz\nsummary: Do bar and baz\n---\nbarbaz body\n")},
		"broken/templates/x.md": {Data: []byte("x\n")},
		"broken/SKILL.md":       {Data: []byte("no front matter\n")},
		"crlf/SKILL.md":         {Data: []byte("---\r\nname: crlf\r\nsummary: Do crlf\r\n---\r\n\r\nbody\r\n")},
		"unterminated/SKILL.md": {Data: []byte("---\nname: u\n")},
	}
}

func TestController_List(t *testing.T) {
	t.Parallel()
	fsys := testFS()
	delete(fsys, "broken/SKILL.md")
	delete(fsys, "broken/templates/x.md")
	delete(fsys, "unterminated/SKILL.md")
	delete(fsys, "crlf/SKILL.md")
	buf := &bytes.Buffer{}
	if err := skill.New(fsys).List(buf); err != nil {
		t.Fatal(err)
	}
	want := "barbaz  Do bar and baz\nfoo     Do foo\n"
	if diff := cmp.Diff(want, buf.String()); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestController_List_brokenFrontMatter(t *testing.T) {
	t.Parallel()
	if err := skill.New(testFS()).List(&bytes.Buffer{}); err == nil {
		t.Fatal("an error should be returned")
	}
}

func TestController_Show(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		skill   string
		path    string
		want    string
		wantErr string
	}{
		{name: "body without front matter", skill: "foo", want: "## Foo\n\nbody\n"},
		{name: "body right after the front matter", skill: "barbaz", want: "barbaz body\n"},
		{name: "CRLF", skill: "crlf", want: "body\n"},
		{name: "file", skill: "foo", path: "templates/a.md", want: "template a\n"},
		{name: "nested file", skill: "foo", path: "scripts/b.py", want: "print(1)\n"},
		{name: "unknown skill", skill: "qux", wantErr: `the skill "qux" isn't found. Available skills: barbaz, broken, crlf, foo, unterminated`},
		{name: "unknown file", skill: "foo", path: "nope.md", wantErr: `the file "nope.md" isn't found in the skill foo. Available files: scripts/b.py, templates/a.md`},
		{name: "SKILL.md as a path", skill: "foo", path: "SKILL.md", wantErr: `the file "SKILL.md" isn't found`},
		{name: "path escaping the skill", skill: "foo", path: "../barbaz/SKILL.md", wantErr: `the file "../barbaz/SKILL.md" isn't found`},
		{name: "no front matter", skill: "broken", wantErr: "the front matter isn't found"},
		{name: "unterminated front matter", skill: "unterminated", wantErr: "the front matter isn't found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			buf := &bytes.Buffer{}
			err := skill.New(testFS()).Show(buf, tt.skill, tt.path)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, buf.String()); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}
