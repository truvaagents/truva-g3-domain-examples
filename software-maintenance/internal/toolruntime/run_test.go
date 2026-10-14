package toolruntime

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestRunJoinsActiveHTTPDrain(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered, release, shutdownStarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "completed")
	})}
	t.Cleanup(func() { _ = server.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, func(context.Context) error {
			err := server.Serve(listener)
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}, func(ctx context.Context) error { close(shutdownStarted); return server.Shutdown(ctx) }, time.Second)
	}()
	responseDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			defer response.Body.Close()
			var body []byte
			body, err = io.ReadAll(response.Body)
			if err == nil && string(body) != "completed" {
				err = errors.New("response was truncated")
			}
		}
		responseDone <- err
	}()
	<-entered
	cancel()
	<-shutdownStarted
	select {
	case err := <-done:
		close(release)
		t.Fatalf("returned before active handler drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-responseDone; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunStartupFailureReleasesWaiter(t *testing.T) {
	want := errors.New("listener failure")
	shutdownCalled := false
	err := Run(t.Context(), func(context.Context) error { return want }, func(context.Context) error { shutdownCalled = true; return nil }, time.Second)
	if !errors.Is(err, want) || !shutdownCalled {
		t.Fatalf("startup error/drain lost: %v", err)
	}
}

func TestRunReportsDrainDeadline(t *testing.T) {
	err := Run(t.Context(), func(context.Context) error { return nil }, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain failure hidden: %v", err)
	}
}
