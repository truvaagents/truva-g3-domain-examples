package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
	"go.opentelemetry.io/otel/trace"
)

func (r *verificationRedis) SetNX(ctx context.Context, key string, value any, ttl time.Duration) *redis.BoolCmd {
	if _, canonical := value.(string); canonical {
		return r.UniversalClient.SetNX(ctx, key, value, ttl)
	}
	if _, exists := r.values[key]; exists {
		return redis.NewBoolResult(false, nil)
	}
	r.values[key] = string(value.([]byte))
	return r.UniversalClient.SetNX(ctx, key, value, ttl)
}

// Uses the production bundle handler and canonical candidate construction. Only
// storage and completed worker observations are fixtures; no model is involved.
func publicationBoundaryFixture(t *testing.T) (*maintenanceWorkspaceTool, *verificationRedis, verificationRecord, string) {
	t.Helper()
	op, r, v, contentKey := verificationReadFixture(t)
	ctx := context.Background()
	c, w, p, err := op.loadCandidate(ctx, v.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	w.VerificationReady = true
	w.CreatedAt = op.now().Add(-3 * time.Minute)
	c.CreatedAt = op.now().Add(-2 * time.Minute)
	p.RunnerImage = "example.invalid/runner@sha256:" + strings.Repeat("a", 64)
	p.commandByID = map[string]commandProfile{"test": {ID: "test", Argv: []string{"go", "test", "-json", "./..."}}}
	op.profiles.profiles[p.WorkspaceProfileID] = p
	v.VerificationID = fmt.Sprintf("verify_%x", sha256.Sum256([]byte(c.CandidateID+"\x00"+p.WorkspaceProfileID+"\x00"+p.profileSHA256+"\x00verification-v1")))
	command, err := json.Marshal(p.commandByID["test"])
	if err != nil {
		t.Fatal(err)
	}
	put := func(kind, id string, value any) {
		t.Helper()
		if err := op.store.putMutable(ctx, kind, id, value, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	for i, target := range []string{"reproduction", "candidate"} {
		idem, status, exit := "verification-candidate-test", "succeeded", int32(0)
		logs := "ok\n"
		if i == 0 {
			idem, status, exit, logs = "verification-reproduction", "failed", 1, r.values[contentKey]
		}
		id := fmt.Sprintf("job_%x", sha256.Sum256([]byte(w.WorkspaceID+"\x00"+c.CandidateID+"\x00"+v.VerificationID+"\x00"+target+"\x00test\x00"+idem)))
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(logs)))
		j := jobRecord{SchemaVersion: 1, JobID: id, WorkspaceID: w.WorkspaceID, CandidateID: c.CandidateID, VerificationID: v.VerificationID, ProfileID: w.ProfileID, ProfileSHA256: w.ProfileSHA256, CaseID: w.CaseID, Target: target, CommandID: "test", CommandSHA256: fmt.Sprintf("%x", sha256.Sum256(command)), RunnerImage: p.RunnerImage, Status: status, ExitCode: &exit, ArtifactID: "art_" + digest, ArtifactSHA256: digest, ArtifactBytes: int64(len(logs)), CreatedAt: v.CreatedAt, CompletedAt: v.UpdatedAt, ExpiresAt: w.ExpiresAt}
		put("job", id, j)
		if err := op.ensureArtifactMetadata(ctx, j); err != nil {
			t.Fatal(err)
		}
		key := op.store.artifactContentKey(j.ArtifactID)
		r.values[key] = logs
		if i == 0 {
			v.ReproductionJobID, contentKey = id, key
		} else {
			v.CandidateJobIDs = []string{id}
		}
	}
	put("workspace", w.WorkspaceID, w)
	put("candidate", c.CandidateID, c)
	put("verification", v.VerificationID, v)
	auth, err := newWorkspaceAuthorizer(map[string]string{"publication": strings.Repeat("p", 40)})
	if err != nil {
		t.Fatal(err)
	}
	return &maintenanceWorkspaceTool{BaseTool: core.NewTool(workspaceToolName), profiles: op.profiles, operations: op, auth: auth, publicationIdentity: "publication"}, r, v, contentKey
}

type boundaryLogger struct {
	core.NoOpLogger
	fields   []map[string]interface{}
	contexts []context.Context
}

func (l *boundaryLogger) WarnWithContext(ctx context.Context, _ string, fields map[string]interface{}) {
	l.fields = append(l.fields, fields)
	l.contexts = append(l.contexts, ctx)
}

func TestPublicationBundleReadFailureObservation(t *testing.T) {
	for _, mode := range []string{"transient", "missing", "short"} {
		t.Run(mode, func(t *testing.T) {
			tool, r, v, key := publicationBoundaryFixture(t)
			logger := &boundaryLogger{}
			tool.Logger = logger
			switch mode {
			case "transient":
				r.readError = errors.New("private backend body")
			case "missing":
				delete(r.values, key)
			case "short":
				r.values[key] = r.values[key][:1]
			}
			input, err := json.Marshal(publicationBundleRequest{CaseID: "case", CandidateID: v.CandidateID, VerificationID: v.VerificationID})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/capabilities/get_publication_bundle", strings.NewReader(string(input)))
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("p", 40))
			sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
			req = req.WithContext(trace.ContextWithSpanContext(core.WithRequestID(req.Context(), "boundary-request"), sc))
			out := httptest.NewRecorder()
			tool.handleGetPublicationBundle(out, req)
			want := http.StatusInternalServerError
			if mode == "transient" {
				want = http.StatusServiceUnavailable
			}
			if out.Code != want || strings.Contains(out.Body.String(), "private backend body") {
				t.Fatalf("unexpected response: %d %s", out.Code, out.Body.String())
			}
			if out.Header().Get("X-Request-ID") != "boundary-request" || out.Header().Get("X-Trace-ID") != sc.TraceID().String() {
				t.Fatal("correlation lost")
			}
			if len(logger.fields) != 1 || logger.fields[0]["status"] != "error" || logger.fields[0]["error_type"] != "dependency_or_state" || logger.fields[0]["operation"] != "get_publication_bundle" || trace.SpanContextFromContext(logger.contexts[0]).TraceID() != sc.TraceID() {
				t.Fatalf("incorrect failure observation: %+v", logger.fields)
			}
			var after verificationRecord
			if err := tool.operations.store.get(req.Context(), "verification", v.VerificationID, &after); err != nil {
				t.Fatal(err)
			}
			if after.Status != v.Status || !after.UpdatedAt.Equal(v.UpdatedAt) {
				t.Fatal("read failure changed verified outcome")
			}
		})
	}
}

