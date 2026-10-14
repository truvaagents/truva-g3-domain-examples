package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3/core"
)

func TestGitHubReadRateLimitEvidence(t *testing.T) {
	now := time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC)
	for _, scenario := range []struct {
		name    string
		status  int
		headers http.Header
		body    string
		wait    time.Duration
	}{
		{"primary", 403, http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {strconv.FormatInt(now.Add(5*time.Minute).Unix(), 10)}}, `{}`, 5 * time.Minute},
		{"secondary", 403, http.Header{"Retry-After": {"120"}}, `{}`, 2 * time.Minute},
		{"secondary_message", 403, nil, `{"message":"You have exceeded a secondary rate limit."}`, time.Minute},
		{"primary_message", 403, nil, `{"message":"API rate limit exceeded for fixture."}`, time.Minute},
		{"429_without_headers", 429, nil, `{}`, time.Minute},
		{"retry_after_date", 429, http.Header{"Retry-After": {now.Add(2 * time.Minute).Format(http.TimeFormat)}}, `{}`, 2 * time.Minute},
		{"invalid_timing", 403, http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"9223372036854775807"}, "Retry-After": {"invalid"}}, `{}`, time.Minute},
		{"duration_suffix", 429, http.Header{"Retry-After": {"1m"}}, `{}`, time.Minute},
		{"fraction", 429, http.Header{"Retry-After": {"0.001"}}, `{}`, time.Minute},
		{"negative", 429, http.Header{"Retry-After": {"-1"}}, `{}`, time.Minute},
		{"overflow", 429, http.Header{"Retry-After": {"9223372036854775807"}}, `{}`, time.Minute},
		{"invalid_with_reset", 403, http.Header{"Retry-After": {"1m"}, "X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {strconv.FormatInt(now.Add(5*time.Minute).Unix(), 10)}}, `{}`, 5 * time.Minute},
		{"denied", 403, nil, `{"message":"Resource not accessible by personal access token"}`, 0},
		{"unauthenticated", 401, http.Header{"Retry-After": {"60"}}, `{}`, 0},
		{"success", 200, http.Header{"X-Ratelimit-Remaining": {"0"}}, `{}`, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			limited := githubReadRateLimitFromResponse(scenario.status, scenario.headers, []byte(scenario.body), now)
			if scenario.wait == 0 {
				if limited != nil {
					t.Fatal("non-rate-limit response classified as rate limit")
				}
				return
			}
			if limited == nil || !limited.notBefore.Equal(now.Add(scenario.wait)) {
				t.Fatalf("wrong timing: %+v", limited)
			}
			if limited.details(now)["retry_after"] != strconv.FormatInt(int64(scenario.wait/time.Second), 10) {
				t.Fatal("retry timing lost")
			}
		})
	}
}

func TestGitHubRetryAfterZero(t *testing.T) {
	now := time.Now().UTC()
	limited := githubReadRateLimitFromResponse(429, http.Header{"Retry-After": {"0"}}, nil, now)
	if limited == nil || !limited.notBefore.Equal(now) || limited.details(now)["retry_after"] != "0" {
		t.Fatal("valid zero-second Retry-After was not preserved")
	}
}

func TestRemediationRateLimitCooldownAndRecovery(t *testing.T) {
	profile, pull := remediationFixture()
	var requests atomic.Int64
	var limited atomic.Bool
	limited.Store(true)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if limited.Load() {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Secondary rate limit"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(pull)
	}))
	defer server.Close()
	client, err := newGitHubPublicationClient(server.URL, strings.Repeat("t", 40))
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient = server.Client()
	operations := &publicationOperations{github: client, now: time.Now}
	number := 7
	input := remediationRequest{ProfileID: profile.PublicationProfileID, PullRequestNumber: &number}
	for range 2 {
		_, err := operations.remediation(t.Context(), profile, input)
		var failure *remediationFailure
		if !errors.As(err, &failure) || failure.code != "REMEDIATION_RATE_LIMITED" || failure.status != 429 || failure.category != core.CategoryRateLimit || failure.retryable || failure.details["retry_not_before"] == "" {
			t.Fatalf("wrong rate-limit response: %+v %v", failure, err)
		}
	}
	if requests.Load() != 1 {
		t.Fatal("cooldown resent the request to GitHub")
	}
	// Existing mutation/read-reconciliation callers retain their original policy.
	_, err = client.request(t.Context(), profile, http.MethodGet, "/legacy", nil, nil, new(any))
	var oldFailure *publicationFailure
	if !errors.As(err, &oldFailure) || oldFailure.kind != "github_rejected" || oldFailure.retryable || requests.Load() != 2 {
		t.Fatalf("legacy publication policy changed: %v", err)
	}
	if client.pendingReadRateLimit(time.Now().Add(2*time.Minute)) != nil {
		t.Fatal("elapsed cooldown retained")
	}
	limited.Store(false)
	state, err := operations.remediation(t.Context(), profile, input)
	if err != nil || state.State != "open" || requests.Load() != 3 {
		t.Fatalf("fresh read did not recover: %+v %v", state, err)
	}
}

func TestRemediationRateLimitHandlerDetails(t *testing.T) {
	profile, _ := remediationFixture()
	client := &githubPublicationClient{}
	client.rememberReadRateLimit(&githubReadRateLimit{upstreamStatus: 403, notBefore: time.Now().Add(time.Minute)})
	auth, err := newPublicationAuthorizer("maintenance-agent", strings.Repeat("s", 40))
	if err != nil {
		t.Fatal(err)
	}
	tool := &maintenancePublicationTool{BaseTool: core.NewTool(publicationToolName), auth: auth, profiles: &publicationProfileStore{profiles: map[string]publicationProfile{profile.PublicationProfileID: profile}}, operations: &publicationOperations{github: client}}
	r := httptest.NewRequest(http.MethodPost, "/api/capabilities/get_remediation_state", strings.NewReader(`{"profile_id":"publication","pull_request_number":7}`))
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 40))
	w := httptest.NewRecorder()
	tool.handleGetRemediationState(w, r)
	var response core.ToolResponse
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || w.Code != 429 || response.Error == nil || response.Error.Code != "REMEDIATION_RATE_LIMITED" || response.Error.Details["retry_after"] != w.Header().Get("Retry-After") || response.Error.Details["retry_not_before"] == "" {
		t.Fatalf("timing omitted: %d %s", w.Code, w.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.requestWithETag(ctx, profile, http.MethodGet, "/unused", nil, nil, nil, new(string)); !errors.Is(err, context.Canceled) {
		t.Fatal("cooldown masked caller cancellation")
	}
}
