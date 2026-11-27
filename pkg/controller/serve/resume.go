package serve

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/suzuki-shunsuke/saki/pkg/claudesession"
	"github.com/suzuki-shunsuke/saki/pkg/herdr"
	"github.com/suzuki-shunsuke/saki/pkg/task"
)

// The results of resuming a session whose pane wasn't found. The others are those of a
// launch: outside_herdr, unavailable, blocked, error, and focused_existing.
const (
	resumeResumed         = "resumed"
	resumeResumedFallback = "resumed_fallback"
	resumeFocusedResumed  = "focused_resumed"
	resumeInvalidSession  = "invalid_session"
	resumeNoRecord        = "no_record"
	resumeNoDir           = "no_dir"
	resumeAlreadyStarting = "already_starting"
)

// resume starts Claude Code resuming the session in a new tab, because no pane of the task
// t's sessions was found. session is the session written last in claude_sessions.
func (l *Launcher) resume(ctx context.Context, logger *slog.Logger, t *task.Task, session string) *focusResponse {
	if l.PaneID == "" {
		return &focusResponse{Result: launchOutsideHerdr, Message: outsideHerdrMessage}
	}
	if !claudesession.IsValidID(session) {
		// The ID is typed into a shell by herdr, so only a UUID is passed.
		return &focusResponse{Result: resumeInvalidSession, Message: "The session ID of " + t.Dir + " is invalid: " + session}
	}
	if !l.startResuming(session) {
		return &focusResponse{Result: resumeAlreadyStarting, Message: "The session of " + t.Dir + " is already starting"}
	}
	defer l.endResuming(session)
	return l.resumeInTab(ctx, logger, t, session)
}

// resumeInTab resumes the session, which no other request is resuming, in a new tab, or
// focuses the tab it was resumed in before.
func (l *Launcher) resumeInTab(ctx context.Context, logger *slog.Logger, t *task.Task, session string) *focusResponse {
	if res := l.focusResumedPane(ctx, logger, t, session); res != nil {
		return res
	}
	record, err := claudesession.Find(l.ConfigDir, session)
	if err != nil {
		if errors.Is(err, claudesession.ErrNotFound) {
			return &focusResponse{Result: resumeNoRecord, Message: "No record of the session " + session + " of " + t.Dir}
		}
		return l.resumeError(logger, err)
	}
	first, last, err := claudesession.Dirs(record)
	if err != nil {
		return l.resumeError(logger, err)
	}
	dir, fallback := l.resumeDir(first, last)
	if dir == "" {
		return &focusResponse{Result: resumeNoDir, Message: "The directories of the session of " + t.Dir + " no longer exist"}
	}
	label, reuse, _ := sessionTab(t)
	job := &launchJob{
		tabLabel:      label,
		reuseTab:      reuse,
		agentPrefix:   "resume",
		cwd:           dir,
		args:          []string{"--resume", session},
		failurePrefix: "Failed to resume the session: ",
	}
	paneID, res := l.openTab(ctx, logger, job)
	if res != nil {
		return res
	}
	// Remembered before starting, so that a click while Claude Code waits at a dialog, when
	// herdr hasn't recorded the session yet, focuses this tab rather than starting the
	// session again, whatever the start returns.
	l.rememberPane(session, paneID)
	if err := l.start(ctx, l.agentName(job.agentPrefix), paneID, job.args); err != nil {
		return l.failure(logger, job, err)
	}
	if fallback {
		return &focusResponse{
			Result:  resumeResumedFallback,
			Message: "Resumed the session of " + t.Dir + " in " + dir + ", because " + last + " no longer exists",
		}
	}
	return &focusResponse{Result: resumeResumed, Message: "Resumed the session of " + t.Dir + " in a new tab"}
}

func (l *Launcher) resumedPaneFailure(logger *slog.Logger, err error) *focusResponse {
	if herdr.IsNotRunning(err) {
		return &focusResponse{Result: launchUnavailable, Message: herdrNotRunningMessage}
	}
	return l.resumeError(logger, err)
}

func (l *Launcher) resumeError(logger *slog.Logger, err error) *focusResponse {
	logger.Error("resume the Claude Code session", "error", err)
	return &focusResponse{Result: launchError, Message: "Failed to resume the session: " + err.Error()}
}

// resumeDir returns the directory to resume the session in: the last working directory,
// or the first when the last no longer exists, and whether it fell back to the first. It
// returns "" when neither exists.
func (l *Launcher) resumeDir(first, last string) (string, bool) {
	if l.isDir(last) {
		return last, false
	}
	if l.isDir(first) {
		return first, true
	}
	return "", false
}

// isDir reports whether path is an absolute path of a directory.
func (l *Launcher) isDir(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	if l.IsDir != nil {
		return l.IsDir(path)
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// focusResumedPane focuses the pane of the tab created to resume the session before, if
// Claude Code still runs there and it isn't another session, which is the case when Claude
// Code exited and was started again or was cleared. When the pane is closed or isn't the
// session's any more, it forgets the pane and returns nil to resume the session. When herdr
// fails otherwise, the pane is kept and the failure is returned: the session may still be
// starting there, and resuming it again would attach two Claude Code to it.
func (l *Launcher) focusResumedPane(ctx context.Context, logger *slog.Logger, t *task.Task, session string) *focusResponse {
	paneID := l.resumedPane(session)
	if paneID == "" {
		return nil
	}
	agent, err := l.Herdr.PaneAgent(ctx, paneID)
	if herdr.HasCode(err, herdr.CodePaneNotFound) {
		l.forgetPane(session)
		return nil
	}
	if err != nil {
		return l.resumedPaneFailure(logger, err)
	}
	if agent.Kind != "claude" || (agent.SessionID != "" && agent.SessionID != session) {
		l.forgetPane(session)
		return nil
	}
	if err := l.Herdr.Focus(ctx, paneID); err != nil {
		return l.resumedPaneFailure(logger, err)
	}
	return &focusResponse{Result: resumeFocusedResumed, Message: "Focused the tab where the session of " + t.Dir + " was resumed"}
}

// startResuming marks the session being resumed, and returns false if it already is.
func (l *Launcher) startResuming(session string) bool {
	l.resumeMu.Lock()
	defer l.resumeMu.Unlock()
	if _, ok := l.resuming[session]; ok {
		return false
	}
	if l.resuming == nil {
		l.resuming = map[string]struct{}{}
	}
	l.resuming[session] = struct{}{}
	return true
}

func (l *Launcher) endResuming(session string) {
	l.resumeMu.Lock()
	defer l.resumeMu.Unlock()
	delete(l.resuming, session)
}

func (l *Launcher) resumedPane(session string) string {
	l.resumeMu.Lock()
	defer l.resumeMu.Unlock()
	return l.resumedPanes[session]
}

func (l *Launcher) rememberPane(session, paneID string) {
	l.resumeMu.Lock()
	defer l.resumeMu.Unlock()
	if l.resumedPanes == nil {
		l.resumedPanes = map[string]string{}
	}
	l.resumedPanes[session] = paneID
}

func (l *Launcher) forgetPane(session string) {
	l.resumeMu.Lock()
	defer l.resumeMu.Unlock()
	delete(l.resumedPanes, session)
}
