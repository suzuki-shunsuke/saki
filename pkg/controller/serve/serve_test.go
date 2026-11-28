package serve_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/suzuki-shunsuke/saki/pkg/controller/serve"
	"github.com/suzuki-shunsuke/saki/pkg/task"
)

func newRequest(target, host string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	req.Host = host
	return req
}

// writeTask writes task.yaml of the task dir under the tasks directory.
func writeTask(t *testing.T, tasksDir, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(tasksDir, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, dir, "task.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// setMtime sets the modification time of a file.
func setMtime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// emptyTasksDir creates an empty tasks directory.
func emptyTasksDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// getIndex requests the task list and returns the response.
func getIndex(h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest("/", "127.0.0.1:8080"))
	return rec
}

func TestHandlerIndex(t *testing.T) {
	t.Parallel()
	dir := emptyTasksDir(t)
	writeTask(t, dir, "alpha", `name: alpha
description: first <task>
status: doing
tags: [saki, web]
repositories: [https://github.com/suzuki-shunsuke/saki]
issues: [owner/repo#1, "https://github.com/suzuki-shunsuke/saki/issues/1#issuecomment-2"]
pull_requests: ["javascript:alert(1)", https://github.com/suzuki-shunsuke/saki/pull/105]
claude_sessions:
  - id: abc
    description: wrote the overview
`)
	writeTask(t, dir, "beta", "name: beta\n")
	// beta is updated later, so it comes first even though its name sorts after alpha.
	// time.Local makes the expected text independent of the time zone the test runs in.
	updated := time.Date(2026, 11, 25, 9, 30, 0, 0, time.Local) //nolint:gosmopolitan
	setMtime(t, filepath.Join(dir, "alpha", "task.yaml"), updated)
	setMtime(t, filepath.Join(dir, "beta", "task.yaml"), updated.Add(time.Hour))
	h := serve.NewHandler(slog.New(slog.DiscardHandler), "", dir, nil, nil, nil, nil)
	rec := getIndex(h)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		// Every column has a checkbox and a header.
		`<input type="checkbox" data-col="name" checked>`,
		`<input type="checkbox" data-col="claude_sessions" checked>`,
		`<th data-col="pull_requests">pull_requests</th>`,
		// Values are escaped.
		`<td data-col="description"><div>first &lt;task&gt;</div></td>`,
		// A list has one line per element.
		`<td data-col="tags"><div>saki</div><div>web</div></td>`,
		// An http(s) URL is a link opened in a new tab, with the URL as its title.
		// The text of a link to a GitHub repository, issue, or pull request is shortened.
		`<a href="https://github.com/suzuki-shunsuke/saki" title="https://github.com/suzuki-shunsuke/saki" target="_blank" rel="noopener noreferrer">suzuki-shunsuke/saki</a>`,
		`<a href="https://github.com/suzuki-shunsuke/saki/pull/105" title="https://github.com/suzuki-shunsuke/saki/pull/105" target="_blank" rel="noopener noreferrer">suzuki-shunsuke/saki#105</a>`,
		// A URL of another form keeps its text.
		`<a href="https://github.com/suzuki-shunsuke/saki/issues/1#issuecomment-2" title="https://github.com/suzuki-shunsuke/saki/issues/1#issuecomment-2" target="_blank" rel="noopener noreferrer">https://github.com/suzuki-shunsuke/saki/issues/1#issuecomment-2</a>`,
		// Other values stay text.
		`<td data-col="issues"><div>owner/repo#1</div><div>`,
		`<td data-col="pull_requests"><div>javascript:alert(1)</div><div>`,
		`<td data-col="claude_sessions"><div>abc: wrote the overview</div></td>`,
		// updated_at is in the local time zone, by the minute.
		`<td data-col="updated_at"><div>2026-11-25 09:30</div></td>`,
		`<input type="checkbox" data-col="updated_at" checked>`,
		// An empty value is an empty cell.
		`<td data-col="description"></td>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page doesn't contain %s", want)
		}
	}
	if strings.Contains(body, "ZgotmplZ") {
		t.Error("the page contains a URL html/template rejected")
	}
	if a, b := strings.Index(body, ">alpha<"), strings.Index(body, ">beta<"); b < 0 || a < b {
		t.Error("the tasks aren't listed the most recently updated first")
	}
}

func TestHandlerHeader(t *testing.T) {
	t.Parallel()
	h := serve.NewHandler(slog.New(slog.DiscardHandler), "v1.2.3", tasksDir(t), nil, nil, nil, nil)
	body := getIndex(h).Body.String()
	// The whole elements are compared, because a task's repository may link to saki too.
	for _, want := range []string{
		`<span id="logo">saki</span>`,
		`<span id="version">v1.2.3</span>`,
		`<a id="repo-link" href="https://github.com/suzuki-shunsuke/saki" target="_blank" rel="noopener noreferrer">GitHub</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page doesn't contain %s", want)
		}
	}
	if strings.Contains(body, "<h1>Tasks</h1>") {
		t.Error("the page still has the heading Tasks")
	}
}

