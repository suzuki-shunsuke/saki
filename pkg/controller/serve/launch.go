package serve

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/suzuki-shunsuke/saki/pkg/herdr"
	"github.com/suzuki-shunsuke/saki/pkg/task"
)

// The results of a launch request.
const (
	launchStarted      = "started"
	launchOutsideHerdr = "outside_herdr"
	launchUnavailable  = "unavailable"
	launchBlocked      = "blocked"
	launchError        = "error"
	launchEmpty        = "empty"
	launchTooLong      = "too_long"
	launchInvalidURL   = "invalid_url"
	launchBadRequest   = "bad_request"

	// launchFocusedExisting means a tab was already reviewing the pull request, and it was
	// focused instead of starting another review.
	launchFocusedExisting = "focused_existing"
)

// The kinds of a launch request: create a task with dev-flow, or review a pull request
// with pr-review. The browser decides the kind, so that what happens is what its button
// said even if its check of the text and the server's differed.
const (
	launchKindTask   = "task"
	launchKindReview = "review"
)

const (
	// maxLaunchTextLength is the longest task description in Unicode characters.
	maxLaunchTextLength = 10000
	// maxLaunchRequestSize holds the longest description of 4-byte characters escaped in
	// JSON, so that a long description is reported as too long rather than a bad request.
	maxLaunchRequestSize = 256 << 10
	// defaultLaunchTimeout is how long to wait for the shell and Claude Code to be ready.
	defaultLaunchTimeout = 60 * time.Second
	// defaultLaunchRetryInterval is how long to wait before trying again while the new
	// pane's shell isn't at its prompt yet.
	defaultLaunchRetryInterval = 500 * time.Millisecond
	// launchTabLabel is the label of the new tab.
	launchTabLabel = "new task"
	// dedicatedPane tells Claude Code that the new pane is the task's own, so that the flow,
	// which otherwise opens a pane for each task, doesn't open another one.
	dedicatedPane = "This pane is dedicated to this task. Don't open another pane."
	// launchPromptPrefix is put before the description, so that Claude Code creates a task
	// with the dev-flow flow embedded in saki. No skill needs to be installed.
	launchPromptPrefix = `Run "saki skill show dev-flow" and follow the instructions. ` + dedicatedPane + " Create a task:\n"
	// reviewPromptPrefix is put before the URL of a pull request, so that Claude Code
	// reviews it with the pr-review flow embedded in saki.
	reviewPromptPrefix = `Run "saki skill show pr-review" and follow the instructions. ` + dedicatedPane + " Review "
	// reviewTabPrefix starts the label of a review's tab, followed by <owner>/<repo>#<number>.
	reviewTabPrefix = "review "
	// sessionTabPrefix starts the label of the tab of a task's new session, followed by the
	// task's directory name.
	sessionTabPrefix = "task "
)

// Launcher starts Claude Code in a new herdr tab to create a task from a description, or
// to review a pull request.
type Launcher struct {
	Herdr Herdr
	// PaneID is the herdr pane saki serve runs in (HERDR_PANE_ID). The new tab is created
	// in the workspace the pane belongs to at that time. Empty outside herdr.
	PaneID string
	// Dir is the current directory of saki serve, where Claude Code runs.
	Dir string
	// Timeout and RetryInterval default to 60 seconds and 500 milliseconds.
	Timeout       time.Duration
	RetryInterval time.Duration
	// Now returns the current time, for the agent's name. It defaults to time.Now.
	Now func() time.Time

	// mu makes looking for a review's tab and creating it one step, so that two requests
	// for the same pull request at once make one tab.
	mu sync.Mutex
}

type launchRequest struct {
	Text string `json:"text"`
	Kind string `json:"kind"`
}

// launchJob is what differs between creating a task and reviewing a pull request.
type launchJob struct {
	tabLabel string
	// reuseTab is true when a tab already labeled tabLabel is focused instead of creating
	// another one.
	reuseTab    bool
	agentPrefix string
	prompt      string
	started     string
}

