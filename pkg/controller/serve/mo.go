package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/suzuki-shunsuke/saki/pkg/mo"
	"github.com/suzuki-shunsuke/saki/pkg/task"
)

// Mo opens files in a group of mo, the Markdown viewer. *mo.Client implements it.
type Mo interface {
	Open(ctx context.Context, group string, files []string) error
}

// The results of a request to open a task in mo.
const (
	moOpened       = "opened"
	moNoDocument   = "no_document"
	moNotInstalled = "not_installed"
	moError        = "error"
	moUnknownTask  = "unknown_task"
)

type moResponse struct {
	Result  string `json:"result"`
	Message string `json:"message"`
	// URL is the page of the task's group in mo, set when it's opened.
	URL string `json:"url,omitempty"`
}

// openInMo opens the task's documents in mo, in the group named after the task's directory.
func openInMo(ctx context.Context, logger *slog.Logger, m Mo, tasksDir, dir string) *moResponse {
	t := findTask(tasksDir, dir)
	if t == nil {
		return &moResponse{Result: moUnknownTask, Message: dir + " isn't found"}
	}
	files, err := taskDocuments(filepath.Join(tasksDir, t.Dir), t)
	if err != nil {
		logger.Error("find the documents of the task", "task", dir, "error", err)
		return &moResponse{Result: moError, Message: "Failed to open " + dir + " in mo: " + err.Error()}
	}
	if len(files) == 0 {
		return &moResponse{Result: moNoDocument, Message: dir + " has no document"}
	}
	if err := m.Open(ctx, t.Dir, files); err != nil {
		if errors.Is(err, mo.ErrNotInstalled) {
			return &moResponse{Result: moNotInstalled, Message: "mo isn't installed. See https://github.com/k1LoW/mo"}
		}
		attrs := []any{"task", dir, "error", err}
		if moErr, ok := errors.AsType[*mo.Error](err); ok {
			attrs = append(attrs, "cause", moErr.Err)
		}
		logger.Error("open the task in mo", attrs...)
		return &moResponse{Result: moError, Message: "Failed to open " + dir + " in mo: " + err.Error()}
	}
	return &moResponse{Result: moOpened, Message: "Opened " + dir + " in mo", URL: mo.URL(t.Dir)}
}

// taskDocuments returns the absolute paths of the task's documents which exist, in the order
// they're shown: the main document, details.md, and pr-explanation.md. The main document is
// review.md for a review of pr-review, and the design document (<dir>.md, or README.md in
// old tasks) for the other tasks.
func taskDocuments(taskDir string, t *task.Task) ([]string, error) {
	main := []string{t.Dir + ".md", "README.md"}
	if t.Type == task.TypePRReview {
		main = []string{"review.md"}
	}
	candidates := [][]string{
		main,
		{"details.md"},
		{"pr-explanation.md"},
	}
	files := make([]string, 0, len(candidates))
	for _, names := range candidates {
		for _, name := range names {
			p := filepath.Join(taskDir, name)
			if !isFile(p) {
				continue
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				return nil, err //nolint:wrapcheck
			}
			files = append(files, abs)
			break
		}
	}
	return files, nil
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// serveMo handles POST /mo.
func serveMo(w http.ResponseWriter, r *http.Request, logger *slog.Logger, m Mo, tasksDir string) {
	dir, ok := readTaskRequest(w, r, logger)
	if !ok {
		return
	}
	writeJSON(w, logger, http.StatusOK, openInMo(r.Context(), logger, m, tasksDir, dir))
}
