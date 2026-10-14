package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

func checkCaseEffectTransitions(t *testing.T, store *caseStore) {
	t.Helper()
	ctx, stop := context.WithTimeout(t.Context(), 30*time.Second)
	defer stop()
	now := time.Now().UTC()
	caseID := "labelled-effect-fixture"
	key := store.caseKey(caseID)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.client.Del(cleanup, key).Err(); err != nil {
			t.Error(err)
		}
	})
	publication := publicationStepResponse{PublicationID: "labelled-publication", ProfileID: "labelled-profile", ProfileSHA256: "labelled-hash", CaseID: caseID, Status: "succeeded", Draft: true, PullRequestURL: "https://example.invalid/fixture-only"}
	// Seed the prior schema: a real effect was carried only in a failed
	// current-attempt diagnostic, not in an accepted model result.
	legacy := caseRecord{SchemaVersion: 1, CaseID: caseID, CurrentTaskID: "task", Status: "failed", UpdatedAt: now,
		ValidationEvidence: &resultValidationEvidence{ExecutionID: "original-execution", Error: "first validation failure", Publication: &publication}}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.client.Set(ctx, key, encoded, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.markResultValidationFailed(ctx, caseID, "task", resultValidationEvidence{ExecutionID: "later-execution", Error: "failure without a publication"}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := store.get(ctx, caseID)
	if err != nil || len(got.PublicationEffects) != 1 || got.PublicationEffects[publication.PublicationID].OriginExecutionID != "original-execution" || got.ValidationEvidence.Publication != nil || got.ValidationEvidence.ExecutionID != "later-execution" {
		t.Fatalf("historical/current evidence mixed: %+v %v", got, err)
	}
	if err := store.markQueued(ctx, caseID, "task", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := store.claimExecution(ctx, caseID, "task", now.Add(3*time.Second)); err != nil || !claimed {
		t.Fatalf("claim: %t %v", claimed, err)
	}
	if err := store.retainExecutionEffects(ctx, caseID, "task", "second-observer", []publicationStepResponse{publication, publication}, now); err != nil {
		t.Fatal(err)
	}
	if err := store.complete(ctx, caseID, "task", investigationResult{CaseID: caseID, ExecutionID: "completed-execution", Status: "inconclusive", Summary: "No new publication claimed."}, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err = store.get(ctx, caseID)
	effect := got.PublicationEffects[publication.PublicationID]
	if err != nil || got.Status != "completed" || got.ValidationEvidence != nil || effect.Publication != publication || effect.OriginExecutionID != "original-execution" || len(effect.ObservedExecutionIDs) != 2 {
		t.Fatalf("completion erased/duplicated evidence: %+v %v", got, err)
	}
	conflict := publication
	conflict.PullRequestURL = "https://example.invalid/different-fixture"
	if err := store.retainExecutionEffects(ctx, caseID, "task", "other-observer", []publicationStepResponse{conflict}, now); err == nil {
		t.Fatal("conflicting immutable effect accepted")
	}
	if err := store.markExecutionFailed(ctx, caseID, "other-task", "fixture", now); !errors.Is(err, errCaseConflict) {
		t.Fatalf("stale task accepted: %v", err)
	}
	got, err = store.get(ctx, caseID)
	if err != nil || got.PublicationEffects[publication.PublicationID].Publication != publication {
		t.Fatal("rejected write changed stored facts")
	}
	// A new store instance observes the same retained effects after restart.
	restarted, err := newCaseStore(store.client, store.keyspace)
	if err != nil {
		t.Fatal(err)
	}
	got, err = restarted.get(ctx, caseID)
	if err != nil || len(got.PublicationEffects) != 1 {
		t.Fatalf("restart retention: %+v %v", got, err)
	}
	t.Log("legacy_migration=passed current_failure_cleared=passed effect_retention=passed duplicate_observer=passed stale_conflict_rejected=passed restart_read=passed paid_calls=0")
}

func TestCaseEffectTransitions(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	keys, err := core.NewRedisKeyspace("effects-local-check")
	if err != nil {
		t.Fatal(err)
	}
	store, err := newCaseStore(client, keys)
	if err != nil {
		t.Fatal(err)
	}
	checkCaseEffectTransitions(t, store)
}

func TestKindCaseEffectTransitions(t *testing.T) {
	if os.Getenv("MAINTENANCE_EFFECT_LIVE_APPROVED") != "one-no-ai-run" {
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
	keys, err := core.NewRedisKeyspace(fmt.Sprintf("v4-effects-check-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	store, err := newCaseStore(client, keys)
	if err != nil {
		t.Fatal(err)
	}
	checkCaseEffectTransitions(t, store)
}
