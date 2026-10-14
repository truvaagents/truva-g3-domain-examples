package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestEvidenceStaleRunningCannotRegressTerminal(t *testing.T) {
	op, original := observationFixture(t)
	ctx := t.Context()
	entered, release := make(chan struct{}), make(chan struct{})
	op.runner.httpClient.Transport = jobObservationTransport(func(*http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":{"active":1}}`))}, nil
	})
	done := make(chan error, 1)
	go func() {
		job, err := op.observeJob(ctx, original.JobID, false)
		if err == nil && job.Status != "succeeded" {
			err = errors.New("stale observer returned its own running state")
		}
		done <- err
	}()
	<-entered
	var terminal jobRecord
	version, err := op.store.getEvidenceVersion(ctx, "job", original.JobID, &terminal)
	if err != nil {
		t.Fatal(err)
	}
	terminal.Status, terminal.CompletedAt = "succeeded", time.Now()
	if err := op.store.commitEvidence(ctx, []evidenceVersion{version}, terminal); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var got jobRecord
	if err := op.store.get(ctx, "job", original.JobID, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "succeeded" || !got.CompletedAt.Equal(terminal.CompletedAt) {
		t.Fatal("terminal evidence regressed")
	}
}

type lostEvidenceReply struct{ redis.UniversalClient }

func (r lostEvidenceReply) EvalSha(ctx context.Context, sha string, keys []string, args ...interface{}) *redis.Cmd {
	cmd := r.UniversalClient.EvalSha(ctx, sha, keys, args...)
	if cmd.Err() == nil {
		cmd.SetErr(errors.New("synthetic lost commit reply"))
	}
	return cmd
}

func (r lostEvidenceReply) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	cmd := r.UniversalClient.Eval(ctx, script, keys, args...)
	if cmd.Err() == nil {
		cmd.SetErr(errors.New("synthetic lost commit reply"))
	}
	return cmd
}

func TestEvidenceUnknownCommitOutcomeCanBeReadWithoutRewrite(t *testing.T) {
	op, _, original, _ := verificationReadFixture(t)
	var record verificationRecord
	version, err := op.store.getEvidenceVersion(t.Context(), "verification", original.VerificationID, &record)
	if err != nil {
		t.Fatal(err)
	}
	record.UpdatedAt = record.UpdatedAt.Add(time.Second)
	client := op.store.client
	op.store.client = lostEvidenceReply{client}
	if err := op.store.commitEvidence(t.Context(), []evidenceVersion{version}, record); err == nil {
		t.Fatal("unknown commit reported success")
	}
	op.store.client = client
	got, _, err := op.refreshVerificationSnapshot(t.Context(), original.VerificationID)
	if err != nil || !got.UpdatedAt.Equal(record.UpdatedAt) || got.Status != original.Status {
		t.Fatalf("read after lost reply changed committed evidence: %v", err)
	}
}

func TestEvidenceSnapshotRejectsChangedOrMissingJobs(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed", true: "missing"}[missing], func(t *testing.T) {
			op, r, original, _ := verificationReadFixture(t)
			var v verificationRecord
			vv, err := op.store.getEvidenceVersion(t.Context(), "verification", original.VerificationID, &v)
			if err != nil {
				t.Fatal(err)
			}
			var job jobRecord
			jv, err := op.store.getEvidenceVersion(t.Context(), "job", v.CandidateJobIDs[0], &job)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Split(vv.key, "}")[0] != strings.Split(jv.key, "}")[0] {
				t.Fatal("cross-slot snapshot")
			}
			if missing {
				if err := r.UniversalClient.Del(t.Context(), jv.key).Err(); err != nil {
					t.Fatal(err)
				}
			} else {
				job.Status = "failed"
				if err := op.store.commitEvidence(t.Context(), []evidenceVersion{jv}, job); err != nil {
					t.Fatal(err)
				}
			}
			v.Status, v.PublicationEligible = "running", false
			if err := op.store.commitEvidence(t.Context(), []evidenceVersion{vv, jv}, v); !errors.Is(err, errEvidenceChanged) {
				t.Fatalf("stale snapshot committed: %v", err)
			}
			var got verificationRecord
			if err := op.store.get(t.Context(), "verification", v.VerificationID, &got); err != nil {
				t.Fatal(err)
			}
			if got.Status != original.Status || !got.UpdatedAt.Equal(original.UpdatedAt) {
				t.Fatal("partial verification mutation")
			}
		})
	}
}

func TestEvidenceConcurrentImportAndExpiry(t *testing.T) {
	op, original := observationFixture(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var job jobRecord
			if _, err := op.store.getEvidenceVersion(ctx, "job", original.JobID, &job); err != nil {
				t.Error(err)
			}
			if job.JobID != original.JobID {
				t.Error("import identity changed")
			}
		}()
	}
	wg.Wait()
	var job jobRecord
	version, err := op.store.getEvidenceVersion(ctx, "job", original.JobID, &job)
	if err != nil {
		t.Fatal(err)
	}
	client := op.store.client.(*verificationRedis).UniversalClient
	before, err := client.PTTL(ctx, version.key).Result()
	if err != nil {
		t.Fatal(err)
	}
	job.Status = "running"
	if err := op.store.commitEvidence(ctx, []evidenceVersion{version}, job); err != nil {
		t.Fatal(err)
	}
	after, err := client.PTTL(ctx, version.key).Result()
	if err != nil || after > before {
		t.Fatalf("commit extended lifetime: %v", err)
	}
	// Missing identity seed fails closed even if a canonical record survives.
	delete(op.store.client.(*verificationRedis).values, op.store.recordKey("job", original.JobID))
	if err := client.Del(ctx, op.store.recordKey("job", original.JobID)).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := op.store.getEvidenceVersion(context.Background(), "job", original.JobID, &job); !errors.Is(err, errWorkspaceRecordNotFound) {
		t.Fatalf("expired seed resurrected: %v", err)
	}
}
