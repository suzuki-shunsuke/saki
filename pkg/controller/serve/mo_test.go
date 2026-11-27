package serve_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/suzuki-shunsuke/saki/pkg/controller/serve"
	"github.com/suzuki-shunsuke/saki/pkg/mo"
)

type fakeMo struct {
	err    error
	group  string
	files  []string
	called bool
}

func (f *fakeMo) Open(_ context.Context, group string, files []string) error {
	f.called = true
	f.group = group
	f.files = files
	return f.err
}

type moResult struct {
	Result  string `json:"result"`
	Message string `json:"message"`
	URL     string `json:"url"`
}

func postMo(t *testing.T, h http.Handler, body, contentType, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mo", strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func writeDoc(t *testing.T, tasksDir, dir, name string) string {
	t.Helper()
	p := filepath.Join(tasksDir, dir, name)
	if err := os.WriteFile(p, []byte("# doc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// moTasksDir creates tasks with documents and returns the directory and the absolute paths
// of the documents each task should open.
func moTasksDir(t *testing.T) (string, map[string][]string) {
	t.Helper()
	dir, err := filepath.Abs(emptyTasksDir(t))
	if err != nil {
		t.Fatal(err)
	}
	writeTask(t, dir, "all", "name: all\n")
	writeTask(t, dir, "old", "name: old\n")
	writeTask(t, dir, "both", "name: both\n")
	writeTask(t, dir, "details_only", "name: details_only\n")
	writeTask(t, dir, "empty", "name: empty\n")
	writeTask(t, dir, "review", "name: review\ntype: pr-review\n")
	writeTask(t, dir, "review_partial", "name: review_partial\ntype: pr-review\n")
	want := map[string][]string{
		"all": {
			writeDoc(t, dir, "all", "all.md"),
			writeDoc(t, dir, "all", "details.md"),
			writeDoc(t, dir, "all", "pr-explanation.md"),
		},
		// Old tasks have README.md as the design document.
		"old": {
			writeDoc(t, dir, "old", "README.md"),
		},
		// <dir>.md wins over README.md.
		"both": {
			writeDoc(t, dir, "both", "both.md"),
		},
		"details_only": {
			writeDoc(t, dir, "details_only", "details.md"),
		},
		// A review of pr-review has review.md as the main document.
		"review": {
			writeDoc(t, dir, "review", "review.md"),
			writeDoc(t, dir, "review", "details.md"),
			writeDoc(t, dir, "review", "pr-explanation.md"),
		},
		// <dir>.md and README.md aren't the main document of a review.
		"review_partial": {
			writeDoc(t, dir, "review_partial", "details.md"),
		},
	}
	writeDoc(t, dir, "both", "README.md")
	writeDoc(t, dir, "review_partial", "review_partial.md")
	writeDoc(t, dir, "review_partial", "README.md")
	// A directory isn't a document.
	if err := os.Mkdir(filepath.Join(dir, "empty", "details.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, want
}

type moCase struct {
	name string
	task string
	err  error
	want moResult
	// wantFiles is whether mo is called with the task's documents.
	wantFiles bool
}

func moCases() []moCase {
	return append(reviewMoCases(), []moCase{
		{
			name:      "all documents",
			task:      "all",
			want:      moResult{Result: "opened", Message: "Opened all in mo", URL: "http://localhost:6275/all"},
			wantFiles: true,
		},
		{
			name:      "README.md",
			task:      "old",
			want:      moResult{Result: "opened", Message: "Opened old in mo", URL: "http://localhost:6275/old"},
			wantFiles: true,
		},
		{
			name:      "design document over README.md",
			task:      "both",
			want:      moResult{Result: "opened", Message: "Opened both in mo", URL: "http://localhost:6275/both"},
			wantFiles: true,
		},
		{
			name:      "details.md only",
			task:      "details_only",
			want:      moResult{Result: "opened", Message: "Opened details_only in mo", URL: "http://localhost:6275/details_only"},
			wantFiles: true,
		},
		{
			name: "no document",
			task: "empty",
			want: moResult{Result: "no_document", Message: "empty has no document"},
		},
		{
			name: "unknown task",
			task: "../all",
			want: moResult{Result: "unknown_task", Message: "../all isn't found"},
		},
		{
			name:      "mo isn't installed",
			task:      "all",
			err:       mo.ErrNotInstalled,
			want:      moResult{Result: "not_installed", Message: "mo isn't installed. See https://github.com/k1LoW/mo"},
			wantFiles: true,
		},
		{
			name:      "mo fails",
			task:      "all",
			err:       &mo.Error{Message: "server did not become ready within 10s", Err: errors.New("exit status 1")},
			want:      moResult{Result: "error", Message: "Failed to open all in mo: server did not become ready within 10s"},
			wantFiles: true,
		},
		{
			name:      "mo adds only some files",
			task:      "all",
			err:       &mo.PartialError{Added: 1, Total: 3},
			want:      moResult{Result: "error", Message: "Failed to open all in mo: added 1 of 3 files"},
			wantFiles: true,
		},
	}...)
}

// reviewMoCases are the cases of reviews of pr-review, whose main document is review.md.
func reviewMoCases() []moCase {
	return []moCase{
		{
			name:      "review",
			task:      "review",
			want:      moResult{Result: "opened", Message: "Opened review in mo", URL: "http://localhost:6275/review"},
			wantFiles: true,
		},
		{
			name:      "review without review.md",
			task:      "review_partial",
			want:      moResult{Result: "opened", Message: "Opened review_partial in mo", URL: "http://localhost:6275/review_partial"},
			wantFiles: true,
		},
	}
}

func TestOpenInMo(t *testing.T) {
	t.Parallel()
	for _, tt := range moCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir, docs := moTasksDir(t)
			f := &fakeMo{err: tt.err}
			h := serve.NewHandler(slog.New(slog.DiscardHandler), dir, nil, nil, f, nil)
			body, err := json.Marshal(map[string]string{"task": tt.task})
			if err != nil {
				t.Fatal(err)
			}
			rec := postMo(t, h, string(body), "application/json", "http://127.0.0.1:8080")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			got := moResult{}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if !tt.wantFiles {
				if f.called {
					t.Errorf("mo was called with %v", f.files)
				}
				return
			}
			if f.group != tt.task {
				t.Errorf("group = %q, want %q", f.group, tt.task)
			}
			if diff := cmp.Diff(docs[tt.task], f.files); diff != "" {
				t.Errorf("files (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenInMoRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		body        string
		contentType string
		origin      string
		want        int
	}{
		{name: "another origin", body: `{"task":"all"}`, contentType: "application/json", origin: "http://evil.example.com", want: http.StatusForbidden},
		{name: "form", body: "task=all", contentType: "application/x-www-form-urlencoded", want: http.StatusForbidden},
		{name: "no content type", body: `{"task":"all"}`, want: http.StatusForbidden},
		{name: "invalid JSON", body: `{`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "no task", body: `{}`, contentType: "application/json", origin: "http://localhost:8080", want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir, _ := moTasksDir(t)
			f := &fakeMo{}
			h := serve.NewHandler(slog.New(slog.DiscardHandler), dir, nil, nil, f, nil)
			rec := postMo(t, h, tt.body, tt.contentType, tt.origin)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if f.called {
				t.Errorf("mo was called on a rejected request")
			}
		})
	}
}

func TestHandlerMoButton(t *testing.T) {
	t.Parallel()
	dir := emptyTasksDir(t)
	writeTask(t, dir, "foo", "name: foo\n")
	h := serve.NewHandler(slog.New(slog.DiscardHandler), dir, nil, nil, nil, nil)
	body := getIndex(h).Body.String()
	want := `<td data-col="name"><button type="button" class="mo-button" title="Open the documents in mo">mo</button><button type="button" class="start-button" title="Start a new Claude Code session for this task in a new herdr tab">start</button><div>foo</div></td>`
	if !strings.Contains(body, want) {
		t.Errorf("the page doesn't have the mo button in the name cell %q", want)
	}
	if got := strings.Count(body, `class="mo-button"`); got != 1 {
		t.Errorf("the page has %d mo buttons, want 1", got)
	}
	if got := strings.Count(body, `class="start-button"`); got != 1 {
		t.Errorf("the page has %d start buttons, want 1", got)
	}
}
