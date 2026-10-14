package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Only the used Redis commands are implemented: unexpected I/O fails the test.
type verificationRedis struct {
	redis.UniversalClient
	values    map[string]string
	readError error
}

func newVerificationRedis(t *testing.T) *verificationRedis {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return &verificationRedis{UniversalClient: client, values: map[string]string{}}
}

func (r *verificationRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	value, ok := r.values[key]
	if !ok {
		return r.UniversalClient.Get(ctx, key)
	}
	return redis.NewStringResult(value, nil)
}

func (r *verificationRedis) GetRange(_ context.Context, key string, start, end int64) *redis.StringCmd {
	if r.readError != nil {
		return redis.NewStringResult("", r.readError)
	}
	value := r.values[key]
	if start >= int64(len(value)) {
		return redis.NewStringResult("", nil)
	}
	return redis.NewStringResult(value[start:min(end+1, int64(len(value)))], nil)
}

func (r *verificationRedis) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) *redis.StatusCmd {
	r.values[key] = string(value.([]byte))
	return r.UniversalClient.Set(ctx, key, value, ttl)
}

func verificationReadFixture(t *testing.T) (*workspaceOperations, *verificationRedis, verificationRecord, string) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := newVerificationRedis(t)
	store := &workspaceStore{client: r}
	profile := workspaceProfile{WorkspaceProfileID: "runner", RepositoryID: "repository", VerificationReady: true, profileSHA256: strings.Repeat("a", 64), Limits: workspaceLimits{MaximumFileBytes: 4096, MaximumCandidateBytes: 8192, MaximumCandidateFiles: 4, MaximumJobLogBytes: 4096}}
	w := workspaceRecord{SchemaVersion: 1, ProfileID: profile.WorkspaceProfileID, ProfileSHA256: profile.profileSHA256, SourceProfileSHA256: strings.Repeat("b", 64), CaseID: "case", RepositoryID: profile.RepositoryID, SnapshotID: "src_" + strings.Repeat("c", 64), ArchiveSHA256: strings.Repeat("c", 64), RevisionSHA: strings.Repeat("d", 40), TreeSHA: strings.Repeat("e", 40), ExpiresAt: now.Add(time.Hour)}
	w.WorkspaceID = fmt.Sprintf("ws_%x", sha256.Sum256([]byte(w.ProfileID+"\x00"+w.ProfileSHA256+"\x00"+w.SourceProfileSHA256+"\x00"+w.CaseID+"\x00"+w.SnapshotID)))
	root := t.TempDir()
	baseline := filepath.Join(root, w.WorkspaceID, "baseline")
	if err := os.MkdirAll(baseline, 0700); err != nil {
		t.Fatal(err)
	}
	code, test := "package sample\n", "package sample\nfunc TestExample() {}\n"
	files, total, err := canonicalCandidateFiles(profile, []candidateFile{{Path: "sample.go", BaseSHA256: "absent", ContentText: &code}}, []candidateFile{{Path: "sample_test.go", BaseSHA256: "absent", ContentText: &test}})
	if err != nil {
		t.Fatal(err)
	}
	files, err = resolveCandidateFileModes(baseline, files)
	if err != nil {
		t.Fatal(err)
	}
	cid, tid := candidateIdentity(w.WorkspaceID, "TestExample", files, 1)
	c := candidateRecord{SchemaVersion: 1, CandidateID: cid, TestArtifactID: tid, WorkspaceID: w.WorkspaceID, ProfileID: w.ProfileID, ProfileSHA256: w.ProfileSHA256, CaseID: w.CaseID, RepositoryID: w.RepositoryID, SnapshotID: w.SnapshotID, RevisionSHA: w.RevisionSHA, ExpectedTest: "TestExample", ChangeCount: 1, TestCount: 1, Files: files, CandidateBytes: total, ExpiresAt: w.ExpiresAt}
	v := verificationRecord{SchemaVersion: 1, VerificationID: "verify_" + strings.Repeat("f", 64), CandidateID: cid, ProfileID: w.ProfileID, ProfileSHA256: w.ProfileSHA256, ReproductionJobID: "job_" + strings.Repeat("1", 64), CandidateJobIDs: []string{"job_" + strings.Repeat("2", 64)}, Status: "succeeded", ExpectedTestObserved: true, SuitePassed: true, PublicationEligible: true, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Second)}
	logs := `{"Action":"fail","Test":"TestExample"}` + "\n"
	artifact := artifactRecord{ArtifactID: "art_" + strings.Repeat("3", 64), CaseID: w.CaseID, Bytes: int64(len(logs))}
	exit1, exit0 := int32(1), int32(0)
	put := func(kind, id string, value any) {
		t.Helper()
		if err := store.putMutable(ctx, kind, id, value, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	put("workspace", w.WorkspaceID, w)
	put("candidate", cid, c)
	put("verification", v.VerificationID, v)
	put("job", v.ReproductionJobID, jobRecord{JobID: v.ReproductionJobID, WorkspaceID: w.WorkspaceID, CaseID: w.CaseID, Status: "failed", ExitCode: &exit1, ArtifactID: artifact.ArtifactID})
	put("job", v.CandidateJobIDs[0], jobRecord{JobID: v.CandidateJobIDs[0], WorkspaceID: w.WorkspaceID, Status: "succeeded", ExitCode: &exit0})
	metadata, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	r.values[store.artifactMetadataKey(w.CaseID, artifact.ArtifactID)] = string(metadata)
	contentKey := store.artifactContentKey(artifact.ArtifactID)
	r.values[contentKey] = logs
	operations := &workspaceOperations{root: root, store: store, profiles: &workspaceProfileStore{profiles: map[string]workspaceProfile{profile.WorkspaceProfileID: profile}}, now: func() time.Time { return now }}
	return operations, r, v, contentKey
}

func TestVerificationReadFailurePreservesOutcome(t *testing.T) {
	for _, mode := range []string{"transient", "missing", "short"} {
		t.Run(mode, func(t *testing.T) {
			operations, r, original, key := verificationReadFixture(t)
			logs := r.values[key]
			readErr := errors.New("synthetic backend failure")
			switch mode {
			case "transient":
				r.readError = readErr
			case "missing":
				delete(r.values, key)
			case "short":
				r.values[key] = logs[:len(logs)-1]
			}
			err := operations.refreshVerification(context.Background(), original.VerificationID)
			if err == nil || (mode == "transient" && !errors.Is(err, readErr)) {
				t.Fatalf("read error lost: %v", err)
			}
			var got verificationRecord
			if err := operations.store.get(context.Background(), "verification", original.VerificationID, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, got) {
				t.Fatal("read failure rewrote outcome/timestamp")
			}
			r.readError = nil
			r.values[key] = logs
			if err := operations.refreshVerification(context.Background(), original.VerificationID); err != nil {
				t.Fatal(err)
			}
			if err := operations.store.get(context.Background(), "verification", original.VerificationID, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, got) {
				t.Fatal("recovered identical evidence invalidated approval")
			}
		})
	}
}

func TestArtifactReadRangeValidation(t *testing.T) {
	operations, _, _, _ := verificationReadFixture(t)
	for _, bounds := range [][2]int64{{-1, 1}, {0, 0}, {9999, 1}} {
		if _, _, err := operations.store.getArtifactSlice(context.Background(), "case", "art_"+strings.Repeat("3", 64), bounds[0], bounds[1]); err == nil {
			t.Fatal("invalid range accepted")
		}
	}
}
