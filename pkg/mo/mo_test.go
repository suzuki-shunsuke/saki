package mo_test

import (
	"testing"

	"github.com/suzuki-shunsuke/saki/pkg/mo"
)

func TestErrorMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stderr string
		want   string
	}{
		{
			name: "error with usage",
			stderr: `Error: server did not become ready within 10s; the port may be in use
Usage:
  mo [flags] [files...]

Flags:
  -h, --help   help for mo
`,
			want: "server did not become ready within 10s; the port may be in use",
		},
		{
			name:   "no error line",
			stderr: "something failed\n\n",
			want:   "something failed",
		},
		{
			name:   "empty",
			stderr: "",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := mo.ErrorMessage(tt.stderr); got != tt.want {
				t.Errorf("ErrorMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCountAdded(t *testing.T) {
	t.Parallel()
	files := []string{"/tasks/foo/foo.md", "/tasks/foo/details.md"}
	tests := []struct {
		name string
		out  string
		want int
	}{
		{
			name: "added to the running server",
			out: `{"url":"http://localhost:6275","files":[
{"url":"http://localhost:6275/foo?file=a1","name":"foo.md","path":"/tasks/foo/foo.md"},
{"url":"http://localhost:6275/foo?file=a2","name":"details.md","path":"/tasks/foo/details.md"}]}`,
			want: 2,
		},
		{
			// When mo starts its server, the output has every file of every group, including
			// the files restored from the last session.
			name: "started a server with other groups",
			out: `{"url":"http://localhost:6275","files":[
{"url":"http://localhost:6275/?file=b1","name":"README.md","path":"/other/README.md"},
{"url":"http://localhost:6275/bar?file=b2","name":"bar.md","path":"/tasks/bar/bar.md"},
{"url":"http://localhost:6275/foo?file=a1","name":"foo.md","path":"/tasks/foo/foo.md"},
{"url":"http://localhost:6275/foo?file=a2","name":"details.md","path":"/tasks/foo/details.md"}]}`,
			want: 2,
		},
		{
			name: "the same file in another group",
			out: `{"url":"http://localhost:6275","files":[
{"url":"http://localhost:6275/?file=c1","name":"details.md","path":"/tasks/foo/details.md"},
{"url":"http://localhost:6275/foo?file=a1","name":"foo.md","path":"/tasks/foo/foo.md"}]}`,
			want: 1,
		},
		{
			name: "a file reported twice",
			out: `{"url":"http://localhost:6275","files":[
{"url":"http://localhost:6275/foo?file=a1","name":"foo.md","path":"/tasks/foo/foo.md"},
{"url":"http://localhost:6275/foo?file=a1","name":"foo.md","path":"/tasks/foo/foo.md"}]}`,
			want: 1,
		},
		{
			name: "no file accepted",
			out:  `{"url":"http://localhost:6275","files":null}`,
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := mo.CountAdded([]byte(tt.out), "foo", files)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("CountAdded() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCountAddedDefaultGroup(t *testing.T) {
	t.Parallel()
	out := `{"files":[{"url":"http://localhost:6275/?file=a1","path":"/x/default/default.md"}]}`
	got, err := mo.CountAdded([]byte(out), "default", []string{"/x/default/default.md"})
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("CountAdded() = %d, want 1", got)
	}
}

func TestCountAddedInvalid(t *testing.T) {
	t.Parallel()
	if _, err := mo.CountAdded([]byte("not json"), "foo", nil); err == nil {
		t.Error("CountAdded() returned no error for invalid output")
	}
}

func TestURL(t *testing.T) {
	t.Parallel()
	if got, want := mo.URL("task_web_ui_open_in_mo"), "http://localhost:6275/task_web_ui_open_in_mo"; got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
	if got, want := mo.URL("a b"), "http://localhost:6275/a%20b"; got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}