func TestHandlerReload(t *testing.T) {
	t.Parallel()
	dir := emptyTasksDir(t)
	writeTask(t, dir, "alpha", "name: alpha\nstatus: doing\n")
	h := serve.NewHandler(slog.New(slog.DiscardHandler), "", dir, nil, nil, nil, nil)
	if body := getIndex(h).Body.String(); !strings.Contains(body, ">doing<") {
		t.Fatalf("the first request doesn't show the task: %s", body)
	}

	// Editing, adding, and removing tasks show up on the next request.
	writeTask(t, dir, "alpha", "name: alpha\nstatus: done\n")
	writeTask(t, dir, "beta", "name: beta\n")
	body := getIndex(h).Body.String()
	if !strings.Contains(body, ">done<") || strings.Contains(body, ">doing<") {
		t.Errorf("the edit doesn't show up: %s", body)
	}
	if !strings.Contains(body, ">beta<") {
		t.Errorf("the added task doesn't show up: %s", body)
	}
	if err := os.RemoveAll(filepath.Join(dir, "alpha")); err != nil {
		t.Fatal(err)
	}
	if body := getIndex(h).Body.String(); strings.Contains(body, ">alpha<") {
		t.Errorf("the removed task still shows up: %s", body)
	}
}

func TestHandlerWarnsOnce(t *testing.T) {
	t.Parallel()
	dir := emptyTasksDir(t)
	writeTask(t, dir, "alpha", "name: alpha\n")
	// A task without task.yaml.
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := task.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h := serve.NewHandler(slog.New(slog.NewTextHandler(&logs, nil)), "", dir, result.Failures, nil, nil, nil)
	// The startup failure is warned when the handler is created, and requests don't repeat
	// it while the task stays unreadable for the same reason.
	getIndex(h)
	getIndex(h)
	if n := strings.Count(logs.String(), "task_dir=broken"); n != 1 {
		t.Errorf("the task was warned %d times, want 1: %s", n, logs.String())
	}
}