// pullRequestURLPattern matches the URL of a GitHub pull request, optionally followed by a
// path, a query, or a fragment. The owner and the repository are limited to the characters
// GitHub allows, because pr-review writes to tasks/review_<owner>_<repo>_<number>/. (?i) isn't
// used: its Unicode case folding would let non-ASCII characters such as "ſ" through.
var pullRequestURLPattern = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)/pull/([0-9]+)(?:[/?#].*)?$`)

// isSpace reports whether r is a space for the browser or for Go: JavaScript's trim and \s
// count U+FEFF but not U+0085, and unicode.IsSpace the other way round. The browser's check
// uses the same set, so that the button and the server agree.
func isSpace(r rune) bool {
	return unicode.IsSpace(r) || r == '\uFEFF'
}

// parsePullRequestURL returns the owner, the repository, and the number of text when it is
// the URL of one pull request, with only spaces around it. Any Unicode space inside rejects
// it, as the browser's check does (RE2's \s is ASCII only).
func parsePullRequestURL(text string) (string, string, string, bool) {
	s := strings.TrimFunc(text, isSpace)
	if strings.ContainsFunc(s, isSpace) {
		return "", "", "", false
	}
	m := pullRequestURLPattern.FindStringSubmatch(s)
	if m == nil || m[2] == "." || m[2] == ".." {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}

// taskJob checks a task description and returns the job to create the task with dev-flow.
func taskJob(text string) (*launchJob, *focusResponse) {
	if strings.TrimSpace(text) == "" {
		return nil, &focusResponse{Result: launchEmpty, Message: "The task description is empty"}
	}
	if utf8.RuneCountInString(text) > maxLaunchTextLength {
		return nil, &focusResponse{Result: launchTooLong, Message: "The task description is too long"}
	}
	return &launchJob{
		tabLabel:    launchTabLabel,
		agentPrefix: "task",
		prompt:      launchPromptPrefix + text,
		started:     "Started Claude Code in a new tab",
	}, nil
}

// reviewJob checks a pull request URL and returns the job to review it with pr-review. The
// URL is built from its parts, so whatever followed the number isn't sent.
func reviewJob(text string) (*launchJob, *focusResponse) {
	owner, repo, number, ok := parsePullRequestURL(text)
	if !ok {
		return nil, &focusResponse{Result: launchInvalidURL, Message: "The pull request URL is invalid"}
	}
	return &launchJob{
		tabLabel:    reviewTabPrefix + owner + "/" + repo + "#" + number,
		reuseTab:    true,
		agentPrefix: "review",
		prompt:      reviewPromptPrefix + "https://github.com/" + owner + "/" + repo + "/pull/" + number,
		started:     "Started Claude Code to review the PR in a new tab",
	}, nil
}

// sessionJob returns the job to start a new Claude Code session for the task t of tasksDir,
// so that the task is worked on in its own session rather than in the session it was
// created in. The session adds its own ID to claude_sessions, so that clicking the task's
// row focuses it afterwards. A review of pr-review is resumed with pr-review (see
// reviewSessionJob).
func sessionJob(tasksDir string, t *task.Task) *launchJob {
	taskDir := filepath.ToSlash(filepath.Join(tasksDir, t.Dir))
	if t.Type == task.TypePRReview {
		return reviewSessionJob(taskDir, t)
	}
	return &launchJob{
		tabLabel:    sessionTabPrefix + t.Dir,
		agentPrefix: "session",
		prompt: `Run "saki skill show dev-flow" and follow the instructions. ` + dedicatedPane +
			" Work on the task in " + taskDir + " in this new session." +
			appendSessionPrompt(taskDir),
		started: startedSessionMessage(t.Dir),
	}
}

// reviewSessionJob returns the job to resume the review t in taskDir with pr-review in a
// new session. With the URL of a pull request in pull_requests, the first one, its tab has
// the label of a tab started from the form to review the pull request, and a tab already
// reviewing it is focused instead, so that the form and the start button don't start two
// sessions for one review. The URL is built from its parts, as the form does.
func reviewSessionJob(taskDir string, t *task.Task) *launchJob {
	job := &launchJob{
		tabLabel:    sessionTabPrefix + t.Dir,
		agentPrefix: "session",
		started:     startedSessionMessage(t.Dir),
	}
	resume := " Resume the review in " + taskDir + " in this new session."
	for _, u := range t.PullRequests {
		owner, repo, number, ok := parsePullRequestURL(u)
		if !ok {
			continue
		}
		job.tabLabel = reviewTabPrefix + owner + "/" + repo + "#" + number
		job.reuseTab = true
		resume = " Resume the review of https://github.com/" + owner + "/" + repo + "/pull/" + number + " in " + taskDir + " in this new session."
		break
	}
	job.prompt = `Run "saki skill show pr-review" and follow the instructions. ` + dedicatedPane + resume + appendSessionPrompt(taskDir)
	return job
}

// appendSessionPrompt asks the new session to add its own ID to the task's claude_sessions.
func appendSessionPrompt(taskDir string) string {
	return " Append this session's ID (the environment variable CLAUDE_CODE_SESSION_ID) to the end of claude_sessions in " + taskDir + "/task.yaml."
}

func startedSessionMessage(dir string) string {
	return "Started a new Claude Code session for " + dir + " in a new tab"
}

// newLaunchJob returns the job of the request's kind, or a response when its text is
// rejected. known is false for an unknown kind.
func newLaunchJob(req *launchRequest) (*launchJob, *focusResponse, bool) {
	switch req.Kind {
	case "", launchKindTask:
		job, res := taskJob(req.Text)
		return job, res, true
	case launchKindReview:
		job, res := reviewJob(req.Text)
		return job, res, true
	default:
		return nil, nil, false
	}
}

// launch creates a tab, starts Claude Code there, and submits the job's prompt. The tab is
// left open when it fails, so that what happened can be seen.
func (l *Launcher) launch(ctx context.Context, logger *slog.Logger, job *launchJob) *focusResponse {
	if l.PaneID == "" {
		return &focusResponse{Result: launchOutsideHerdr, Message: "saki serve isn't running in a herdr pane"}
	}
	paneID, res := l.openTab(ctx, logger, job)
	if res != nil {
		return res
	}
	name := l.agentName(job.agentPrefix)
	if err := l.start(ctx, name, paneID); err != nil {
		return l.failure(logger, err)
	}
	if err := l.Herdr.Prompt(ctx, name, job.prompt); err != nil {
		return l.failure(logger, err)
	}
	return &focusResponse{Result: launchStarted, Message: job.started}
}

// openTab creates the job's tab and returns its pane. When the job reuses a tab and one is
// already labeled the same, it focuses that tab instead and returns the response.
func (l *Launcher) openTab(ctx context.Context, logger *slog.Logger, job *launchJob) (string, *focusResponse) {
	if job.reuseTab {
		// Held until the tab is created, so that a request for the same pull request
		// waiting here finds it. Starting Claude Code takes longer and is left outside.
		l.mu.Lock()
		defer l.mu.Unlock()
		tabs, err := l.Herdr.ListTabs(ctx)
		if err != nil {
			return "", tabFailure(logger, "Failed to list the herdr tabs: ", err)
		}
		if i := slices.IndexFunc(tabs, func(t herdr.Tab) bool { return sameReviewTab(t.Label, job.tabLabel) }); i >= 0 {
			if err := l.Herdr.FocusTab(ctx, tabs[i].TabID); err != nil {
				return "", tabFailure(logger, "Failed to focus the tab: ", err)
			}
			return "", &focusResponse{
				Result:  launchFocusedExisting,
				Message: "A tab is already reviewing " + strings.TrimPrefix(tabs[i].Label, reviewTabPrefix) + ": focused it",
			}
		}
	}
	workspaceID, err := l.Herdr.PaneWorkspace(ctx, l.PaneID)
	if err != nil {
		return "", l.failure(logger, err)
	}
	paneID, err := l.Herdr.CreateTab(ctx, workspaceID, l.Dir, job.tabLabel)
	if err != nil {
		return "", l.failure(logger, err)
	}
	return paneID, nil
}

// sameReviewTab reports whether label is the label of want's review: "review " and the
// number are the same, and the owner and the repository are the same ignoring ASCII case,
// as GitHub does. strings.EqualFold isn't used: its Unicode case folding would match the
// Kelvin sign with "k".
func sameReviewTab(label, want string) bool {
	if len(label) != len(want) || !strings.HasPrefix(label, reviewTabPrefix) {
		return false
	}
	i := strings.LastIndexByte(want, '#')
	if i < 0 || label[i:] != want[i:] {
		return false
	}
	for j := len(reviewTabPrefix); j < i; j++ {
		if lowerASCII(label[j]) != lowerASCII(want[j]) {
			return false
		}
	}
	return true
}

func lowerASCII(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + 'a' - 'A'
	}
	return b
}

// tabFailure is the response when listing or focusing the tabs failed. No tab is created
// then, because starting a duplicate review is worse than doing nothing.
func tabFailure(logger *slog.Logger, prefix string, err error) *focusResponse {
	if herdr.IsNotRunning(err) {
		return &focusResponse{Result: launchUnavailable, Message: herdrNotRunningMessage}
	}
	logger.Error("look for the review's herdr tab", "error", err)
	return &focusResponse{Result: launchError, Message: prefix + err.Error()}
}

// start starts Claude Code, trying again only while the new pane's shell isn't at its
// prompt yet. Any other error is final: Claude Code may already be starting, and trying
// again would type "claude" into it.
func (l *Launcher) start(ctx context.Context, name, paneID string) error {
	deadline := l.now().Add(l.timeout())
	for {
		// herdr's errors are returned as they are, because their message is shown to the
		// user and their code decides the result.
		err := l.Herdr.StartAgent(ctx, name, paneID, deadline.Sub(l.now()))
		if !herdr.HasCode(err, herdr.CodePaneBusy) {
			return err //nolint:wrapcheck
		}
		if !l.now().Add(l.retryInterval()).Before(deadline) {
			// The last error from herdr tells why it timed out.
			return err //nolint:wrapcheck
		}
		select {
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck
		case <-time.After(l.retryInterval()):
		}
	}
}

func (l *Launcher) failure(logger *slog.Logger, err error) *focusResponse {
	switch {
	case herdr.IsNotRunning(err):
		return &focusResponse{Result: launchUnavailable, Message: herdrNotRunningMessage}
	case herdr.HasCode(err, herdr.CodeAgentNotReady), herdr.HasCode(err, herdr.CodeAgentBlocked):
		return &focusResponse{Result: launchBlocked, Message: "Claude Code is waiting for input in the new tab"}
	}
	logger.Error("start Claude Code in a new herdr tab", "error", err)
	return &focusResponse{Result: launchError, Message: "Failed to start Claude Code: " + err.Error()}
}

// agentName returns a name herdr accepts ([a-z][a-z0-9_-]{0,31}), unique enough even for
// two launches in the same second. rand.Text is base32 (A-Z, 2-7), lowercased for herdr.
// "session-" makes the longest name, 28 characters.
func (l *Launcher) agentName(prefix string) string {
	return prefix + "-" + l.now().Format("20060102-150405") + "-" + strings.ToLower(rand.Text()[:4])
}

func (l *Launcher) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Launcher) timeout() time.Duration {
	if l.Timeout > 0 {
		return l.Timeout
	}
	return defaultLaunchTimeout
}

func (l *Launcher) retryInterval() time.Duration {
	if l.RetryInterval > 0 {
		return l.RetryInterval
	}
	return defaultLaunchRetryInterval
}

// serveLaunch handles POST /launch.
func serveLaunch(w http.ResponseWriter, r *http.Request, logger *slog.Logger, l *Launcher) {
	if !isLocalJSONRequest(r) {
		http.Error(w, "forbidden request", http.StatusForbidden)
		return
	}
	req := &launchRequest{}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLaunchRequestSize)).Decode(req)
	var job *launchJob
	var res *focusResponse
	known := false
	if err == nil {
		job, res, known = newLaunchJob(req)
	}
	if !known {
		writeJSON(w, logger, http.StatusBadRequest, &focusResponse{Result: launchBadRequest, Message: "invalid request"})
		return
	}
	if res == nil {
		// Starting goes on even if the browser goes away, because stopping halfway would
		// leave a tab without the prompt. The result is logged too, so that it isn't lost
		// when the browser has gone (an error is already logged by failure).
		res = l.launch(context.WithoutCancel(r.Context()), logger, job)
	}
	switch res.Result {
	case launchError:
	case launchFocusedExisting:
		logger.Info("focus the herdr tab already reviewing the pull request", "result", res.Result, "message", res.Message)
	default:
		logger.Info("start Claude Code in a new herdr tab", "result", res.Result, "message", res.Message)
	}
	writeJSON(w, logger, http.StatusOK, res)
}

// serveStart handles POST /start, which starts a new Claude Code session for a task in a
// new herdr tab.
func serveStart(w http.ResponseWriter, r *http.Request, logger *slog.Logger, l *Launcher, tasksDir string) {
	dir, ok := readTaskRequest(w, r, logger)
	if !ok {
		return
	}
	var res *focusResponse
	if t := findTask(tasksDir, dir); t == nil {
		res = &focusResponse{Result: focusUnknownTask, Message: dir + " isn't found"}
	} else {
		// As with serveLaunch, starting goes on even if the browser goes away.
		res = l.launch(context.WithoutCancel(r.Context()), logger, sessionJob(tasksDir, t))
	}
	if res.Result != launchError {
		logger.Info("start a new Claude Code session for the task in a new herdr tab", "task", dir, "result", res.Result, "message", res.Message)
	}
	writeJSON(w, logger, http.StatusOK, res)
}
