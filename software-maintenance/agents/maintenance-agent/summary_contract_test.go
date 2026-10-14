package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

func TestCoordinatorSummaryProse(t *testing.T) {
	agent := &maintenanceAgent{profile: coordinatorProfile{Limits: coordinatorLimits{MaximumFindings: 32}}}
	for _, tc := range []struct {
		summary string
		valid   bool
	}{
		{"Evidence:\n\tChecks passed.\r\nCandidate ready.", true},
		{"Evidence\x00invalid", false}, {"Evidence\x1binvalid", false},
		{"Evidence\x7finvalid", false}, {"Evidence\u0085invalid", false},
	} {
		decision := investigationDecision{Status: "inconclusive", Summary: tc.summary}
		err := agent.validateInvestigationDecision(&decision)
		if (err == nil) != tc.valid || decision.Summary != tc.summary {
			t.Fatalf("summary shape %q: %v", tc.summary, err)
		}
	}
	decision := investigationDecision{Status: "inconclusive", Summary: "Valid\nprose", Findings: []string{"Invalid\nfinding"}}
	if agent.validateInvestigationDecision(&decision) == nil || validCoordinatorText("Invalid\nidentifier") {
		t.Fatal("prose allowance relaxed a non-summary contract")
	}
}

func TestCoordinatorInvalidWorkersFailBeforeStartup(t *testing.T) {
	t.Setenv("MAINTENANCE_INVESTIGATION_WORKERS", "0")
	if err := runCoordinator(); err == nil || !strings.Contains(err.Error(), "MAINTENANCE_INVESTIGATION_WORKERS") {
		t.Fatalf("invalid worker setting did not fail before external startup: %v", err)
	}
}

func TestCoordinatorSummaryConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value   string
		present bool
		want    int
	}{
		{"", false, 10000}, {"10000", true, 10000}, {"4096", true, 4096},
		{"65536", true, 65536}, {"", true, 0}, {"0", true, 0},
		{"-1", true, 0}, {"1.5", true, 0}, {"65537", true, 0}, {"many", true, 0},
	} {
		got, err := coordinatorSummaryLimit(func(string) (string, bool) { return tc.value, tc.present })
		if got != tc.want || (err != nil) != (tc.want == 0) {
			t.Fatalf("%+v: got %d, %v", tc, got, err)
		}
	}
	// Explicit defaults share an identity; changed effective policy cannot reuse it.
	t.Setenv("MAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES", "10000")
	a, err := loadCoordinatorProfile("../../profiles/retail-order-maintenance.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES", "4096")
	b, err := loadCoordinatorProfile("../../profiles/retail-order-maintenance.json")
	if err != nil {
		t.Fatal(err)
	}
	if a.ProfileSHA256 == b.ProfileSHA256 || a.Limits.SummaryLimit() != 10000 || b.Limits.SummaryLimit() != 4096 {
		t.Fatal("effective result policy not applied to identity")
	}
}