// Explicit test-binary composition entrypoint. The publication package starts
// this child with a private pipe; EOF stops it. It is absent from runtime images.
func TestPublicationBundleServer(t *testing.T) {
	if os.Getenv("MAINTENANCE_BUNDLE_TEST_SERVER") != "1" {
		t.Skip("test-binary composition only")
	}
	tool, r, v, key := publicationBoundaryFixture(t)
	logs := r.values[key]
	var lock sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		lock.Lock()
		defer lock.Unlock()
		r.readError = nil
		r.values[key] = logs
		current := v
		switch req.URL.Query().Get("fixture") {
		case "read_error":
			r.readError = errors.New("private backend body")
		case "missing":
			delete(r.values, key)
		case "newer":
			current.UpdatedAt = tool.operations.now()
		}
		var stored verificationRecord
		version, err := tool.operations.store.getEvidenceVersion(req.Context(), "verification", v.VerificationID, &stored)
		if err == nil {
			err = tool.operations.store.commitEvidence(req.Context(), []evidenceVersion{version}, current)
		}
		if err != nil {
			http.Error(w, "fixture failure", 500)
			return
		}
		tool.handleGetPublicationBundle(w, req)
	}))
	defer server.Close()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"url": server.URL, "candidate_id": v.CandidateID, "verification_id": v.VerificationID, "now": tool.operations.now()}); err != nil {
		t.Fatal(err)
	}
	_, err := io.Copy(io.Discard, os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
}
