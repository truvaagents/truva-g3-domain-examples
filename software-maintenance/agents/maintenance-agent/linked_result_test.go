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

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

type linkedDiscovery struct{ core.Discovery }

func (linkedDiscovery) FindByCapability(_ context.Context, capability string) ([]*core.ServiceInfo, error) {
	issuer := "maintenance-assessment-tool"
	if capability == "get_remediation_state" {
		issuer = "maintenance-publication-tool"
	}
	return []*core.ServiceInfo{{Name: issuer, Type: core.ComponentTypeTool, Address: "linked-fixture.invalid", Port: 80, Capabilities: []core.Capability{{Name: capability, Endpoint: "/" + capability}}}}, nil
}

type linkedTransport func(*http.Request) (*http.Response, error)

func (f linkedTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func checkLinkedResults(t *testing.T, client redis.UniversalClient, keys core.RedisKeyspace) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	scope := lifecycle.Scope{CoordinatorProfileID: "labelled-link-profile", ServiceProfileID: "labelled-service", RepositoryID: "labelled-repository", EnvironmentID: "labelled-kind"}
	rule := lifecycle.Rule{CheckID: "labelled-check", Operation: "fixture", SignaturePaths: []string{"/observation/operation"}, ComparisonPaths: []string{"/observation/operation"}}
	policy := &lifecycle.Policy{SchemaVersion: 1, Revision: "labelled-v1", EnvironmentID: scope.EnvironmentID, Rules: []lifecycle.Rule{rule}}
	baseSHA, commitSHA, mergeSHA := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	facts, err := lifecycle.BuildFacts(scope, policy, rule, map[string]any{"observation": map[string]any{"operation": "fixture"}},
		map[string]json.RawMessage{"/release/git_revision": json.RawMessage(`"` + baseSHA + `"`), "/release/source_matches_commit": json.RawMessage(`true`)}, "origin-observation", now.Add(-time.Minute),
		[]lifecycle.EvidenceReference{{Capability: "labelled-evidence", ProfileID: scope.ServiceProfileID, ProfileVersion: "labelled-version", PayloadSHA256: lifecycle.Digest([]byte("labelled-fixture"))}}, 32768)
	if err != nil {
		t.Fatal(err)
	}
	store, err := lifecycle.NewStore(client, keys, scope)
	if err != nil {
		t.Fatal(err)
	}
	remediation := lifecycle.RemediationReference{PublicationID: "labelled-publication", OriginCaseID: "labelled-origin", OriginExecutionID: "labelled-origin-execution", PublicationProfileID: "labelled-publication-profile", PublicationProfileSHA256: "labelled-publication-hash", PullRequestURL: "https://example.invalid/labelled/pull/1"}
	assessment, err := store.Put(ctx, lifecycle.Assessment{SchemaVersion: 1, OriginCaseID: remediation.OriginCaseID, OriginExecutionID: remediation.OriginExecutionID, Disposition: "draft_pr_opened", Summary: "Labelled synthetic fixture, not a service assessment.", Facts: facts, Remediations: []lifecycle.RemediationReference{remediation}, AssessedAt: now.Add(-time.Minute), RevalidateAfter: now.Add(time.Hour)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Exact known fixture keys only. No scan and no application records deleted.
	scopeJSON, _ := json.Marshal(scope)
	cleanupKeys := []string{
		keys.Tagged("maintenance-lifecycle", lifecycle.Digest(scopeJSON), "assessment", assessment.AssessmentID),
		keys.Tagged("maintenance-lifecycle", lifecycle.Digest(scopeJSON), "issue", assessment.IssueID),
		keys.Tagged("maintenance-lifecycle", lifecycle.Digest(scopeJSON), "assessments"),
		keys.Tagged("maintenance-lifecycle", lifecycle.Digest(scopeJSON), "check", rule.CheckID),
		keys.Tagged("maintenance-lifecycle", lifecycle.Digest(scopeJSON), "due"),
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := client.Del(cleanup, cleanupKeys...).Err(); err != nil {
			t.Error(err)
		}
	})
	agent := &maintenanceAgent{assessments: store, now: func() time.Time { return now }, lifecycleLimits: lifecycle.Limits{RemediationMaxAge: time.Minute, RevalidateAfter: time.Hour},
		profile: coordinatorProfile{CoordinatorProfileID: scope.CoordinatorProfileID, ProfileSHA256: "labelled-coordinator-hash", RepositoryID: scope.RepositoryID, AssessmentPolicy: policy, InvestigationReady: true,
			PublicationProfileID: remediation.PublicationProfileID, ExpectedPublicationSHA256: remediation.PublicationProfileSHA256, Limits: coordinatorLimits{MaximumFindings: 10}}}
	current := facts
	current.ObservationID = "current-observation"
	match := lifecycle.Compare(assessment, current, policy, now, time.Hour)
	lookup := lifecycle.Lookup{CoordinatorProfileID: scope.CoordinatorProfileID, CoordinatorProfileSHA256: agent.profile.ProfileSHA256, ObservationID: current.ObservationID, CheckID: rule.CheckID, PolicyConfigured: true, CheckedAt: now, Candidates: []lifecycle.Match{match}}
	truth := true
	state := linkedRemediationObservation{SchemaVersion: 1, ProfileID: remediation.PublicationProfileID, ProfileSHA256: remediation.PublicationProfileSHA256, RepositoryID: scope.RepositoryID, PullRequestNumber: 1, PullRequestURL: remediation.PullRequestURL,
		State: "open", ObservedAt: now, ReferenceKind: "publication_receipt", OriginPublicationID: remediation.PublicationID, OriginCaseID: remediation.OriginCaseID, OriginPublicationProfileSHA256: remediation.PublicationProfileSHA256,
		PublishedCommitSHA: commitSHA, HeadSHA: commitSHA, BaseSHA: baseSHA, HeadMatchesPublicationCommit: &truth}
	step := func(issuer, capability string, data any) orchestration.StepResult {
		body, marshalErr := json.Marshal(map[string]any{"success": true, "data": data})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return orchestration.StepResult{Success: true, AgentName: issuer, Capability: capability, Response: string(body)}
	}
	for _, tc := range []struct {
		name, status string
		fail         bool
	}{
		{"known", "known_issue", false}, {"open", "existing_remediation", false}, {"merged_still_serving_base", "awaiting_deployment", false},
		{"closed", "existing_remediation", true}, {"changed_head", "existing_remediation", true}, {"wrong_origin", "existing_remediation", true},
		{"expired_read", "existing_remediation", true}, {"new_failed_read", "existing_remediation", true}, {"changed_facts", "known_issue", true},
		{"stale_planning_read_refreshed", "existing_remediation", false}, {"unrelated_planning_read", "existing_remediation", false},
		{"wrong_observation", "known_issue", true}, {"wrong_profile", "known_issue", true}, {"cancelled", "existing_remediation", true},
		{"policy_absent", "known_issue", true}, {"harness_failed", "known_issue", true}, {"unknown_deployment", "awaiting_deployment", true}, {"unsupported_resolution", "resolved_verified", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, comparison := state, lookup
			local := &maintenanceAgent{profile: agent.profile, assessments: agent.assessments, now: agent.now, lifecycleLimits: agent.lifecycleLimits}
			decision := investigationDecision{Status: tc.status, Summary: "Labelled current decision; production resolution is unknown.", Findings: []string{}, Assessment: &assessmentDecisionReference{assessment.AssessmentID, current.ObservationID, rule.CheckID}, NextCheck: &reconsiderationCondition{Condition: "Revalidate current facts and PR state.", NotBefore: now.Add(10 * time.Minute)}}
			if tc.status != "known_issue" {
				decision.Remediation = &remediationDecisionReference{remediation.PublicationID, remediation.OriginCaseID}
			}
			if tc.status == "awaiting_deployment" {
				snapshot.State, snapshot.Merged, snapshot.MergeSHA = "merged", &truth, mergeSHA
			}
			switch tc.name {
			case "closed":
				snapshot.State = "closed_unmerged"
			case "changed_head":
				snapshot.HeadSHA = mergeSHA
			case "wrong_origin":
				snapshot.OriginCaseID = "different-origin"
			case "expired_read":
				snapshot.ObservedAt = now.Add(-2 * time.Minute)
			case "changed_facts":
				comparison.Candidates = []lifecycle.Match{{Assessment: match.Assessment, Eligible: false, Reason: "facts_changed"}}
			case "wrong_observation":
				comparison.ObservationID = "unrelated-observation"
			case "wrong_profile":
				comparison.CoordinatorProfileSHA256 = "different-profile"
			case "policy_absent":
				local.profile.AssessmentPolicy = nil
			case "unknown_deployment":
				snapshot.BaseSHA = commitSHA
			}
			execution := &orchestration.ExecutionResult{Success: tc.name != "harness_failed", Steps: []orchestration.StepResult{step("maintenance-assessment-tool", "find_related_assessments", comparison), step("maintenance-publication-tool", "get_remediation_state", snapshot)}}
			if tc.name == "new_failed_read" {
				execution.Steps = append(execution.Steps, orchestration.StepResult{AgentName: "maintenance-publication-tool", Capability: "get_remediation_state", Error: "labelled provider unavailable"})
			}
			if tc.name == "stale_planning_read_refreshed" {
				stale := state
				stale.ObservedAt = now.Add(-4 * time.Minute)
				execution.Steps[1] = step("maintenance-publication-tool", "get_remediation_state", stale)
			}
			if tc.name == "unrelated_planning_read" {
				unrelated := lookup
				unrelated.ObservationID = "unrelated-observation"
				execution.Steps = append(execution.Steps, step("maintenance-assessment-tool", "find_related_assessments", unrelated))
			}
			calls := 0
			local.BaseAgent = core.NewBaseAgent(maintenanceAgentName)
			local.Logger = nil
			local.Discovery = linkedDiscovery{}
			local.httpClient = &http.Client{Timeout: time.Second, Transport: linkedTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				var params map[string]string
				if json.NewDecoder(r.Body).Decode(&params) != nil {
					t.Fatal("invalid refresh parameters")
				}
				var data any
				switch r.URL.Path {
				case "/find_related_assessments":
					if len(params) != 3 || params["profile_id"] != scope.CoordinatorProfileID || params["observation_id"] != current.ObservationID || params["check_id"] != rule.CheckID {
						t.Fatal("refresh changed the model-selected observation/check")
					}
					data = comparison
				case "/get_remediation_state":
					if len(params) != 3 || params["profile_id"] != remediation.PublicationProfileID || params["publication_id"] != remediation.PublicationID || params["origin_case_id"] != remediation.OriginCaseID {
						t.Fatal("refresh changed the model-selected publication")
					}
					if tc.name == "new_failed_read" {
						return nil, fmt.Errorf("labelled current provider unavailable")
					}
					data = snapshot
				default:
					t.Fatal("unexpected boundary capability")
				}
				encoded, _ := json.Marshal(core.ToolResponse{Success: true, Data: data})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(encoded))), Header: make(http.Header)}, nil
			})}
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if tc.name == "cancelled" {
				cancel()
			}
			body, _ := json.Marshal(decision)
			result, err := local.buildInvestigationResult(callCtx, investigationInput{CaseID: "current-case", ObservationRefs: []string{current.ObservationID}}, &orchestration.OrchestratorResponse{RequestID: "labelled-current-execution", Response: string(body)}, execution)
			if (err != nil) != tc.fail {
				t.Fatalf("status=%s expected_failure=%t error=%v", result.Status, tc.fail, err)
			}
			if !tc.fail && (result.Status != tc.status || result.PublicationID != "" || result.LinkedAssessment == nil || result.LinkedAssessment.OriginCaseID != remediation.OriginCaseID || result.NextCheck == nil) {
				t.Fatal("linked attribution or current publication distinction lost")
			}
			if !tc.fail && (result.LinkedAssessmentCheckedAt == nil || !result.LinkedAssessmentCheckedAt.Equal(now) || calls < 1 || calls > 2) {
				t.Fatal("fresh acceptance evidence or bounded read count missing")
			}
		})
	}
	t.Log("linked_positive_and_negative_gates=passed fixture_scope=labelled_only paid_calls=0 github_mutations=0")
}

func TestLinkedResultEvidenceGates(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	keys, err := core.NewRedisKeyspace("labelled-links-local")
	if err != nil {
		t.Fatal(err)
	}
	checkLinkedResults(t, client, keys)
}

func TestKindLinkedResultEvidenceGates(t *testing.T) {
	if os.Getenv("MAINTENANCE_ASSESSMENT_LIVE_APPROVED") != "one-no-ai-run" {
		t.Skip("operator-only labelled Kind check")
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		t.Fatal("requires Kind")
	}
	connection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	client, err := core.NewRedisUniversalClient(connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	keys, err := core.NewRedisKeyspace(fmt.Sprintf("v4-links-check-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	checkLinkedResults(t, client, keys)
}