func TestHandlerTasksDirRemoved(t *testing.T) {
	t.Parallel()
	dir := emptyTasksDir(t)
	writeTask(t, dir, "alpha", "name: alpha\n")
	h := serve.NewHandler(slog.New(slog.DiscardHandler), "", dir, nil, nil, nil, nil)

	moved := dir + ".moved"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if rec := getIndex(h); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d without the tasks directory, want %d", rec.Code, http.StatusInternalServerError)
	}
	// The server recovers once the tasks directory is back.
	if err := os.Rename(moved, dir); err != nil {
		t.Fatal(err)
	}
	if rec := getIndex(h); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), ">alpha<") {
		t.Errorf("status = %d after the tasks directory is back, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandlerSearchBox(t *testing.T) {
	t.Parallel()
	dir := emptyTasksDir(t)
	writeTask(t, dir, "alpha", `name: alpha
description: '<b>"quoted" & more</b>'
status: in-review
tags: [saki, web]
repositories: [https://github.com/suzuki-shunsuke/saki]
issues: [https://github.com/suzuki-shunsuke/saki/issues/1#issuecomment-2]
pull_requests: [https://github.com/suzuki-shunsuke/saki/pull/105, https://github.com/suzuki-shunsuke/saki/pull/106]
`)
	h := serve.NewHandler(slog.New(slog.DiscardHandler), "", dir, nil, nil, nil, nil)
	body := getIndex(h).Body.String()
	for _, want := range []string{
		`<input type="search" id="search" placeholder="Filter by name or description, or by status:, tag:, repo:, issue:, pr:" aria-label="Filter by name or description, or by status:, tag:, repo:, issue:, pr:" autocomplete="off">`,
		`<span id="search-count" aria-live="polite"></span>`,
		`<p id="no-match" hidden>No tasks match.</p>`,
		// The form to start Claude Code for a new task.
		`<textarea id="launch-text" rows="4" placeholder="Describe a task to create with dev-flow, or paste a pull request URL to review"`,
		`<button type="submit" id="launch-button" disabled>Create task</button>`,
		// The pager and the choices of the page size.
		`<button type="button" id="page-prev">Prev</button>`,
		`<span id="page-info"></span>`,
		`<button type="button" id="page-next">Next</button>`,
		"<label>Per page <select id=\"page-size\">",
		`<option value="10">10</option>`,
		`<option value="30">30</option>`,
		`<option value="50">50</option>`,
		`<option value="100">100</option>`,
		`<option value="all">All</option>`,
		// The search box matches the name and the description kept in the row, escaped.
		// tag:, repo:, issue:, and pr: match the values separated by newlines: each URL and its
		// short form, or only the URL if it has no short form.
		`<tr data-dir="alpha" data-name="alpha" data-description="&lt;b&gt;&#34;quoted&#34; &amp; more&lt;/b&gt;" data-status="in-review" ` +
			"data-tags=\"saki\nweb\" " +
			"data-repos=\"https://github.com/suzuki-shunsuke/saki\nsuzuki-shunsuke/saki\" " +
			"data-issues=\"https://github.com/suzuki-shunsuke/saki/issues/1#issuecomment-2\" " +
			"data-prs=\"https://github.com/suzuki-shunsuke/saki/pull/105\nsuzuki-shunsuke/saki#105\n" +
			"https://github.com/suzuki-shunsuke/saki/pull/106\nsuzuki-shunsuke/saki#106\">",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page doesn't contain %s", want)
		}
	}
}

func TestHandlerColumnOrder(t *testing.T) {
	t.Parallel()
	h := serve.NewHandler(slog.New(slog.DiscardHandler), "", emptyTasksDir(t), nil, nil, nil, nil)
	rec := getIndex(h)

	matches := regexp.MustCompile(`<th data-col="([a-z_]+)">`).FindAllStringSubmatch(rec.Body.String(), -1)
	got := make([]string, 0, len(matches))
	for _, m := range matches {
		got = append(got, m[1])
	}
	want := []string{
		"name", "description", "type", "status", "updated_at", "tags", "parent_task", "sub_tasks",
		"derived_from", "derived_tasks", "repositories", "issues", "pull_requests", "claude_sessions",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("columns (-want +got):\n%s", diff)
	}
}

