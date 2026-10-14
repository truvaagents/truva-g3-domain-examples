package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

func fixtureFacts(t *testing.T, observationID string) (Facts, *Policy) {
	t.Helper()
	scope := Scope{CoordinatorProfileID: "fixture-maintenance", ServiceProfileID: "fixture-service", RepositoryID: "fixture-repository", EnvironmentID: "fixture-qa"}
	rule := Rule{CheckID: "fixture-contract", Operation: "submit", SignaturePaths: []string{"/observation/operation", "/outcome/kind"}, ComparisonPaths: []string{"/outcome/input", "/outcome/result", "/configuration/effective"}}
	policy := &Policy{SchemaVersion: 1, Revision: "fixture-v1", EnvironmentID: scope.EnvironmentID, Rules: []Rule{rule}}
	document := map[string]any{"observation": map[string]any{"operation": "submit", "id": observationID}, "outcome": map[string]any{"kind": "accepted", "input": map[string]any{"class": "regular", "text": "code * and [1, 2, ] remain data"}, "result": json.Number("17")}, "configuration": map[string]any{"effective": map[string]any{"feature": true}}}
	mandatory := map[string]json.RawMessage{"/release/revision": json.RawMessage(`"build-fixture"`)}
	facts, err := BuildFacts(scope, policy, rule, document, mandatory, observationID, time.Now().Add(-time.Minute).UTC(), []EvidenceReference{{Capability: "fixture-evidence", ProfileID: scope.ServiceProfileID, ProfileVersion: "fixture-profile", PayloadSHA256: Digest([]byte("fixture"))}}, 32768)
	if err != nil {
		t.Fatal(err)
	}
	return facts, policy
}

func TestExplicitEmptyStringPolicy(t *testing.T) {
	document := map[string]any{"observation": map[string]any{"code": ""}}
	if _, err := Select(document, "/observation/code"); err == nil {
		t.Fatal("default selection must still reject empty strings")
	}
	if value, err := selectFact(document, "/observation/code", true); err != nil || string(value) != `""` {
		t.Fatalf("explicit empty value was not preserved: %s %v", value, err)
	}
	for _, value := range []any{nil} {
		document["observation"].(map[string]any)["code"] = value
		if _, err := selectFact(document, "/observation/code", true); err == nil {
			t.Fatal("explicit empty policy allowed null")
		}
	}
	_, policy := fixtureFacts(t, "fixture-empty-policy")
	policy.Rules[0].AllowEmptyStringPaths = []string{"/outcome/input"}
	if err := ValidatePolicy(policy); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{"/outcome/unused"}, {"/outcome/input", "/outcome/input"}} {
		policy.Rules[0].AllowEmptyStringPaths = paths
		if ValidatePolicy(policy) == nil {
			t.Fatal("invalid empty-string policy accepted")
		}
	}
}

func TestOwnerPredicateApplicability(t *testing.T) {
	original, policy := fixtureFacts(t, "first-observation")
	now := time.Now().UTC()
	record := Assessment{SchemaVersion: 1, Facts: original, AssessedAt: now.Add(-time.Minute), RevalidateAfter: now.Add(time.Hour)}
	current, _ := fixtureFacts(t, "different-observation")
	if match := Compare(record, current, policy, now, time.Hour); !match.Eligible {
		t.Fatalf("distinct IDs/same business facts should compare: %+v", match)
	}
	if IssueID(original) != IssueID(current) {
		t.Fatal("occurrence IDs leaked into owner-defined issue signature")
	}
	if string(current.Comparison["/outcome/input"]) != `{"class":"regular","text":"code * and [1, 2, ] remain data"}` {
		t.Fatal("application text changed")
	}
	changed := current
	changed.Comparison = map[string]json.RawMessage{}
	for path, value := range current.Comparison {
		changed.Comparison[path] = value
	}
	changed.Comparison["/release/revision"] = json.RawMessage(`"changed-build"`)
	encoded, _ := json.Marshal(changed.Comparison)
	changed.ComparedFactsDigest = Digest(encoded)
	if Compare(record, changed, policy, now, time.Hour).Eligible {
		t.Fatal("changed build reused assessment")
	}
	if Compare(record, current, policy, now.Add(2*time.Hour), time.Hour).Eligible {
		t.Fatal("expired assessment reused")
	}
	if Compare(record, current, nil, now, time.Hour).Eligible {
		t.Fatal("no owner policy allowed reuse")
	}
	changed = current
	changed.Scope.EnvironmentID = "other-environment"
	if Compare(record, changed, policy, now, time.Hour).Eligible {
		t.Fatal("cross-scope assessment reused")
	}
	for _, value := range []any{nil, "", map[string]any{"unknown": nil}} {
		if _, err := Select(map[string]any{"outcome": map[string]any{"value": value}}, "/outcome/value"); err == nil {
			t.Fatalf("unknown fact accepted: %#v", value)
		}
	}
	for _, value := range []any{false, json.Number("0"), []any{}, map[string]any{"known": false}} {
		if _, err := Select(map[string]any{"outcome": map[string]any{"value": value}}, "/outcome/value"); err != nil {
			t.Fatalf("known value rejected: %#v %v", value, err)
		}
	}
}