func TestCoordinatorSummaryBytesAndPublicationEvidence(t *testing.T) {
	agent := &maintenanceAgent{profile: coordinatorProfile{PublicationProfileID: "publication", ExpectedPublicationSHA256: "profile", Limits: coordinatorLimits{MaximumFindings: 32}}}
	input := investigationInput{CaseID: "case"}
	pub := publicationStepResponse{PublicationID: "publication-id", ProfileID: "publication", ProfileSHA256: "profile", CaseID: "case", Status: "succeeded", Draft: true, PullRequestURL: "https://github.com/example/service/pull/1"}
	body, err := json.Marshal(map[string]any{"success": true, "data": pub})
	if err != nil {
		t.Fatal(err)
	}
	execution := &orchestration.ExecutionResult{Success: true, Steps: []orchestration.StepResult{{Success: true, AgentName: "maintenance-publication-tool", Capability: "publish_draft_pr", Response: string(body)}}}
	ctx := orchestration.WithRequestID(context.Background(), "execution-id")
	for _, length := range []int{4268, 10000, 10001} {
		raw, marshalErr := json.Marshal(investigationDecision{Status: "publication_requested", Summary: strings.Repeat("x", length), Findings: []string{}})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		response := &orchestration.OrchestratorResponse{RequestID: "execution-id", Response: string(raw)}
		result, validationErr := agent.buildInvestigationResult(ctx, input, response, execution)
		if length <= 10000 {
			if validationErr != nil || result.Status != "draft_pr_opened" {
				t.Fatalf("length %d: %+v %v", length, result, validationErr)
			}
			continue
		}
		var field *responsecontract.FieldError
		if !errors.As(validationErr, &field) || field.Field != "summary" || field.Actual != length || field.Limit != 10000 {
			t.Fatalf("wrong length diagnostic: %v", validationErr)
		}
		evidence := agent.resultValidationEvidence(ctx, input, execution, validationErr)
		if evidence.Publication == nil || evidence.Publication.PublicationID != pub.PublicationID || evidence.ExecutionID != "execution-id" {
			t.Fatalf("effects lost: %+v", evidence)
		}
		server := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: server.Addr()})
		t.Cleanup(func() { _ = client.Close() })
		keyspace, keyErr := core.NewRedisKeyspace("summary-test")
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		store, storeErr := newCaseStore(client, keyspace)
		if storeErr != nil {
			t.Fatal(storeErr)
		}
		record, marshalErr := json.Marshal(caseRecord{CaseID: "case", CurrentTaskID: "task", Status: "running"})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := client.Set(ctx, store.caseKey("case"), record, 0).Err(); err != nil {
			t.Fatal(err)
		}
		if err := store.markResultValidationFailed(ctx, "case", "task", evidence, time.Now()); err != nil {
			t.Fatal(err)
		}
		saved, err := store.get(ctx, "case")
		if err != nil || saved.Status != "failed" || saved.Result != nil || saved.ExecutionID != "execution-id" || saved.ValidationEvidence == nil || saved.ValidationEvidence.Publication == nil {
			t.Fatalf("failure not faithfully recorded: %+v %v", saved, err)
		}
	}
	decision := investigationDecision{Status: "inconclusive", Summary: strings.Repeat("é", 5001)}
	var field *responsecontract.FieldError
	if !errors.As(agent.validateInvestigationDecision(&decision), &field) || field.Actual != 10002 {
		t.Fatal("limit did not count UTF-8 bytes")
	}
	execution.Steps[0].Success = false
	if agent.resultValidationEvidence(ctx, input, execution, errors.New("untrusted prose")).Publication != nil {
		t.Fatal("failed publication promoted to evidence")
	}
	execution.Steps[0].Success = true
	if agent.confirmedPublication(investigationInput{CaseID: "other-case"}, execution) != nil {
		t.Fatal("cross-case publication accepted")
	}
}

// Operator-only check against the deployed Kind Redis. Isolated diagnostic
// records only: no task admission, AI request, tool dispatch or GitHub write.
func TestKindSummaryFailurePersistence(t *testing.T) {
	if os.Getenv("MAINTENANCE_SUMMARY_LIVE_APPROVED") != "one-no-ai-run" {
		t.Skip("operator-only Kind check")
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		t.Fatal("requires Kind")
	}
	profile, err := loadCoordinatorProfile(os.Getenv("MAINTENANCE_AGENT_PROFILE_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	if profile.Limits.SummaryLimit() != 10000 {
		t.Fatalf("effective summary limit = %d", profile.Limits.SummaryLimit())
	}
	connection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	client, err := core.NewRedisUniversalClient(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	keyspace, err := core.NewRedisKeyspace(fmt.Sprintf("summary-check-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	store, err := newCaseStore(client, keyspace)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	key := store.caseKey("diagnostic-fixture")
	record, err := json.Marshal(caseRecord{CaseID: "diagnostic-fixture", CurrentTaskID: "diagnostic-task", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, key, record, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := client.Del(cleanup, key).Err(); err != nil {
			t.Error(err)
		}
	}()
	evidence := resultValidationEvidence{ExecutionID: "labelled-local-fixture", Error: "summary exceeds its UTF-8 byte limit: 10001 > 10000", Publication: &publicationStepResponse{PublicationID: "labelled-local-fixture", CaseID: "diagnostic-fixture", Status: "succeeded", Draft: true, PullRequestURL: "https://example.invalid/diagnostic-only"}}
	if err := store.markResultValidationFailed(ctx, "diagnostic-fixture", "diagnostic-task", evidence, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := store.get(ctx, "diagnostic-fixture")
	if err != nil || got.Status != "failed" || got.Result != nil || got.ValidationEvidence == nil || got.ValidationEvidence.Publication == nil || got.ValidationEvidence.Publication.PublicationID != "labelled-local-fixture" || got.ExecutionID != evidence.ExecutionID {
		t.Fatalf("persisted evidence mismatch: %+v %v", got, err)
	}
	if err := store.markResultValidationFailed(ctx, "diagnostic-fixture", "other-task", evidence, time.Now()); !errors.Is(err, errCaseConflict) {
		t.Fatalf("task conflict not rejected: %v", err)
	}
	t.Log("effective_limit_bytes=10000 actual_redis_roundtrip=passed failure_preserved=true publication_evidence_preserved=true paid_calls=0")
}
