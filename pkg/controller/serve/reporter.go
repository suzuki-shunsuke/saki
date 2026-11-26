package serve

import (
	"log/slog"
	"sync"

	"github.com/suzuki-shunsuke/saki/pkg/task"
)

// failureReporter reads the tasks and logs a warning for each task whose task.yaml
// couldn't be read. The tasks are read on every request, so a task that stays unreadable
// for the same reason is warned only once: when it becomes unreadable or its error changes.
type failureReporter struct {
	logger *slog.Logger
	mu     sync.Mutex
	// reported maps the directory of each task warned about to its error message.
	reported map[string]string
}

// newFailureReporter creates a failureReporter and warns about the failures of the
// read at startup.
func newFailureReporter(logger *slog.Logger, initialFailures []*task.Failure) *failureReporter {
	r := &failureReporter{
		logger:   logger,
		reported: map[string]string{},
	}
	r.report(initialFailures)
	return r
}

// read reads the tasks of dir and warns about the new failures.
// Reading and updating the reported failures happen under one lock, so concurrent requests
// can't overwrite a newer result with an older one and repeat a warning.
func (r *failureReporter) read(dir string) (*task.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result, err := task.Read(dir)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}
	r.report(result.Failures)
	return result, nil
}

// report warns about the failures which weren't reported or whose error changed, and
// replaces the reported failures with them. A task which became readable is forgotten, so
// it's warned again if it becomes unreadable later.
// The caller must hold r.mu, except in newFailureReporter.
func (r *failureReporter) report(failures []*task.Failure) {
	reported := make(map[string]string, len(failures))
	for _, failure := range failures {
		msg := failure.Err.Error()
		reported[failure.Dir] = msg
		if prev, ok := r.reported[failure.Dir]; ok && prev == msg {
			continue
		}
		r.logger.Warn("skipped a task whose task.yaml couldn't be read",
			"task_dir", failure.Dir,
			"error", failure.Err)
	}
	r.reported = reported
}
