package main

import (
	"context"
	"encoding/json"
	"errors"
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
)

func TestAssessmentRecordingLostResponse(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	checkAssessmentRecording(t, client)
}

func TestKindAssessmentRecordingLostResponse(t *testing.T) {
	if os.Getenv("MAINTENANCE_RECORDING_LIVE_APPROVED") != "one-no-ai-run" {
		t.Skip("operator-only isolated Redis check")
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		t.Fatal("requires Kind")
	}
	cfg, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	client, err := core.NewRedisUniversalClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	checkAssessmentRecording(t, client)
}

func checkAssessmentRecording(t *testing.T, client redis.UniversalClient) {
	t.Helper()
	ctx := t.Context()
	keys, err := core.NewRedisKeyspace(fmt.Sprintf("recording-fixture-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := newCaseStore(client, keys)
	if err != nil {
		t.Fatal(err)
	}
	scope := lifecycle.Scope{CoordinatorProfileID: "fixture-coordinator", ServiceProfileID: "fixture-service", RepositoryID: "fixture-repo", EnvironmentID: "fixture-kind"}
	rule := lifecycle.Rule{CheckID: "fixture-check", Operation: "fixture", SignaturePaths: []string{"/observation/kind"}, ComparisonPaths: []string{"/observation/kind"}}
	policy := &lifecycle.Policy{SchemaVersion: 1, Revision: "fixture-v1", EnvironmentID: scope.EnvironmentID, Rules: []lifecycle.Rule{rule}}
	now := time.Now().UTC()
	facts, err := lifecycle.BuildFacts(scope, policy, rule, map[string]any{"observation": map[string]any{"kind": "fixture"}}, map[string]json.RawMessage{"/release/revision": json.RawMessage(`"fixture"`)}, "fixture-observation", now, []lifecycle.EvidenceReference{{Capability: "fixture-evidence", PayloadSHA256: "fixture"}}, 32768)
	if err != nil {
		t.Fatal(err)
	}
	store, err := lifecycle.NewStore(client, keys, scope)
	if err != nil {
		t.Fatal(err)
	}
	input := investigationInput{CaseID: "fixture-case", ObservationRefs: []string{facts.ObservationID}}
	result := investigationResult{CaseID: input.CaseID, ExecutionID: "fixture-execution", ExecutionSuccess: true, Status: "benign", Summary: "Labelled fixture only."}
	origin := caseRecord{SchemaVersion: 1, CaseID: input.CaseID, CurrentTaskID: "fixture-task", Status: "completed", Result: &result}
	raw, _ := json.Marshal(origin)
	if err := client.Set(ctx, cases.caseKey(input.CaseID), raw, 0).Err(); err != nil {
		t.Fatal(err)
	}
	cleanupKeys := []string{cases.caseKey(input.CaseID)}
	scopeJSON, _ := json.Marshal(scope)
	for _, parts := range [][]string{{"issue", lifecycle.IssueID(facts)}, {"assessments"}, {"check", rule.CheckID}, {"due"}} {
		cleanupKeys = append(cleanupKeys, keys.Tagged("maintenance-lifecycle", lifecycle.Digest(scopeJSON), parts...))
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Del(cleanup, cleanupKeys...).Err(); err != nil {
			t.Error(err)
		}
	})
	// More than a page of genuine store records: the lookup is truncated but
	// IssueVersion still returns the authoritative CAS value independently.
	base := lifecycle.Assessment{SchemaVersion: 1, OriginCaseID: input.CaseID, OriginExecutionID: result.ExecutionID, Disposition: "benign", Summary: result.Summary, Facts: facts, AssessedAt: now, RevalidateAfter: now.Add(time.Hour)}
	for i := 0; i < 21; i++ {
		historical := base
		historical.OriginExecutionID = fmt.Sprintf("fixture-history-%d", i)
		saved, err := store.Put(ctx, historical, uint64(i))
		if err != nil {
			t.Fatal(err)
		}
		cleanupKeys = append(cleanupKeys, keys.Tagged("maintenance-lifecycle", lifecycle.Digest(scopeJSON), "assessment", saved.AssessmentID))
	}
	reads, writes := 0, 0
	var receipt lifecycle.Assessment
	agent := &maintenanceAgent{BaseAgent: core.NewBaseAgent("fixture-coordinator"), cases: cases, profile: coordinatorProfile{CoordinatorProfileID: scope.CoordinatorProfileID, ProfileSHA256: "fixture-profile", AssessmentPolicy: policy}, lifecycleLimits: lifecycle.Limits{RecordingTimeout: time.Minute}, now: func() time.Time { return now }}
	agent.Discovery = linkedDiscovery{}
	agent.httpClient = &http.Client{Timeout: time.Minute, Transport: linkedTransport(func(r *http.Request) (*http.Response, error) {
		var value any
		switch r.URL.Path {
		case "/find_related_assessments":
			reads++
			records, truncated, err := store.List(ctx, rule.CheckID, 20)
			if err != nil || len(records) != 20 || !truncated {
				t.Fatal("history fixture did not truncate", err)
			}
			version, err := store.IssueVersion(ctx, facts)
			if err != nil {
				t.Fatal(err)
			}
			value = lifecycle.Lookup{CoordinatorProfileID: scope.CoordinatorProfileID, CoordinatorProfileSHA256: agent.profile.ProfileSHA256, ObservationID: facts.ObservationID, CheckID: rule.CheckID, PolicyConfigured: true, CurrentIssueID: lifecycle.IssueID(facts), CurrentIssueVersion: &version, CurrentFactsDigest: facts.ComparedFactsDigest, Truncated: truncated}
		case "/record_assessment":
			writes++
			var payload struct {
				Version   uint64 `json:"expected_issue_version"`
				Execution string `json:"expected_origin_execution_id"`
				Issue     string `json:"expected_issue_id"`
				Facts     string `json:"expected_facts_digest"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			saved, err := cases.get(ctx, input.CaseID)
			if err != nil || len(saved.AssessmentRecording) != 1 || saved.AssessmentRecording[0].Intent == nil {
				t.Fatal("write preceded durable intent", err)
			}
			if payload.Version != 21 || payload.Execution != result.ExecutionID || payload.Issue != lifecycle.IssueID(facts) || payload.Facts != facts.ComparedFactsDigest {
				t.Fatal("write intent changed")
			}
			receipt, err = store.Put(ctx, base, payload.Version)
			if err != nil {
				t.Fatal(err)
			}
			cleanupKeys = append(cleanupKeys, keys.Tagged("maintenance-lifecycle", lifecycle.Digest(scopeJSON), "assessment", receipt.AssessmentID))
			if writes == 1 {
				return nil, errors.New("labelled lost response after committed write")
			}
			value = receipt
		default:
			t.Fatal("unexpected capability", r.URL.Path)
		}
		encoded, _ := json.Marshal(core.ToolResponse{Success: true, Data: value})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(encoded)))}, nil
	})}
	agent.recordCompletedAssessments(ctx, input, result)
	saved, err := cases.get(ctx, input.CaseID)
	if err != nil || len(saved.AssessmentRecording) != 1 || saved.AssessmentRecording[0].Status != "unavailable" || saved.AssessmentRecording[0].Intent == nil {
		t.Fatal("lost response intent not retained", err)
	}
	// A concurrent preparer that observed the advanced version still adopts
	// the first durable intent instead of replacing it with a renewal.
	second := saved.AssessmentRecording[0]
	secondIntent := *second.Intent
	secondIntent.ExpectedIssueVersion++
	second.Intent = &secondIntent
	selected, err := agent.saveAssessmentRecording(ctx, input.CaseID, second, true)
	if err != nil || selected.Intent.ExpectedIssueVersion != 21 {
		t.Fatal("second preparer replaced original intent", err)
	}
	// New agent object: no in-process memory is needed for re-entry.
	restartedStore, err := newCaseStore(client, keys)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &maintenanceAgent{BaseAgent: core.NewBaseAgent("fixture-restarted"), cases: restartedStore, profile: agent.profile, lifecycleLimits: agent.lifecycleLimits, now: agent.now, httpClient: agent.httpClient}
	restarted.Discovery = linkedDiscovery{}
	restarted.recordCompletedAssessments(ctx, input, result)
	saved, err = cases.get(ctx, input.CaseID)
	if err != nil || saved.AssessmentRecording[0].Status != "recorded" || saved.AssessmentRecording[0].AssessmentID != receipt.AssessmentID || receipt.Version != 22 || reads != 1 || writes != 2 {
		t.Fatalf("replay renewed or lost assessment: reads=%d writes=%d saved=%+v err=%v", reads, writes, saved.AssessmentRecording, err)
	}
	// A late diagnostic must not erase the already persisted receipt.
	late := saved.AssessmentRecording[0]
	late.Status = "unavailable"
	late.AssessmentID = ""
	retained, err := agent.saveAssessmentRecording(ctx, input.CaseID, late, false)
	if err != nil || retained.Status != "recorded" {
		t.Fatal("late failure erased receipt", err)
	}
	restarted.recordCompletedAssessments(ctx, input, result)
	if writes != 2 || reads != 1 {
		t.Fatal("recorded assessment unnecessarily dispatched again")
	}
	version, err := store.IssueVersion(ctx, facts)
	if err != nil || version != 22 {
		t.Fatal("retry silently renewed issue", version, err)
	}
	stale := late
	stale.ExecutionID = "different-execution"
	if _, err := agent.saveAssessmentRecording(ctx, input.CaseID, stale, true); !errors.Is(err, errCaseConflict) {
		t.Fatal("stale execution accepted", err)
	}
	t.Log("truncated_history=passed persisted_before_dispatch=passed lost_response_replay=passed restart=passed stale_execution=passed receipt_retention=passed paid_calls=0")
}
