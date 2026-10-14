// Package toolruntime joins the HTTP drain before an example component releases
// its owned clients and telemetry. Agents pass BaseAgent.Stop; tools pass
// BaseTool.Shutdown. Registration and serving remain framework-owned.
package toolruntime

import (
	"context"
	"errors"
	"time"
)

func Run(ctx context.Context, run func(context.Context) error, shutdown func(context.Context) error, timeout time.Duration) error {
	if timeout <= 0 {
		return errors.New("component shutdown timeout must be positive")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	drained := make(chan error, 1)
	go func() {
		<-runCtx.Done()
		// The process signal has canceled ctx; the drain needs its own bounded
		// deadline while retaining any startup/lifecycle context values.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		drained <- shutdown(shutdownCtx)
	}()
	err := run(runCtx)
	// Also release the shutdown waiter after a startup/listener failure.
	stop()
	shutdownErr := <-drained
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	return errors.Join(err, shutdownErr)
}