func TestHandlerStatus(t *testing.T) {
	t.Parallel()
	h := serve.NewHandler(slog.New(slog.DiscardHandler), "", emptyTasksDir(t), nil, nil, nil, nil)
	tests := []struct {
		name   string
		target string
		host   string
		want   int
	}{
		{name: "127.0.0.1", target: "/", host: "127.0.0.1:8080", want: http.StatusOK},
		{name: "localhost", target: "/", host: "localhost:8080", want: http.StatusOK},
		{name: "host without port", target: "/", host: "localhost", want: http.StatusOK},
		{name: "another path", target: "/foo", host: "127.0.0.1:8080", want: http.StatusNotFound},
		{name: "rebinding host", target: "/", host: "evil.example.com:8080", want: http.StatusForbidden},
		{name: "rebinding host on another path", target: "/foo", host: "evil.example.com", want: http.StatusForbidden},
		{name: "IPv6 loopback", target: "/", host: "[::1]:8080", want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, newRequest(tt.target, tt.host))
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

// freePort returns a port which is free at the moment.
func freePort(t *testing.T) int {
	t.Helper()
	lc := &net.ListenConfig{}
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func tasksDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "task.yaml"), []byte("name: alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A task without task.yaml is skipped with a warning.
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// syncBuffer is a buffer the server's goroutine can write logs into while the test reads it.
type syncBuffer struct {
	ch chan string
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.ch <- string(p)
	return len(p), nil
}

var urlPattern = regexp.MustCompile(`url=(http://127\.0\.0\.1:[0-9]+)`)

// waitForURL waits until Serve logs the URL, and returns it and the logs written by then.
func waitForURL(t *testing.T, logs *syncBuffer, errCh <-chan error) (string, string) {
	t.Helper()
	var got strings.Builder
	for {
		if m := urlPattern.FindStringSubmatch(got.String()); m != nil {
			return m[1], got.String()
		}
		select {
		case s := <-logs.ch:
			got.WriteString(s)
		case err := <-errCh:
			t.Fatalf("Serve() returned before starting: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("the URL wasn't logged: %s", got.String())
		}
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestServe(t *testing.T) {
	t.Parallel()
	logs := &syncBuffer{ch: make(chan string, 10)}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		// Port 0 lets the OS choose a free port, and the logged URL must have that port.
		errCh <- serve.Serve(ctx, logger, &serve.Param{TasksDir: tasksDir(t), Port: 0})
	}()

	url, got := waitForURL(t, logs, errCh)
	if !strings.Contains(got, "task_dir=broken") {
		t.Errorf("the task without task.yaml wasn't warned: %s", got)
	}
	if body := get(t, url); !strings.Contains(body, ">alpha<") {
		t.Errorf("the page doesn't list the task: %s", body)
	}

	// Canceling the context, as SIGINT does, is a normal stop.
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Serve() = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve() didn't stop")
	}
}

func TestServeBusyPort(t *testing.T) {
	t.Parallel()
	lc := &net.ListenConfig{}
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	if err := serve.Serve(t.Context(), logger, &serve.Param{TasksDir: tasksDir(t), Port: port}); err == nil {
		t.Error("Serve() returned no error for a busy port")
	}
	if strings.Contains(logs.String(), "url=") {
		t.Errorf("the URL was logged although listen failed: %s", logs.String())
	}
}

func TestServeNoTasksDir(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	param := &serve.Param{TasksDir: filepath.Join(t.TempDir(), "tasks"), Port: freePort(t)}
	if err := serve.Serve(t.Context(), logger, param); err == nil {
		t.Error("Serve() returned no error for a missing tasks directory")
	}
}

func TestServeStopWithIdleConnection(t *testing.T) {
	t.Parallel()
	logs := &syncBuffer{ch: make(chan string, 10)}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		errCh <- serve.Serve(ctx, logger, &serve.Param{TasksDir: tasksDir(t), Port: 0})
	}()
	url, _ := waitForURL(t, logs, errCh)

	// A connection which hasn't sent a request, like one a browser opens in advance,
	// mustn't turn a stop by a signal into an error.
	d := &net.Dialer{}
	conn, err := d.DialContext(t.Context(), "tcp", strings.TrimPrefix(url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Serve() = %v, want nil", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Serve() didn't stop")
	}
}
