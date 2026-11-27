package serve

import (
	"context"
	"encoding/json"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/suzuki-shunsuke/saki/pkg/herdr"
	"github.com/suzuki-shunsuke/saki/pkg/task"
)

// Herdr finds and focuses the panes of coding agents and the tabs, and starts Claude Code in
// a new tab.
// *herdr.Client implements it.
type Herdr interface {
	ListAgents(ctx context.Context) ([]herdr.Agent, error)
	Focus(ctx context.Context, paneID string) error
	PaneWorkspace(ctx context.Context, paneID string) (string, error)
	CreateTab(ctx context.Context, workspaceID, cwd, label string) (string, error)
	StartAgent(ctx context.Context, name, paneID string, wait time.Duration, args ...string) error
	PaneAgent(ctx context.Context, paneID string) (*herdr.Agent, error)
	Prompt(ctx context.Context, name, text string) error
	ListTabs(ctx context.Context) ([]herdr.Tab, error)
	FocusTab(ctx context.Context, tabID string) error
}

// The results of an open request, besides those of resuming a session and starting a new one.
const (
	focusFocused     = "focused"
	focusNoSession   = "no_session"
	focusNotFound    = "not_found"
	focusUnavailable = "unavailable"
	focusError       = "error"
	focusUnknownTask = "unknown_task"
)

// herdrNotRunningMessage is shown when herdr's server isn't running.
const herdrNotRunningMessage = "herdr isn't running"

// resultBadRequest is the result of a request from the page whose body is invalid.
const resultBadRequest = "bad_request"

// maxTaskRequestSize limits the body of a request from the page, which only holds a task name.
const maxTaskRequestSize = 1 << 16

// taskRequest is the body of a request from the page about a task, such as an open request.
type taskRequest struct {
	Task string `json:"task"`
}

type focusResponse struct {
	Result  string `json:"result"`
	Message string `json:"message"`
}

// openTask opens the task's Claude Code session, when its open button is clicked. It focuses
// the herdr pane of the session: of the sessions with a pane, the one written last in
// claude_sessions wins. When no session has a pane, l resumes the session written last in a
// new tab; without l, it's reported as not found. When the task has no session, l starts a
// new one as the new button does; without l, it's reported as no session. dir is the task's
// directory name.
func openTask(ctx context.Context, logger *slog.Logger, h Herdr, l *Launcher, tasksDir, dir string) *focusResponse {
	t := findTask(tasksDir, dir)
	if t == nil {
		return &focusResponse{Result: focusUnknownTask, Message: dir + " isn't found"}
	}
	sessions := make([]string, 0, len(t.ClaudeSessions))
	for _, s := range t.ClaudeSessions {
		if s != nil && s.ID != "" {
			sessions = append(sessions, s.ID)
		}
	}
	if len(sessions) == 0 {
		if l == nil {
			return &focusResponse{Result: focusNoSession, Message: dir + " has no Claude Code session"}
		}
		return l.startSession(ctx, logger, tasksDir, t)
	}
	agents, err := h.ListAgents(ctx)
	if err != nil {
		return herdrFailure(logger, err)
	}
	paneID := findPane(agents, sessions)
	if paneID == "" {
		return resumeTask(ctx, logger, l, t, sessions[len(sessions)-1])
	}
	if err := h.Focus(ctx, paneID); err != nil {
		return herdrFailure(logger, err)
	}
	return &focusResponse{Result: focusFocused, Message: "Focused the pane of " + dir}
}

// resumeTask resumes the session of the task t with l, or reports that no pane was found
// without l.
func resumeTask(ctx context.Context, logger *slog.Logger, l *Launcher, t *task.Task, session string) *focusResponse {
	if l == nil {
		return &focusResponse{Result: focusNotFound, Message: "No herdr pane for the sessions of " + t.Dir}
	}
	// As with a launch, resuming goes on even if the browser goes away, because stopping
	// halfway would leave a tab without Claude Code.
	res := l.resume(context.WithoutCancel(ctx), logger, t, session)
	if res.Result != launchError {
		logger.Info("resume the Claude Code session of the task", "task", t.Dir, "result", res.Result, "message", res.Message)
	}
	return res
}

// findTask returns the task in the directory dir, or nil. dir is only compared with the
// names of the task directories, so a path such as "../x" just isn't found.
func findTask(tasksDir, dir string) *task.Task {
	result, err := task.Read(tasksDir)
	if err != nil {
		return nil
	}
	for _, t := range result.Tasks {
		if t.Dir == dir {
			return t
		}
	}
	return nil
}

// findPane returns the pane of the session written last that has a Claude Code pane, or
// "". When panes share a session ID, the first one herdr lists wins.
func findPane(agents []herdr.Agent, sessions []string) string {
	panes := make(map[string]string, len(agents))
	for _, a := range agents {
		if a.Kind != "claude" || a.SessionID == "" {
			continue
		}
		if _, ok := panes[a.SessionID]; !ok {
			panes[a.SessionID] = a.PaneID
		}
	}
	for _, session := range slices.Backward(sessions) {
		if paneID, ok := panes[session]; ok {
			return paneID
		}
	}
	return ""
}

func herdrFailure(logger *slog.Logger, err error) *focusResponse {
	if herdr.IsNotRunning(err) {
		return &focusResponse{Result: focusUnavailable, Message: herdrNotRunningMessage}
	}
	logger.Error("focus the herdr pane", "error", err)
	return &focusResponse{Result: focusError, Message: "Failed to focus the pane: " + err.Error()}
}

// serveOpen handles POST /open.
func serveOpen(w http.ResponseWriter, r *http.Request, logger *slog.Logger, h Herdr, l *Launcher, tasksDir string) {
	dir, ok := readTaskRequest(w, r, logger)
	if !ok {
		return
	}
	writeJSON(w, logger, http.StatusOK, openTask(r.Context(), logger, h, l, tasksDir, dir))
}

// readTaskRequest reads the task directory name of a request from the page, such as an open
// request. It rejects a request from another site with 403 and an invalid body with 400,
// and returns false then.
func readTaskRequest(w http.ResponseWriter, r *http.Request, logger *slog.Logger) (string, bool) {
	if !isLocalJSONRequest(r) {
		http.Error(w, "forbidden request", http.StatusForbidden)
		return "", false
	}
	req := &taskRequest{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTaskRequestSize)).Decode(req); err != nil || req.Task == "" {
		writeJSON(w, logger, http.StatusBadRequest, &focusResponse{Result: resultBadRequest, Message: "invalid request"})
		return "", false
	}
	return req.Task, true
}

// isLocalJSONRequest reports whether the request comes from this server's page and has a
// JSON body. Another site's page can't send JSON without a CORS preflight, which this
// server doesn't allow, and the Origin check rejects a cross-site request the browser
// labels.
func isLocalJSONRequest(r *http.Request) bool {
	return isLocalOrigin(r.Header.Get("Origin")) && isJSON(r.Header.Get("Content-Type"))
}

// isLocalOrigin reports whether the Origin header is absent or names the loopback host.
func isLocalOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLoopbackHost(u.Host)
}

func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Error("write the response", "error", err)
	}
}
