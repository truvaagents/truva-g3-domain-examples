package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestKindAssessmentHTTPCapabilities(t *testing.T) {
	if os.Getenv("MAINTENANCE_ASSESSMENT_LIVE_APPROVED") != "one-no-ai-run" {
		t.Skip("operator-only no-AI Kind check")
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		t.Fatal("requires Kind")
	}
	operator, coordinator := os.Getenv("MAINTENANCE_AGENT_API_TOKEN"), os.Getenv("MAINTENANCE_AGENT_SERVICE_TOKEN")
	if operator == "" || coordinator == "" {
		t.Fatal("configured credentials required (not logged)")
	}
	ctx, stop := context.WithTimeout(t.Context(), time.Minute)
	defer stop()
	client := &http.Client{Timeout: 10 * time.Second}
	requestID := fmt.Sprintf("v4-assessment-http-check-%d", time.Now().UnixNano())
	for _, check := range []struct {
		name, path, token, body string
		status                  int
	}{
		{"operator_history", "find", operator, `{"profile_id":"retail-order-maintenance"}`, 200},
		{"coordinator_history", "find", coordinator, `{"profile_id":"retail-order-maintenance"}`, 200},
		{"wrong_role", "find", "labelled-unauthorized-fixture", `{"profile_id":"retail-order-maintenance"}`, 401},
		{"wrong_profile", "find", operator, `{"profile_id":"different-fixture-profile"}`, 403},
		{"invalid_check", "find", operator, `{"profile_id":"retail-order-maintenance","check_id":"missing-fixture-check"}`, 400},
		{"missing_assessment", "get", operator, `{"profile_id":"retail-order-maintenance","assessment_id":"missing-fixture-assessment"}`, 404},
		{"duplicate_field", "find", operator, `{"profile_id":"retail-order-maintenance","profile_id":"retail-order-maintenance"}`, 400},
		{"policy_unconfigured", "record", operator, `{"profile_id":"retail-order-maintenance","case_id":"labelled-fixture-only","observation_id":"labelled-fixture-only","check_id":"fixture-check","expected_issue_version":0}`, 422},
	} {
		t.Run(check.name, func(t *testing.T) {
			// Owning example's in-cluster service; no arbitrary URL or provider access.
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://maintenance-assessment-tool.software-maintenance.svc.cluster.local/maintenance/assessments/"+check.path, strings.NewReader(check.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+check.token)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-TruvaG3-Request-ID", requestID)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal("assessment HTTP request failed")
			}
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(io.LimitReader(response.Body, 65537))
			if err != nil || len(body) > 65536 || !json.Valid(body) {
				t.Fatal("invalid or oversized capability response")
			}
			if response.StatusCode != check.status {
				t.Fatalf("status=%d expected=%d", response.StatusCode, check.status)
			}
			if check.status == 200 {
				var envelope struct {
					Success bool `json:"success"`
					Data    struct {
						PolicyConfigured bool              `json:"policy_configured"`
						Candidates       []json.RawMessage `json:"candidates"`
					} `json:"data"`
				}
				if json.Unmarshal(body, &envelope) != nil || !envelope.Success || envelope.Data.PolicyConfigured || len(envelope.Data.Candidates) != 0 {
					t.Fatal("unconfigured policy fabricated reusable history")
				}
			}
		})
	}
	t.Logf("request_id=%s paid_calls=0 cases_created=0 github_mutations=0", requestID)
}

func TestKindApplicationRecordInspection(t *testing.T) {
	if os.Getenv("MAINTENANCE_ASSESSMENT_LIVE_APPROVED") != "one-no-ai-run" {
		t.Skip("operator-only read-only Kind check")
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" || os.Getenv("MAINTENANCE_AGENT_API_TOKEN") == "" {
		t.Fatal("Kind and configured operator credential required (not logged)")
	}
	ctx, stop := context.WithTimeout(t.Context(), time.Minute)
	defer stop()
	client := &http.Client{Timeout: 10 * time.Second}
	requestID := fmt.Sprintf("v4-application-inspection-%d", time.Now().UnixNano())
	for _, check := range []struct {
		name, body   string
		unauthorized bool
		status       int
	}{
		{"history", `{"profile_id":"retail-order-maintenance"}`, false, 200},
		{"unauthorized", `{"profile_id":"retail-order-maintenance"}`, true, 401},
		{"different_profile", `{"profile_id":"labelled-other-profile"}`, false, 403},
		{"ambiguous_locator", `{"profile_id":"retail-order-maintenance","case_id":"fixture","assessment_id":"fixture"}`, false, 400},
		{"assessment_owner_changed", `{"profile_id":"retail-order-maintenance","assessment_id":"labelled-missing-record"}`, false, 400},
		{"r12_read_only", `{"profile_id":"retail-order-maintenance","case_id":"case_be228ecade0b0d07756372cf07d40cf4bc9666a8d6c68a800b1fb5a191512dda"}`, false, 200},
	} {
		t.Run(check.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://maintenance-agent.software-maintenance.svc.cluster.local/maintenance/records/inspect", strings.NewReader(check.body))
			if err != nil {
				t.Fatal(err)
			}
			token := os.Getenv("MAINTENANCE_AGENT_API_TOKEN")
			if check.unauthorized {
				token = "labelled-unauthorized-fixture"
			}
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-TruvaG3-Request-ID", requestID)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal("inspection request failed")
			}
			defer func() { _ = response.Body.Close() }()
			data, err := io.ReadAll(io.LimitReader(response.Body, 131073))
			if err != nil || len(data) > 131072 || !json.Valid(data) || response.StatusCode != check.status {
				t.Fatalf("status=%d expected=%d", response.StatusCode, check.status)
			}
			if check.status != 200 {
				return
			}
			var envelope struct {
				Success bool `json:"success"`
				Data    struct {
					SchemaVersion   int          `json:"schema_version"`
					Records         []caseRecord `json:"records"`
					ExecutionRefs   []string     `json:"execution_refs"`
					Suppression     bool         `json:"automatic_suppression_enabled"`
					Reconsideration bool         `json:"automatic_reconsideration_enabled"`
				} `json:"data"`
			}
			if json.Unmarshal(data, &envelope) != nil || !envelope.Success || envelope.Data.SchemaVersion != 1 || envelope.Data.Suppression || envelope.Data.Reconsideration {
				t.Fatal("inspection fabricated enabled automation")
			}
			if check.name == "r12_read_only" && (len(envelope.Data.Records) != 1 || envelope.Data.Records[0].Status != "failed" || envelope.Data.Records[0].LastErrorCode != "RESULT_VALIDATION_FAILED") {
				t.Fatal("stored application outcome lost")
			}
		})
	}
	t.Logf("request_id=%s paid_calls=0 cases_created=0 github_mutations=0", requestID)
}