func TestAssessmentConditionalStore(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	keys, err := core.NewRedisKeyspace("assessment-test")
	if err != nil {
		t.Fatal(err)
	}
	facts, _ := fixtureFacts(t, "origin-observation")
	store, err := NewStore(client, keys, facts.Scope)
	if err != nil {
		t.Fatal(err)
	}
	checkAssessmentConditionalStore(t, store)
}

func checkAssessmentConditionalStore(t *testing.T, store *Store) {
	t.Helper()
	facts, _ := fixtureFacts(t, "origin-observation")
	t.Cleanup(func() {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		ids, err := store.client.ZRange(ctx, store.key("assessments"), 0, 10).Result()
		if err != nil {
			t.Error(err)
			return
		}
		keys := []string{store.key("assessments"), store.key("check", facts.CheckID), store.key("due")}
		for _, id := range ids {
			record, err := store.Get(ctx, id)
			if err != nil {
				t.Error(err)
				return
			}
			keys = append(keys, store.key("assessment", id), store.key("issue", record.IssueID))
		}
		if err := store.client.Del(ctx, keys...).Err(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC()
	base := Assessment{SchemaVersion: 1, OriginCaseID: "fixture-case", OriginExecutionID: "fixture-execution", Disposition: "benign", Summary: "Labelled fixture only.", Facts: facts, AssessedAt: now, RevalidateAfter: now.Add(time.Hour)}
	ctx := context.Background()
	if version, err := store.IssueVersion(ctx, facts); err != nil || version != 0 {
		t.Fatalf("initial authoritative issue version: %d %v", version, err)
	}
	first, err := store.Put(ctx, base, 0)
	if err != nil || first.Version != 1 {
		t.Fatalf("create: %+v %v", first, err)
	}
	if version, err := store.IssueVersion(ctx, facts); err != nil || version != first.Version {
		t.Fatalf("authoritative issue version: %d %v", version, err)
	}
	replayed, err := store.Put(ctx, base, 0)
	if err != nil || replayed.AssessmentID != first.AssessmentID || !replayed.AssessedAt.Equal(first.AssessedAt) {
		t.Fatal("replay changed immutable assessment")
	}
	secondInput := base
	secondInput.OriginExecutionID = "second-execution"
	if _, err := store.Put(ctx, secondInput, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale write accepted: %v", err)
	}
	second, err := store.Put(ctx, secondInput, 1)
	if err != nil || second.Version != 2 || second.SupersedesID != first.AssessmentID {
		t.Fatalf("supersede: %+v %v", second, err)
	}
	if current, err := store.Current(ctx, first); err != nil || current {
		t.Fatal("old assessment stayed current")
	}
	if current, err := store.Current(ctx, second); err != nil || !current {
		t.Fatal("new assessment not current")
	}
	listed, truncated, err := store.List(ctx, "", 1)
	if err != nil || len(listed) != 1 || !truncated {
		t.Fatalf("bounded list: %+v %t %v", listed, truncated, err)
	}
	otherScope := facts.Scope
	otherScope.EnvironmentID = "other-environment"
	other, err := NewStore(store.client, store.keys, otherScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Get(ctx, first.AssessmentID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-scope lookup exposed record")
	}
	renewal, err := store.Put(ctx, base, 2)
	if err != nil || renewal.Version != 3 || renewal.AssessmentID == first.AssessmentID || renewal.SupersedesID != second.AssessmentID {
		t.Fatalf("immutable renewal: %+v %v", renewal, err)
	}
	if current, err := store.Current(ctx, second); err != nil || current {
		t.Fatal("renewal left prior version current")
	}
	// Concurrent writers to a previously unseen signature cannot both own v1.
	facts.Signature["/outcome/kind"] = json.RawMessage(`"concurrent-fixture"`)
	var mutex sync.Mutex
	var won, conflicted int
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Go(func() {
			proposal := base
			proposal.Facts = facts
			proposal.OriginExecutionID = fmt.Sprintf("concurrent-fixture-%d", i)
			_, err := store.Put(ctx, proposal, 0)
			mutex.Lock()
			defer mutex.Unlock()
			if err == nil {
				won++
			} else if errors.Is(err, ErrConflict) {
				conflicted++
			} else {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if won != 1 || conflicted != 3 {
		t.Fatalf("concurrent ownership: won=%d conflicts=%d", won, conflicted)
	}
	restarted, err := NewStore(store.client, store.keys, store.scope)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.Get(ctx, second.AssessmentID); err != nil || got.OriginExecutionID != second.OriginExecutionID || got.Version != 2 {
		t.Fatalf("restart read: %+v %v", got, err)
	}
	t.Log("create=passed replay=passed stale_CAS=passed supersede=passed concurrent_CAS=passed bounded_lookup=passed scope_isolation=passed restart=passed paid_calls=0")
}

func TestKindAssessmentConditionalStore(t *testing.T) {
	if os.Getenv("MAINTENANCE_ASSESSMENT_LIVE_APPROVED") != "one-no-ai-run" {
		t.Skip("operator-only Kind check")
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
	keys, err := core.NewRedisKeyspace(fmt.Sprintf("v4-assessment-check-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	facts, _ := fixtureFacts(t, "origin-observation")
	store, err := NewStore(client, keys, facts.Scope)
	if err != nil {
		t.Fatal(err)
	}
	checkAssessmentConditionalStore(t, store)
}

func TestAssessmentConfigFailFast(t *testing.T) {
	limits, err := ResolveLimits(func(string) (string, bool) { return "", false })
	if err != nil || limits.PageLimit != 20 || limits.RevalidateAfter != time.Hour || limits.RemediationMaxAge != time.Minute {
		t.Fatalf("defaults: %+v %v", limits, err)
	}
	for key, values := range map[string][]string{
		"MAINTENANCE_ASSESSMENT_PAGE_LIMIT":        {"", "0", "-1", "51", "1.5"},
		"MAINTENANCE_ASSESSMENT_FACT_MAX_BYTES":    {"0", "1023", "131073"},
		"MAINTENANCE_ASSESSMENT_LOOKUP_MAX_BYTES":  {"0", "4095", "1048577"},
		"MAINTENANCE_ASSESSMENT_REVALIDATE_AFTER":  {"", "0", "30s", "25h", "bad"},
		"MAINTENANCE_REMEDIATION_STATE_MAX_AGE":    {"0", "6m"},
		"MAINTENANCE_LIFECYCLE_DUE_LIMIT":          {"0", "51"},
		"MAINTENANCE_LIFECYCLE_TERMINAL_RETENTION": {"90d", "1h", "9000h"},
	} {
		for _, value := range values {
			if _, err := ResolveLimits(func(k string) (string, bool) { return value, k == key }); err == nil {
				t.Fatalf("invalid %s=%q accepted", key, value)
			}
		}
	}
}
