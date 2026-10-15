package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

func TestReviewObjectiveProseAdmission(t *testing.T) {
	const limit = 8192
	token := strings.Repeat("labelled-credential-", 3)
	auth, err := newReviewAuthorizer(map[string]string{"maintenance-agent": token})
	if err != nil {
		t.Fatal(err)
	}
	agent := &maintenanceReviewAgent{
		BaseAgent: core.NewBaseAgent(reviewAgentName), auth: auth,
		profiles: &reviewProfileStore{profiles: map[string]reviewProfile{"review": {
			callerSet: map[string]struct{}{"maintenance-agent": {}},
			Limits:    reviewLimits{MaximumRequestBytes: 32 << 10, MaximumObjectiveBytes: limit},
		}}},
	}
	agent.Logger = nil
	for _, tc := range []struct {
		name, objective string
		invalidID       bool
		accepted        bool
	}{
		{"multiline", "Review the candidate.\n\tCompare the evidence.\r\nExplain the conclusion.", false, true},
		{"at_byte_limit", strings.Repeat("é", limit/2), false, true},
		{"over_byte_limit", strings.Repeat("é", limit/2+1), false, false},
		{"empty", " \n\t", false, false},
		{"control", "Review\x00candidate", false, false},
		{"unicode_control", "Review\u0085candidate", false, false},
		{"identifier_newline", "Review\nthis candidate", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := reviewRequest{ProfileID: "review", CaseID: "labelled-case", CandidateID: "cand_" + strings.Repeat("a", 64), VerificationID: "verify_" + strings.Repeat("b", 64), Objective: tc.objective}
			if tc.invalidID {
				input.CandidateID += "\n"
			}
			body, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/review", strings.NewReader(string(body)))
			request.Header.Set("Authorization", "Bearer "+token)
			writer := httptest.NewRecorder()
			agent.handleReview(writer, request)
			var response core.ToolResponse
			if err := json.Unmarshal(writer.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			// An absent orchestrator stops accepted input before any AI call.
			wantCode, wantStatus := "INVALID_REVIEW_SCOPE", http.StatusBadRequest
			if tc.accepted {
				wantCode, wantStatus = "ORCHESTRATOR_INITIALIZING", http.StatusServiceUnavailable
			}
			if writer.Code != wantStatus || response.Error == nil || response.Error.Code != wantCode {
				t.Fatalf("unexpected admission result: %d %s", writer.Code, writer.Body.String())
			}
			if !tc.accepted && !tc.invalidID && !strings.Contains(response.Error.Message, "8192 bytes") {
				t.Fatalf("objective rejection lost its configured byte limit: %s", response.Error.Message)
			}
		})
	}
}

func TestReviewSummaryProseAndReceiptBoundary(t *testing.T) {
	profile := reviewProfile{Limits: reviewLimits{MaximumFindings: 32}}
	for _, tc := range []struct {
		summary string
		valid   bool
	}{
		{"Evidence:\n\tChecks passed.\r\nCandidate ready.", true},
		{"Evidence\x00invalid", false}, {"Evidence\x1binvalid", false},
		{"Evidence\x7finvalid", false}, {"Evidence\u0085invalid", false},
	} {
		decision := reviewDecision{Status: "inconclusive", Summary: tc.summary}
		err := validateDecision(profile, &decision)
		if (err == nil) != tc.valid || decision.Summary != tc.summary {
			t.Fatalf("summary shape %q: %v", tc.summary, err)
		}
	}
	decision := reviewDecision{Status: "inconclusive", Summary: "Valid\nprose", BlockingFindings: []string{"Invalid\nfinding"}}
	if validateDecision(profile, &decision) == nil || validReviewText("Invalid\nidentifier") {
		t.Fatal("prose allowance relaxed a non-summary contract")
	}
	agent := &maintenanceReviewAgent{}
	_, _, err := agent.buildReceipt(profile, reviewRequest{}, &orchestration.OrchestratorResponse{
		RequestID: "diagnostic", Response: `{"status":"approved","summary":"Valid\nprose","blocking_findings":[]}`,
	}, &orchestration.ExecutionResult{Success: true})
	var validation *responsecontract.ValidationError
	if !errors.As(err, &validation) || validation.Code != "TRUSTED_EVIDENCE_MISSING" {
		t.Fatalf("approval accepted without receipts: %v", err)
	}
}

func TestReviewSummaryByteLimit(t *testing.T) {
	for _, tc := range []struct {
		summary string
		limit   int
		valid   bool
	}{
		{strings.Repeat("x", 4183), 0, true},
		{strings.Repeat("x", 10000), 0, true},
		{strings.Repeat("x", 10001), 0, false},
		{strings.Repeat("é", 5000), 0, true},
		{strings.Repeat("é", 5001), 0, false},
		{strings.Repeat("x", 4097), 4096, false},
		{"", 0, false}, {string([]byte{0xff}), 0, false},
	} {
		decision := reviewDecision{Status: "inconclusive", Summary: tc.summary}
		err := validateDecision(reviewProfile{Limits: reviewLimits{MaximumSummaryBytes: tc.limit}}, &decision)
		if (err == nil) != tc.valid {
			t.Fatalf("bytes=%d override=%d: %v", len(tc.summary), tc.limit, err)
		}
	}
}

func TestReviewSummarySettingAppliedAtStartup(t *testing.T) {
	const key = "MAINTENANCE_REVIEW_MAXIMUM_SUMMARY_BYTES"
	filename := os.Getenv("MAINTENANCE_REVIEW_PROFILES_FILE")
	if filename == "" {
		filename = "../../profiles/retail-order-review.json"
	}
	initial, err := loadReviewProfiles(filename)
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := initial.get("retail-order-review")
	if !ok {
		t.Fatal("profile missing")
	}
	if _, present := os.LookupEnv(key); !present && profile.Limits.summaryLimit() != 10000 {
		t.Fatal("absent setting did not default to 10000")
	}
	t.Logf("effective_summary_bytes_before_test=%d", profile.Limits.summaryLimit())
	for _, tc := range []struct {
		value string
		want  int
	}{{"10000", 10000}, {"4096", 4096}, {"65536", 65536}, {"0", 0}, {"", 0}} {
		t.Setenv(key, tc.value)
		store, err := loadReviewProfiles(filename)
		if tc.want == 0 {
			if err == nil {
				t.Fatalf("accepted invalid configuration %q", tc.value)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		profile, ok := store.get("retail-order-review")
		if !ok || profile.Limits.summaryLimit() != tc.want {
			t.Fatalf("override %q: resolved limit %d", tc.value, profile.Limits.summaryLimit())
		}
		decision := reviewDecision{Status: "inconclusive", Summary: strings.Repeat("x", profile.Limits.summaryLimit()+1)}
		if err := validateDecision(profile, &decision); err == nil {
			t.Fatalf("override %q not enforced", tc.value)
		}
	}
}
