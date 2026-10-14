package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// Operator-only real-backend check: isolated TTL-bound fixtures, no case,
// Kubernetes Job, model call or publication.
func TestKindAtomicEvidence(t *testing.T) {
	if os.Getenv("MAINTENANCE_EVIDENCE_LIVE_APPROVED") != "one-no-ai-run" {
		t.Skip("requires explicit operator approval")
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		t.Fatal("requires Kind")
	}
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "workspace-evidence-diagnostic"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	if err := telemetry.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.atomic_workspace_evidence")
	defer span.End()
	span.SetAttribute("diagnostic.fixture_records", true)
	runID := fmt.Sprintf("evidence-cas-%d", time.Now().UnixNano())
	t.Logf("run=%s trace_id=%s paid_calls=0", runID, trace.SpanContextFromContext(ctx).TraceID())
	connection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	client, err := core.NewRedisUniversalClient(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	keyspace, err := core.NewRedisKeyspace(runID)
	if err != nil {
		t.Fatal(err)
	}
	op, fixture, original, _ := verificationReadFixture(t)
	op.store = &workspaceStore{client: client, keyspace: keyspace}
	for key, value := range fixture.values {
		if err := client.Set(ctx, strings.ReplaceAll(key, "default", runID), value, time.Hour).Err(); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, jobs, err := op.refreshVerificationSnapshot(ctx, original.VerificationID)
			if err != nil || !reflect.DeepEqual(got, original) || len(jobs) != 2 {
				t.Errorf("concurrent import/unchanged refresh invalidated approval: %v", err)
			}
		}()
	}
	wg.Wait()
	var stale verificationRecord
	vv, err := op.store.getEvidenceVersion(ctx, "verification", original.VerificationID, &stale)
	if err != nil {
		t.Fatal(err)
	}
	var job jobRecord
	jv, err := op.store.getEvidenceVersion(ctx, "job", original.CandidateJobIDs[0], &job)
	if err != nil {
		t.Fatal(err)
	}
	before := client.PTTL(ctx, jv.key).Val()
	// Controlled competing proposals; production never regresses terminal jobs.
	job.Status = "failed"
	if err := op.store.commitEvidence(ctx, []evidenceVersion{jv}, job); err != nil {
		t.Fatal(err)
	}
	job.Status = "running"
	if err := op.store.commitEvidence(ctx, []evidenceVersion{jv}, job); !errors.Is(err, errEvidenceChanged) {
		t.Fatalf("late job proposal accepted: %v", err)
	}
	if err := op.store.commitEvidence(ctx, []evidenceVersion{vv, jv}, stale); !errors.Is(err, errEvidenceChanged) {
		t.Fatalf("stale verification accepted: %v", err)
	}
	got, _, err := op.refreshVerificationSnapshot(ctx, original.VerificationID)
	if err != nil || got.Status != "failed" || got.PublicationEligible {
		t.Fatalf("fresh evidence not reconciled: %v", err)
	}
	again, _, err := op.refreshVerificationSnapshot(ctx, original.VerificationID)
	if err != nil || !reflect.DeepEqual(got, again) || client.PTTL(ctx, jv.key).Val() > before {
		t.Fatalf("repeat read changed timestamp/lifetime: %v", err)
	}
	t.Logf("PASS concurrent_imports=12 stale_job_rejected=true stale_verification_rejected=true approval_stable=true ttl_preserved=true redis_namespace=%s", runID)
}
