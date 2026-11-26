// Package serve implements the serve command.
// It serves a page listing the tasks on 127.0.0.1, reading them on every request.
package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/suzuki-shunsuke/saki/pkg/herdr"
	"github.com/suzuki-shunsuke/saki/pkg/mo"
	"github.com/suzuki-shunsuke/saki/pkg/task"
)

// DefaultPort is the port the server listens on unless another one is given.
const DefaultPort = 8080

// host is the only address the server listens on.
// It's fixed to the IPv4 loopback address because the server is for the local user only.
const host = "127.0.0.1"

const (
	readHeaderTimeout = 10 * time.Second
	// shutdownTimeout is how long a stop waits for the requests being served.
	// It's short because the server is local and a stop shouldn't keep the user waiting.
	shutdownTimeout = time.Second
)

// Param is the input of Serve.
type Param struct {
	// TasksDir is the directory the tasks are read from.
	TasksDir string
	// Port is the port the server listens on.
	Port int
	// HerdrPaneID is the herdr pane saki serve runs in (HERDR_PANE_ID), empty outside herdr.
	HerdrPaneID string
	// Dir is the current directory of saki serve, where Claude Code starts for a new task.
	Dir string
}

// Serve serves the task list until ctx is canceled.
// It reads the tasks once at startup to fail fast when the tasks directory is missing.
// The cancellation of ctx (by SIGINT or SIGTERM) is a normal stop, so Serve returns nil.
func Serve(ctx context.Context, logger *slog.Logger, param *Param) error {
	result, err := task.Read(param.TasksDir)
	if err != nil {
		return err //nolint:wrapcheck
	}
	// The handler warns about the failures, before listening so that the warnings precede
	// the URL and are logged even when listen fails.
	h := herdr.New()
	handler := NewHandler(logger, param.TasksDir, result.Failures, h, mo.New(), &Launcher{
		Herdr:  h,
		PaneID: param.HerdrPaneID,
		Dir:    param.Dir,
	})

	addr := net.JoinHostPort(host, strconv.Itoa(param.Port))
	lc := &net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	// The URL is logged only after listen succeeds, so a busy port never prints a URL
	// pointing at another server. It's built from the listener's address so that it has
	// the port the OS chose for --port 0.
	logger.Info("started the server", "url", "http://"+ln.Addr().String())

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
		// ctx is already canceled, so the shutdown needs a context of its own.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			// Shutdown doesn't close a connection which hasn't sent a request yet (such as
			// one a browser opens in advance) until it's a few seconds old. A stop by a
			// signal is still a normal stop, so the remaining connections are closed.
			logger.Debug("close the connections remaining after the shutdown timeout", "error", err)
			_ = server.Close()
		}
		return nil
	}
}
