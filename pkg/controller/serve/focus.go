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
	StartAgent(ctx context.Context, name, paneID string, wait time.Duration) error
	Prompt(ctx context.Context, name, text string) error
	ListTabs(ctx context.Context) ([]herdr.Tab, error)
	FocusTab(ctx context.Context, tabID string) error
}

// The results of a focus request.
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

// taskRequest is the body of a request from the page about a task, such as a focus request.
type taskRequest struct {
	Task string `json:"task"`
}

type focusResponse struct {
	Result  string `json:"result"`
	Message string `json:"message"`
}

// focusTask focuses the herdr pane of the task's Claude Code session. Of the sessions with a
// pane, the one written last in claude_sessions wins. dir is the task's directory name.
func focusTask(ctx context.Context, logger *slog.Logger, h Herdr, tasksDir, dir string) *focusResponse {
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
		return &focusResponse{Result: focusNoSession, Message: dir + " has no Claude Code session"}
	}
	agents, err := h.ListAgents(ctx)
	if err != nil {
		return herdrFailure(logger, err)
	}
	paneID := findPane(agents, sessions)
	if paneID == "" {
		return &focusResponse{Result: focusNotFound, Message: "No herdr pane for the sessions of " + dir}
	}
	if err := h.Focus(ctx, paneID); err != nil {
		return herdrFailure(logger, err)
	}
	return &focusResponse{Result: focusFocused, Message: "Focused the pane of " + dir}
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

// serveFocus handles POST /focus.
func serveFocus(w http.ResponseWriter, r *http.Request, logger *slog.Logger, h Herdr, tasksDir string) {
	dir, ok := readTaskRequest(w, r, logger)
	if !ok {
		return
	}
	writeJSON(w, logger, http.StatusOK, focusTask(r.Context(), logger, h, tasksDir, dir))
}

// readTaskRequest reads the task directory name of a request from the page, such as a focus
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
