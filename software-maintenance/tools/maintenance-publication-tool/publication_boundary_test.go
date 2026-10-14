package main

import (
	"bufio"
	"context"
	"crypto/sha1" // #nosec G505 -- Git blob wire identity for the local provider fixture.
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type boundaryRedis struct {
	redis.UniversalClient
	values             map[string]string
	failSucceededWrite bool
}

func (r *boundaryRedis) Get(_ context.Context, key string) *redis.StringCmd {
	v, ok := r.values[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(v, nil)
}
func (r *boundaryRedis) Set(_ context.Context, key string, v any, _ time.Duration) *redis.StatusCmd {
	var encoded string
	switch value := v.(type) {
	case string:
		encoded = value
	case []byte:
		encoded = string(value)
	default:
		panic("unexpected fixture value")
	}
	var record publicationRecord
	if r.failSucceededWrite && json.Unmarshal([]byte(encoded), &record) == nil && record.Status == "succeeded" {
		r.failSucceededWrite = false
		return redis.NewStatusResult("", errors.New("fixture: final publication write unavailable"))
	}
	r.values[key] = encoded
	return redis.NewStatusResult("OK", nil)
}
func (r *boundaryRedis) SetNX(ctx context.Context, key string, v any, ttl time.Duration) *redis.BoolCmd {
	if _, ok := r.values[key]; ok {
		return redis.NewBoolResult(false, nil)
	}
	r.Set(ctx, key, v, ttl)
	return redis.NewBoolResult(true, nil)
}
func (r *boundaryRedis) Eval(_ context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	if r.values[keys[0]] == args[0] {
		delete(r.values, keys[0])
		return redis.NewCmdResult(int64(1), nil)
	}
	return redis.NewCmdResult(int64(0), nil)
}

// Run explicitly with a workspace test binary, built from this same tree:
// go test -c -o /private/tmp/<run>/workspace.test ./tools/maintenance-workspace-tool
// MAINTENANCE_WORKSPACE_TEST_BINARY=/private/tmp/<run>/workspace.test go test -run TestPublicationProducerConsumer ./tools/maintenance-publication-tool
// No cross-package imports, real credentials, live GitHub calls or LLM calls.
func TestPublicationProducerConsumer(t *testing.T) {
	binary := os.Getenv("MAINTENANCE_WORKSPACE_TEST_BINARY")
	if binary == "" {
		t.Skip("requires explicit workspace test-binary composition")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestPublicationBundleServer$") // #nosec G204 -- explicit local test binary, no production input
	cmd.Env = []string{"MAINTENANCE_BUNDLE_TEST_SERVER=1"}
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = in.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("workspace fixture: %v", err)
		}
	})
	line, err := bufio.NewReader(out).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var hello struct {
		URL            string    `json:"url"`
		CandidateID    string    `json:"candidate_id"`
		VerificationID string    `json:"verification_id"`
		Now            time.Time `json:"now"`
	}
	if err := json.Unmarshal(line, &hello); err != nil {
		t.Fatalf("workspace fixture handshake: %s: %v", line, err)
	}
	for _, mode := range []string{"success", "final_store_failure", "newer", "wrong_candidate", "wrong_review_identity", "rejected", "expired", "missing", "read_error"} {
		t.Run(mode, func(t *testing.T) {
			request := publishRequest{ProfileID: "publication", CaseID: "case", CandidateID: hello.CandidateID, VerificationID: hello.VerificationID, Title: "Verified scoped repair", Body: "Synthetic receipt-boundary verification"}
			var review reviewReceipt
			reviewer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writePublicationSuccess(w, http.StatusOK, review) }))
			defer reviewer.Close()
			issuers, err := newTrustedCapabilityClient(hello.URL, strings.Repeat("p", 40), reviewer.URL, strings.Repeat("r", 40))
			if err != nil {
				t.Fatal(err)
			}
			bundle, err := issuers.workspaceBundle(ctx, request, 3*time.Second)
			if err != nil {
				t.Fatalf("producer bundle: %v", err)
			}
			profile := publicationProfile{PublicationProfileID: "publication", PublicationReady: true, RepositoryID: bundle.Workspace.RepositoryID, WorkspaceProfileID: bundle.Workspace.ProfileID, ExpectedWorkspaceProfileSHA256: bundle.Workspace.ProfileSHA256, ExpectedSourceProfileSHA256: bundle.Workspace.SourceProfileSHA256, ExpectedReviewProfileSHA256: strings.Repeat("e", 64), Owner: "fixture", Repository: "service", BaseRef: "main", BranchPrefix: "maintenance", profileSHA256: strings.Repeat("f", 64), Limits: publicationLimits{TimeoutMilliseconds: 3000, MaximumFiles: 10, MaximumCandidateBytes: 8192, MaximumTitleBytes: 256, MaximumBodyBytes: 4096, RecordTTLSeconds: 3600, ClaimTTLSeconds: 60}}
			review = reviewReceipt{SchemaVersion: 1, CaseID: request.CaseID, CandidateID: request.CandidateID, TestArtifactID: bundle.Candidate.TestArtifactID, VerificationID: request.VerificationID, WorkspaceProfileSHA256: bundle.Workspace.ProfileSHA256, ReviewProfileSHA256: profile.ExpectedReviewProfileSHA256, Status: "approved", ContractReferences: []string{"fixture-contract"}, EvidenceReferences: []string{request.VerificationID}, ReviewerExecutionID: "fixture-review", CreatedAt: hello.Now.Add(-500 * time.Millisecond), ExpiresAt: hello.Now.Add(time.Hour)}
			switch mode {
			case "rejected":
				review.Status = "rejected"
			case "expired":
				review.ExpiresAt = hello.Now.Add(-time.Second)
			case "wrong_candidate":
				review.CandidateID = "cand_" + strings.Repeat("0", 64)
			}
			review.ReviewID = reviewIdentity(review)
			if mode == "wrong_review_identity" {
				review.ReviewID = "review_" + strings.Repeat("0", 64)
			}
			request.ReviewID = review.ReviewID
			// The approval predates these real bundle reads. A read cannot make
			// the otherwise unchanged approval stale.
			for i := 0; i < 3; i++ {
				again, err := issuers.workspaceBundle(ctx, request, 3*time.Second)
				if err != nil || !again.Verification.UpdatedAt.Equal(bundle.Verification.UpdatedAt) {
					t.Fatalf("repeat %d invalidated approval: %v", i, err)
				}
			}
			if mode == "newer" || mode == "missing" || mode == "read_error" {
				issuers.workspaceEndpoint += "?fixture=" + mode
			}
			var calls atomic.Int32
			var pullCreates atomic.Int32
			var providerMu sync.Mutex
			var createdPull map[string]any
			files := make([]githubPullFile, 0, len(bundle.Candidate.Files))
			for _, file := range bundle.Candidate.Files {
				content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
				if err != nil {
					t.Fatal(err)
				}
				hash := sha1.New() // #nosec G401 -- fixture reproduces Git's blob contract, not a security hash.
				_, _ = fmt.Fprintf(hash, "blob %d\x00", len(content))
				_, _ = hash.Write(content)
				status := "modified"
				if file.Delete {
					status = "removed"
				}
				files = append(files, githubPullFile{Path: file.Path, SHA: fmt.Sprintf("%x", hash.Sum(nil)), Status: status})
			}
			github := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerMu.Lock()
				defer providerMu.Unlock()
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				var response any
				status := http.StatusOK
				switch {
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/git/ref/"):
					response = map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": bundle.Workspace.RevisionSHA}}
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/commits/"):
					response = map[string]any{"sha": bundle.Workspace.RevisionSHA, "commit": map[string]any{"tree": map[string]string{"sha": bundle.Workspace.TreeSHA}}}
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls"):
					response = []any{}
					if createdPull != nil {
						response = []any{createdPull}
					}
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/1/files"):
					response = files
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/1"):
					response = createdPull
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls"):
					status = http.StatusCreated
					pullCreates.Add(1)
					createdPull = map[string]any{"id": 1, "number": 1, "html_url": "https://github.invalid/fixture/service/pull/1", "title": request.Title, "body": request.Body, "changed_files": len(files), "draft": true, "state": "open", "base": map[string]any{"ref": "main", "sha": bundle.Workspace.RevisionSHA, "repo": map[string]string{"full_name": "fixture/service"}}, "head": map[string]any{"ref": publicationBranch(profile, request), "sha": strings.Repeat("9", 40), "repo": map[string]string{"full_name": "fixture/service"}}}
					response = createdPull
				case r.Method == http.MethodPost:
					status = http.StatusCreated
					response = map[string]string{"sha": strings.Repeat("9", 40)}
				default:
					t.Errorf("unexpected GitHub request: %s %s", r.Method, r.URL.Path)
					status = http.StatusInternalServerError
				}
				w.WriteHeader(status)
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer github.Close()
			origin, err := url.Parse(github.URL)
			if err != nil {
				t.Fatal(err)
			}
			op := publicationOperations{issuers: issuers, github: &githubPublicationClient{baseURL: origin, token: strings.Repeat("g", 40), httpClient: github.Client()}, store: &publicationStore{client: &boundaryRedis{values: map[string]string{}, failSucceededWrite: mode == "final_store_failure"}}, now: func() time.Time { return hello.Now }}
			result, err := op.publish(ctx, profile, request)
			if mode == "final_store_failure" {
				var failure *publicationFailure
				if !errors.As(err, &failure) || failure.kind != "state_unavailable" || pullCreates.Load() != 1 {
					t.Fatalf("lost-write fixture did not reach the final boundary: %v, PR creates=%d", err, pullCreates.Load())
				}
				result, err = op.publish(ctx, profile, request)
			}
			if mode == "success" || mode == "final_store_failure" {
				if err != nil || result.Status != "succeeded" || !result.Draft || result.PullRequestNumber != 1 || calls.Load() == 0 {
					t.Fatalf("publication failed: %+v %v calls=%d", result, err, calls.Load())
				}
				if pullCreates.Load() != 1 {
					t.Fatalf("same publication created %d PRs", pullCreates.Load())
				}
			} else if err == nil || calls.Load() != 0 {
				t.Fatalf("unsafe acceptance: %v, GitHub calls=%d", err, calls.Load())
			}
			t.Logf("%s: publication error=%v, stub GitHub calls=%d", mode, err, calls.Load())
		})
	}
}
