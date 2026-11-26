package serve

import (
	"bytes"
	_ "embed"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/suzuki-shunsuke/saki/pkg/task"
)

//go:embed index.html
var indexHTML string

var indexTemplate = template.Must(template.New("index").Parse(indexHTML)) //nolint:gochecknoglobals

// column is a column of the task list: one per key of task.yaml, and updated_at.
type column struct {
	// Key is the key of task.yaml, or updated_at. It also identifies the column in the
	// page's script and in the hidden columns saved in the browser.
	Key   string
	cells func(t *task.Task) []item
}

// item is one line in a cell. A list value has one item per element.
type item struct {
	Text string
	// URL is set when the item is rendered as a link.
	URL string
}

type cell struct {
	Key   string
	Items []item
}

// row is a row of the task list. The fields other than Cells are what the search box
// matches. Tags, Repos, Issues, and PRs are values separated by newlines.
type row struct {
	// Dir is the task's directory name, which a click on the row sends to focus its pane.
	Dir         string
	Name        string
	Description string
	Status      string
	Tags        string
	Repos       string
	Issues      string
	PRs         string
	Cells       []cell
}

// searchValues returns the values tag: matches, separated by newlines.
func searchValues(values []string) string {
	return strings.Join(values, "\n")
}

// searchURLs returns the values repo:, issue:, and pr: match, separated by newlines: each URL
// as written in task.yaml and, if the URL has one, its short form shown in the list.
func searchURLs(urls []string) string {
	values := make([]string, 0, 2*len(urls)) //nolint:mnd
	for _, u := range urls {
		values = append(values, u)
		if ref, ok := shortGitHubRef(u); ok {
			values = append(values, ref)
		}
	}
	return searchValues(values)
}

type pageData struct {
	Columns []column
	Rows    []row
}

// columns are the columns of the task list, in the order they are shown.
// claude_sessions is last because its values are long.
var columns = []column{ //nolint:gochecknoglobals
	{Key: "name", cells: func(t *task.Task) []item { return texts(t.Name) }},
	{Key: "description", cells: func(t *task.Task) []item { return texts(t.Description) }},
	{Key: "type", cells: func(t *task.Task) []item { return texts(t.Type) }},
	{Key: "status", cells: func(t *task.Task) []item { return texts(t.Status) }},
	{Key: "updated_at", cells: updatedAt},
	{Key: "tags", cells: func(t *task.Task) []item { return texts(t.Tags...) }},
	{Key: "parent_task", cells: func(t *task.Task) []item { return texts(t.ParentTask) }},
	{Key: "sub_tasks", cells: func(t *task.Task) []item { return texts(t.SubTasks...) }},
	{Key: "derived_from", cells: func(t *task.Task) []item { return texts(t.DerivedFrom) }},
	{Key: "derived_tasks", cells: func(t *task.Task) []item { return texts(t.DerivedTasks...) }},
	{Key: "repositories", cells: func(t *task.Task) []item { return links(t.Repositories) }},
	{Key: "issues", cells: func(t *task.Task) []item { return links(t.Issues) }},
	{Key: "pull_requests", cells: func(t *task.Task) []item { return links(t.PullRequests) }},
	{Key: "claude_sessions", cells: claudeSessions},
}

// texts returns an item per non-empty value.
func texts(values ...string) []item {
	items := make([]item, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		items = append(items, item{Text: v})
	}
	return items
}

// links returns an item per non-empty value, and makes the http and https URLs links.
// The text of a link to a GitHub repository, issue, or pull request is shortened (see
// shortGitHubRef); the link still points to the original URL.
// Other values stay text: html/template would replace a URL of another scheme with
// "#ZgotmplZ" and render a broken link.
func links(values []string) []item {
	items := texts(values...)
	for i, it := range items {
		if strings.HasPrefix(it.Text, "http://") || strings.HasPrefix(it.Text, "https://") {
			items[i].URL = it.Text
			if ref, ok := shortGitHubRef(it.Text); ok {
				items[i].Text = ref
			}
		}
	}
	return items
}

// updatedAtFormat is the format of updated_at. Tasks are ordered by the minute, so seconds
// aren't shown.
const updatedAtFormat = "2006-01-02 15:04"

