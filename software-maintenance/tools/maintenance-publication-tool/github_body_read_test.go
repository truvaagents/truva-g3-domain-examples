package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Real TLS responses distinguish a broken transfer from invalid complete data,
// and verify that the historical publication request path retains its policy.
func TestRemediationBodyReadFailures(t *testing.T) {
	for _, scenario := range []string{"interrupted", "malformed_json", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			profile, _ := remediationFixture()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				body := `{"id":`
				if scenario == "interrupted" {
					w.Header().Set("Content-Length", strconv.Itoa(len(body)+100))
				}
				if scenario == "oversized" {
					body = strings.Repeat("x", (2<<20)+1)
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			client, err := newGitHubPublicationClient(server.URL, strings.Repeat("t", 40))
			if err != nil {
				t.Fatal(err)
			}
			client.httpClient = server.Client()
			operations := &publicationOperations{github: client, now: time.Now}
			number := 7
			_, err = operations.remediation(t.Context(), profile, remediationRequest{ProfileID: profile.PublicationProfileID, PullRequestNumber: &number})
			var failure *remediationFailure
			if !errors.As(err, &failure) {
				t.Fatalf("unexpected error: %v", err)
			}
			wantStatus, wantCode, wantRetry := 502, "REMEDIATION_RESPONSE_INVALID", false
			if scenario == "interrupted" {
				wantStatus, wantCode, wantRetry = 503, "REMEDIATION_UNAVAILABLE", true
			}
			if failure.status != wantStatus || failure.code != wantCode || failure.retryable != wantRetry {
				t.Fatalf("wrong classification: %+v", failure)
			}
			_, err = client.request(t.Context(), profile, http.MethodGet, "/legacy", nil, nil, new(any))
			var oldFailure *publicationFailure
			if !errors.As(err, &oldFailure) || oldFailure.kind != "github_response" || oldFailure.retryable {
				t.Fatalf("legacy publication policy changed: %v", err)
			}
		})
	}
}
