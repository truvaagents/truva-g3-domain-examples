package serviceevidence

import (
	"errors"
	"fmt"
	"testing"
)

func TestSharedReadFailureContract(t *testing.T) {
	for _, tc := range []struct {
		err       error
		status    int
		code      string
		retryable bool
	}{
		{classifyStatus(404), 404, "EVIDENCE_NOT_FOUND", false},
		{&upstreamFailure{kind: failureInvalidResponse}, 502, "INVALID_UPSTREAM_RESPONSE", false},
		{classifyStatus(503), 503, "EVIDENCE_SOURCE_UNAVAILABLE", true},
		{classifyStatus(429), 503, "EVIDENCE_SOURCE_UNAVAILABLE", true},
		{classifyStatus(409), 503, "EVIDENCE_SOURCE_UNAVAILABLE", true},
		{classifyStatus(504), 504, "EVIDENCE_SOURCE_TIMEOUT", true},
		{errReleaseChanged, 409, "RELEASE_CHANGED", true},
	} {
		failure, ok := ClassifyReadFailure(fmt.Errorf("reader: %w", tc.err))
		if !ok || failure.Status != tc.status || failure.Code != tc.code || failure.Retryable != tc.retryable {
			t.Fatalf("%v classified as %+v, recognized=%v", tc.err, failure, ok)
		}
	}
	if _, ok := ClassifyReadFailure(errors.New("unrelated store error")); ok {
		t.Fatal("unrelated backend error was claimed by the service adapter")
	}
}
