package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// Operator-only integration check. Normal tests skip it. This creates one
// sandbox Job and isolated, expiring fixture records, not an admitted case.
func TestKindOversizedLogEvidence(t *testing.T) {
	if os.Getenv("MAINTENANCE_LOG_LIVE_APPROVED") != "one-no-ai-run" {
		t.Skip("requires explicit operator approval")
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		t.Fatal("requires the existing Kind workspace-tool pod")
	}
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "workspace-log-diagnostic"
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
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.oversized_job_logs")
	defer span.End()
	span.SetAttribute("diagnostic.fixture_records", true)
	runID := fmt.Sprintf("log-prefix-%d", time.Now().UnixNano())
	t.Logf("run=%s trace_id=%s paid_calls=0", runID, trace.SpanContextFromContext(ctx).TraceID())
	runner, err := newKubernetesRunnerFromEnvironment(true)
	if err != nil {
		t.Fatal(err)
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
	keyspace, err := core.NewRedisKeyspace(runID)
	if err != nil {
		t.Fatal(err)
	}
	operations, fixture, verification, _ := verificationReadFixture(t)
	operations.store = &workspaceStore{client: client, keyspace: keyspace}
	operations.runner = runner
	for key, value := range fixture.values {
		// Fixture keys all use the default deployment; both namespace and hash
		// tag are replaced. No existing application's records are touched.
		key = strings.ReplaceAll(key, "default", runID)
		if err := client.Set(ctx, key, value, time.Hour).Err(); err != nil {
			t.Fatal(err)
		}
	}
	profiles, err := loadWorkspaceProfiles(os.Getenv("MAINTENANCE_WORKSPACE_PROFILES_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles.profiles) != 1 {
		t.Fatal("expected one operator-owned sandbox profile")
	}
	profile := operations.profiles.profiles[verification.ProfileID]
	for _, configured := range profiles.profiles {
		profile.RunnerImage, profile.WorkspacePVC = configured.RunnerImage, configured.WorkspacePVC
	}
	const limit = 524288
	const outputBytes = 600 * 1024
	// Fixed diagnostic output only. No caller-supplied shell or repository code.
	command := commandProfile{ID: "log-prefix-check", Argv: []string{"/bin/sh", "-c", "head -c 614400 /dev/zero | tr '\\000' x"}}
	profile.commandByID = map[string]commandProfile{command.ID: command}
	profile.Limits.MaximumJobLogBytes, profile.Limits.MaximumJobSeconds = limit, 60
	operations.profiles.profiles[profile.WorkspaceProfileID] = profile
	directory, err := os.MkdirTemp("/var/lib/maintenance-workspaces", "log-prefix-diagnostic-")
	if err != nil {
		t.Fatal(err)
	}
	// Empty read-only mount; allow the non-root runner to use its working directory.
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Remove(directory); err != nil {
			t.Error(err)
		}
	}()
	var candidate candidateRecord
	if err := operations.store.get(ctx, "candidate", verification.CandidateID, &candidate); err != nil {
		t.Fatal(err)
	}
	commandBytes, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	record := jobRecord{SchemaVersion: 1, JobID: verification.CandidateJobIDs[0], KubernetesJob: runID,
		WorkspaceID: candidate.WorkspaceID, CandidateID: candidate.CandidateID, VerificationID: verification.VerificationID,
		ProfileID: profile.WorkspaceProfileID, ProfileSHA256: profile.profileSHA256, CaseID: candidate.CaseID,
		CommandID: command.ID, CommandSHA256: fmt.Sprintf("%x", sha256.Sum256(commandBytes)),
		RunnerImage: profile.RunnerImage, Status: "pending", CreatedAt: operations.now(), ExpiresAt: candidate.ExpiresAt}
	if err := runner.submit(ctx, profile, record, filepath.Base(directory), command); err != nil {
		t.Fatal(err)
	}
	if err := operations.store.putMutable(ctx, "job", record.JobID, record, time.Hour); err != nil {
		t.Fatal(err)
	}
	var state runnerState
	for {
		state, err = runner.inspect(ctx, record.KubernetesJob, limit)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == "succeeded" || state.Status == "failed" || state.Status == "timed_out" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if state.Status != "succeeded" || state.ExitCode == nil || *state.ExitCode != 0 || state.CompletedAt.IsZero() || !state.LogTruncated || !bytes.Equal(state.Logs, bytes.Repeat([]byte("x"), limit)) {
		t.Fatalf("incorrect terminal evidence: status=%s truncated=%v bytes=%d", state.Status, state.LogTruncated, len(state.Logs))
	}
	full, err := runner.inspect(ctx, record.KubernetesJob, outputBytes+1)
	if err != nil || full.LogTruncated || len(full.Logs) != outputBytes || !bytes.Equal(full.Logs[:min(len(full.Logs), limit)], state.Logs) {
		t.Fatalf("full log/prefix mismatch: %v bytes=%d", err, len(full.Logs))
	}
	observed, err := operations.getJob(ctx, record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256(state.Logs))
	if observed.Status != "succeeded" || !observed.ArtifactTruncated || observed.ArtifactBytes != limit || observed.ArtifactSHA256 != wantHash {
		t.Fatalf("stored receipt lost evidence: %+v", observed)
	}
	_, stored, err := operations.store.getArtifactSlice(ctx, record.CaseID, observed.ArtifactID, 0, limit)
	if err != nil || !bytes.Equal(stored, state.Logs) {
		t.Fatalf("stored bytes differ: %v", err)
	}
	var checked verificationRecord
	if err := operations.store.get(ctx, "verification", verification.VerificationID, &checked); err != nil {
		t.Fatal(err)
	}
	if checked.PublicationEligible || checked.SuitePassed || checked.Status != "failed" || !checked.ExpectedTestObserved {
		t.Fatalf("truncated candidate evidence accepted: %+v", checked)
	}
	again, err := operations.getJob(ctx, record.JobID)
	if err != nil || !reflect.DeepEqual(observed, again) {
		t.Fatalf("repeat read changed receipt: %v", err)
	}
	var repeated verificationRecord
	if err := operations.store.get(ctx, "verification", verification.VerificationID, &repeated); err != nil || !reflect.DeepEqual(checked, repeated) {
		t.Fatalf("repeat read invalidated verification timestamp: %v", err)
	}
	t.Logf("PASS job=%s full_bytes=%d retained_bytes=%d sha256=%s truncated=true publication_eligible=false repeated_read_stable=true redis_namespace=%s", record.KubernetesJob, len(full.Logs), len(stored), wantHash, runID)
}
