package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// These focused tests protect verification evidence integrity, not application
// business behavior. In particular, truncation cannot turn into publishable proof.
func TestOversizedJobLogPrefix(t *testing.T) {
	const limit = 524288
	for _, failed := range []bool{false, true} {
		for _, size := range []int{limit - 1, limit, limit + 1, limit + 2, 2 * limit} {
			t.Run(fmt.Sprintf("failed=%v/bytes=%d", failed, size), func(t *testing.T) {
				logs := bytes.Repeat([]byte{0xff, '\n', '*', 'x'}, (size+3)/4)[:size]
				exitCode, status := 0, "succeeded"
				if failed {
					exitCode, status = 1, "failed"
				}
				runner := &kubernetesRunner{baseURL: "http://example.invalid", namespace: "fixture", httpClient: &http.Client{Transport: jobObservationTransport(func(r *http.Request) (*http.Response, error) {
					var data []byte
					switch {
					case strings.HasSuffix(r.URL.Path, "/log"):
						data = logs
					case strings.Contains(r.URL.Path, "/jobs/"):
						data = []byte(fmt.Sprintf(`{"status":{%q:1,"completionTime":"2026-09-29T12:00:00Z"}}`, status))
					default:
						data = []byte(fmt.Sprintf(`{"items":[{"metadata":{"name":"fixture-pod"},"status":{"containerStatuses":[{"name":"runner","state":{"terminated":{"exitCode":%d,"finishedAt":"2026-09-29T12:00:00Z"}}}]}}]}`, exitCode))
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
				})}}
				got, err := runner.inspect(t.Context(), "fixture-job", limit)
				if err != nil {
					t.Fatal(err)
				}
				if got.Status != status || got.ExitCode == nil || int(*got.ExitCode) != exitCode || got.CompletedAt.IsZero() || got.LogTruncated != (size > limit) || !bytes.Equal(got.Logs, logs[:min(size, limit)]) {
					t.Fatalf("terminal evidence changed: status=%s truncated=%v len=%d", got.Status, got.LogTruncated, len(got.Logs))
				}
				again, err := runner.inspect(t.Context(), "fixture-job", limit)
				if err != nil || !reflect.DeepEqual(got, again) {
					t.Fatalf("repeat observation changed: %v", err)
				}
			})
		}
	}
}

type logReadFailure struct{ err error }

func (r logReadFailure) Read([]byte) (int, error) { return 0, r.err }

func TestKubernetesLogReadPolicies(t *testing.T) {
	readErr := errors.New("fixture interrupted read")
	for _, tc := range []struct {
		name   string
		status int
		body   io.Reader
	}{
		{"read_error", 200, io.MultiReader(strings.NewReader("part"), logReadFailure{readErr})},
		{"oversized_error", 403, strings.NewReader(strings.Repeat("x", 32))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &kubernetesRunner{baseURL: "http://example.invalid", httpClient: &http.Client{Transport: jobObservationTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(tc.body), Header: make(http.Header)}, nil
			})}}
			_, data, truncated, err := runner.requestLogPrefix(t.Context(), "/log", 16)
			if err == nil || data != nil || truncated {
				t.Fatal("failed read was promoted to usable evidence")
			}
			if tc.name == "read_error" && !errors.Is(err, readErr) {
				t.Fatal("read error cause lost")
			}
		})
	}
	runner := &kubernetesRunner{baseURL: "http://example.invalid", httpClient: &http.Client{Transport: jobObservationTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 32))), Header: make(http.Header)}, nil
	})}}
	if _, _, err := runner.request(context.Background(), http.MethodGet, "/jobs", nil, 16); err == nil {
		t.Fatal("strict JSON reader accepted oversized response")
	}
	for _, limit := range []int64{-1, 1<<63 - 1} {
		if _, _, _, err := runner.requestLogPrefix(t.Context(), "/log", limit); err == nil {
			t.Fatal("invalid/overflowing byte limit accepted")
		}
	}
}