// updatedAt shows the time in the time zone of the machine running saki serve.
func updatedAt(t *task.Task) []item {
	if t.UpdatedAt.IsZero() {
		return nil
	}
	// The spec shows the time in the local time zone of the machine running saki serve.
	return []item{{Text: t.UpdatedAt.Local().Format(updatedAtFormat)}} //nolint:gosmopolitan
}

// githubRefPattern matches the URL of a GitHub repository, issue, or pull request, with at
// most one trailing slash.
var githubRefPattern = regexp.MustCompile(`^https://github\.com/([^/?#]+)/([^/?#]+)(?:/(?:issues|pull)/([0-9]+))?/?$`)

// shortGitHubRef returns <owner>/<repo> for the URL of a GitHub repository and
// <owner>/<repo>#<number> for the URL of an issue or a pull request, and false for any other
// URL. Only the form of the URL is checked, so it doesn't matter which column the URL is in.
func shortGitHubRef(url string) (string, bool) {
	m := githubRefPattern.FindStringSubmatch(url)
	if m == nil {
		return "", false
	}
	ref := m[1] + "/" + m[2]
	if m[3] != "" {
		ref += "#" + m[3]
	}
	return ref, true
}

func claudeSessions(t *task.Task) []item {
	items := make([]item, 0, len(t.ClaudeSessions))
	for _, s := range t.ClaudeSessions {
		if s == nil {
			continue
		}
		items = append(items, item{Text: s.ID + ": " + s.Description})
	}
	return items
}

// NewHandler returns the handler serving the list of the tasks.
// It reads the tasks of tasksDir on every request, so changes to task.yaml show up on
// reload. initialFailures are the failures of the read at startup, which NewHandler warns
// about so that the first request doesn't repeat the warnings. h focuses the herdr pane of
// a task's session when its row is clicked, m opens a task's documents in mo when its mo
// button is clicked, and l starts Claude Code in a new tab to create a task.
func NewHandler(logger *slog.Logger, tasksDir string, initialFailures []*task.Failure, h Herdr, m Mo, l *Launcher) http.Handler {
	reporter := newFailureReporter(logger, initialFailures)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		result, err := reporter.read(tasksDir)
		if err != nil {
			// The tasks directory was removed or became unreadable after startup.
			// The server keeps running and recovers once the directory is back.
			logger.Error("read the tasks", "error", err)
			http.Error(w, "failed to read the tasks directory", http.StatusInternalServerError)
			return
		}
		serveIndex(w, logger, result.Tasks)
	})
	mux.HandleFunc("POST /focus", func(w http.ResponseWriter, r *http.Request) {
		serveFocus(w, r, logger, h, tasksDir)
	})
	mux.HandleFunc("POST /mo", func(w http.ResponseWriter, r *http.Request) {
		serveMo(w, r, logger, m, tasksDir)
	})
	mux.HandleFunc("POST /launch", func(w http.ResponseWriter, r *http.Request) {
		serveLaunch(w, r, logger, l)
	})
	return checkHost(mux)
}

func serveIndex(w http.ResponseWriter, logger *slog.Logger, tasks []*task.Task) {
	data := &pageData{
		Columns: columns,
		Rows:    make([]row, 0, len(tasks)),
	}
	for _, t := range tasks {
		cells := make([]cell, 0, len(columns))
		for _, c := range columns {
			cells = append(cells, cell{Key: c.Key, Items: c.cells(t)})
		}
		data.Rows = append(data.Rows, row{
			Dir:         t.Dir,
			Name:        t.Name,
			Description: t.Description,
			Status:      t.Status,
			Tags:        searchValues(t.Tags),
			Repos:       searchURLs(t.Repositories),
			Issues:      searchURLs(t.Issues),
			PRs:         searchURLs(t.PullRequests),
			Cells:       cells,
		})
	}
	// Render into a buffer so that a template error doesn't send a half-written page.
	buf := &bytes.Buffer{}
	if err := indexTemplate.Execute(buf, data); err != nil {
		logger.Error("render the task list", "error", err)
		http.Error(w, "failed to render the task list", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Don't let the browser show a stale list, e.g. when going back to the page.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

// checkHost rejects a request whose Host header isn't the loopback host.
// A malicious site could otherwise read the page through the browser with DNS rebinding.
// Only the host name is checked, because a browser omits the port of the scheme's
// default port (e.g. --port 80).
func checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostHeader string) bool {
	name := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		name = h
	}
	return strings.EqualFold(name, "localhost") || name == host
}
