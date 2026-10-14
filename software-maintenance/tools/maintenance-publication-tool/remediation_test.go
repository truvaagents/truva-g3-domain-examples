package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

func remediationFixture() (publicationProfile, githubRemediationPR) {
	profile := publicationProfile{
		PublicationProfileID: "publication", RepositoryID: "service", Owner: "fixture", Repository: "service",
		WorkspaceProfileID: "workspace", profileSHA256: strings.Repeat("a", 64),
		Limits: publicationLimits{TimeoutMilliseconds: 1000}, callerSet: map[string]struct{}{"maintenance-agent": {}},
	}
	var pull githubRemediationPR
	_ = json.Unmarshal([]byte(`{"id":42,"number":7,"html_url":"https://github.invalid/fixture/service/pull/7","state":"open","draft":true,"merged":false,"base":{"ref":"main","sha":"`+strings.Repeat("1", 40)+`","repo":{"full_name":"fixture/service"}},"head":{"ref":"repair","sha":"`+strings.Repeat("2", 40)+`","repo":{"full_name":"fork/service"}},"merge_commit_sha":"`+strings.Repeat("3", 40)+`"}`), &pull)
	return profile, pull
}

func TestRemediationObservation(t *testing.T) {
	for _, scenario := range []string{"open", "closed", "merged", "missing_merge", "missing_commits", "missing_draft", "deleted_head_repository", "wrong_repository", "wrong_number", "wrong_id", "invalid_sha", "contradictory_state", "invalid_url", "oversized_ref"} {
		t.Run(scenario, func(t *testing.T) {
			profile, pull := remediationFixture()
			want, invalid := "open", false
			no := false
			yes := true
			switch scenario {
			case "closed":
				pull.State, pull.Merged, want = "closed", &no, "closed_unmerged"
			case "merged":
				pull.State, pull.Merged, want = "closed", &yes, "merged"
			case "missing_merge":
				pull.State, pull.Merged, want = "closed", nil, "unknown"
			case "missing_commits":
				pull.Base.SHA, pull.Head.SHA, pull.MergeCommitSHA = "", "", ""
			case "missing_draft":
				pull.Draft = nil
			case "deleted_head_repository":
				pull.Head.Repo = nil
			case "wrong_repository":
				pull.Base.Repo.FullName, invalid = "other/service", true
			case "wrong_number":
				pull.Number, invalid = 8, true
			case "wrong_id":
				pull.ID, invalid = 0, true
			case "invalid_sha":
				pull.Head.SHA, invalid = "instruction prose", true
			case "contradictory_state":
				pull.Merged, invalid = &yes, true
			case "invalid_url":
				pull.HTMLURL, invalid = "https://github.invalid/other/service/pull/7", true
			case "oversized_ref":
				pull.Head.Ref, invalid = strings.Repeat("x", 1025), true
			}
			now := time.Now().UTC()
			state, err := observeRemediation(profile, 7, pull, now, `W/"etag"`)
			if invalid {
				if err == nil {
					t.Fatal("invalid observation accepted")
				}
				return
			}
			if err != nil || state.State != want || !state.ObservedAt.Equal(now) || state.SourceVersion != `W/"etag"` || state.ReferenceKind != "repository_pull_request" {
				t.Fatalf("incorrect observation: %+v %v", state, err)
			}
			if want != "merged" && state.MergeSHA != "" {
				t.Fatal("test merge commit presented as actual merge")
			}
			if scenario == "missing_draft" && state.Draft != nil || scenario == "missing_merge" && state.Merged != nil {
				t.Fatal("unknown fact replaced with false")
			}
		})
	}
}

func TestRemediationFreshReadAndReceiptIntegrity(t *testing.T) {
	profile, pull := remediationFixture()
	now := time.Now().UTC()
	record := publicationRecord{SchemaVersion: 1, ProfileID: profile.PublicationProfileID, ProfileSHA256: profile.profileSHA256, CaseID: "original-case", RepositoryID: profile.RepositoryID, RepositoryOwner: profile.Owner, RepositoryName: profile.Repository, WorkspaceProfileID: profile.WorkspaceProfileID, CandidateID: "cand_" + strings.Repeat("1", 64), VerificationID: "verify_" + strings.Repeat("2", 64), ReviewID: "review_" + strings.Repeat("3", 64), Status: "succeeded", CommitSHA: pull.Head.SHA, PullRequestID: pull.ID, PullRequestNumber: pull.Number, ExpiresAt: now.Add(time.Hour)}
	record.PublicationID = publicationIdentity(profile, publishRequest{CaseID: record.CaseID, CandidateID: record.CandidateID, VerificationID: record.VerificationID, ReviewID: record.ReviewID})
	keyspace, err := core.NewRedisKeyspace("remediation-fixture")
	if err != nil {
		t.Fatal(err)
	}
	redis := &boundaryRedis{values: make(map[string]string)}
	store := &publicationStore{client: redis, keyspace: keyspace}
	if err := store.put(context.Background(), record, time.Hour); err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(redis.values)
	var reads int
	status := http.StatusOK
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet || r.URL.Path != "/repos/fixture/service/pulls/7" || r.URL.RawQuery != "" {
			t.Errorf("unexpected mutation or scope: %s %s", r.Method, r.URL)
		}
		w.Header().Set("ETag", `"current-version"`)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(pull)
	}))
	defer server.Close()
	client, err := newGitHubPublicationClient(server.URL, strings.Repeat("t", 40))
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient = server.Client()
	operations := &publicationOperations{store: store, github: client, now: func() time.Time { return now }}
	input := remediationRequest{ProfileID: profile.PublicationProfileID, PublicationID: record.PublicationID, OriginCaseID: record.CaseID}
	state, err := operations.remediation(context.Background(), profile, input)
	if err != nil || state.OriginCaseID != record.CaseID || state.OriginPublicationID != record.PublicationID || state.HeadMatchesPublicationCommit == nil || !*state.HeadMatchesPublicationCommit || state.OriginPublicationProfileSHA256 != record.ProfileSHA256 || state.ProfileChangedSincePublication == nil || *state.ProfileChangedSincePublication {
		t.Fatalf("receipt attribution lost: %+v %v", state, err)
	}
	// Every invocation reads again; changed head is an observation, not a new
	// approval or a mutation of the historical publication receipt.
	pull.Head.SHA = strings.Repeat("4", 40)
	state, err = operations.remediation(context.Background(), profile, input)
	if err != nil || state.HeadMatchesPublicationCommit == nil || *state.HeadMatchesPublicationCommit || reads != 2 {
		t.Fatalf("changed head not observed freshly: %+v %v", state, err)
	}
	status = http.StatusServiceUnavailable
	if _, err := operations.remediation(context.Background(), profile, input); err == nil {
		t.Fatal("upstream failure replaced with cached state")
	}
	after, _ := json.Marshal(redis.values)
	if string(after) != string(original) {
		t.Fatal("read changed authoritative store")
	}
	status = http.StatusOK
	changedProfile := profile
	changedProfile.profileSHA256 = strings.Repeat("b", 64)
	changedProfile.WorkspaceProfileID = "new-workspace"
	state, err = operations.remediation(t.Context(), changedProfile, input)
	if err != nil || state.OriginCaseID != record.CaseID || state.OriginPublicationProfileSHA256 != record.ProfileSHA256 || state.ProfileSHA256 != changedProfile.profileSHA256 || state.ProfileChangedSincePublication == nil || !*state.ProfileChangedSincePublication || len(state.Limitations) != 2 {
		t.Fatalf("profile change lost historical attribution or disclosure: %+v %v", state, err)
	}
	// The same old receipt cannot become current publication authority.
	if _, err := operations.get(t.Context(), changedProfile, getPublicationRequest{CaseID: record.CaseID, PublicationID: record.PublicationID}); err == nil {
		t.Fatal("historical read relaxed current-profile publication validation")
	}
	after, _ = json.Marshal(redis.values)
	if string(after) != string(original) {
		t.Fatal("historical read relabelled or renewed the receipt")
	}
	for _, variant := range []string{"expired", "wrong_origin", "wrong_profile_id", "wrong_repository", "wrong_owner", "wrong_repository_id", "tampered_hash", "tampered_candidate", "incomplete_publication", "wrong_pr_identity"} {
		t.Run(variant, func(t *testing.T) {
			p, in := profile, input
			r := record
			switch variant {
			case "expired":
				operations.now = func() time.Time { return record.ExpiresAt.Add(time.Second) }
			case "wrong_origin":
				in.OriginCaseID = "different-case"
			case "wrong_profile_id":
				p.PublicationProfileID = "another-profile"
			case "wrong_repository":
				p.Repository = "another-service"
			case "wrong_owner":
				p.Owner = "another-owner"
			case "wrong_repository_id":
				p.RepositoryID = "another-service"
			case "tampered_hash":
				r.ProfileSHA256 = strings.Repeat("c", 64)
			case "tampered_candidate":
				r.CandidateID = "cand_" + strings.Repeat("4", 64)
			case "incomplete_publication":
				r.Status = "branch_created"
			case "wrong_pr_identity":
				r.PullRequestID++
			}
			if err := store.put(t.Context(), r, time.Hour); err != nil {
				t.Fatal(err)
			}
			before := reads
			_, err := operations.remediation(context.Background(), p, in)
			if err == nil || variant != "wrong_pr_identity" && reads != before {
				t.Fatal("invalid receipt reached provider")
			}
			if variant == "wrong_pr_identity" && reads != before+1 {
				t.Fatal("PR identity was not checked against fresh GitHub evidence")
			}
			operations.now = func() time.Time { return now }
			if err := store.put(t.Context(), record, time.Hour); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRemediationProviderErrorMapping(t *testing.T) {
	for _, scenario := range []struct {
		status int
		err    error
		code   string
		retry  bool
	}{
		{404, failPublication("github_rejected", false, nil), "REMEDIATION_NOT_FOUND", false},
		{403, failPublication("github_rejected", false, nil), "REMEDIATION_PROVIDER_ACCESS_DENIED", false},
		{401, failPublication("github_rejected", false, nil), "REMEDIATION_PROVIDER_ACCESS_DENIED", false},
		{429, failPublication("github_rejected", true, nil), "REMEDIATION_UNAVAILABLE", true},
		{503, failPublication("github_rejected", true, nil), "REMEDIATION_UNAVAILABLE", true},
		{200, failPublication("github_response", false, nil), "REMEDIATION_RESPONSE_INVALID", false},
		{0, failPublication("github_unavailable", true, context.DeadlineExceeded), "REMEDIATION_TIMEOUT", true},
	} {
		var failure *remediationFailure
		if !errors.As(remediationProviderFailure(scenario.status, scenario.err), &failure) || failure.code != scenario.code || failure.retryable != scenario.retry {
			t.Fatalf("wrong classification: %+v", failure)
		}
	}
}

func TestRemediationAuthenticationAndInputBoundary(t *testing.T) {
	profile, _ := remediationFixture()
	auth, err := newPublicationAuthorizer("maintenance-agent", strings.Repeat("s", 40))
	if err != nil {
		t.Fatal(err)
	}
	tool := &maintenancePublicationTool{BaseTool: core.NewTool(publicationToolName), auth: auth, profiles: &publicationProfileStore{profiles: map[string]publicationProfile{profile.PublicationProfileID: profile}}, operations: &publicationOperations{}}
	for _, scenario := range []struct {
		body, token string
		status      int
	}{
		{`{"profile_id":"publication","pull_request_number":7}`, "wrong", 401},
		{`{"profile_id":"other","pull_request_number":7}`, strings.Repeat("s", 40), 404},
		{`{"profile_id":"publication","pull_request_number":7,"url":"https://arbitrary.invalid"}`, strings.Repeat("s", 40), 400},
		{`{"profile_id":"publication","pull_request_number":7,"pull_request_number":8}`, strings.Repeat("s", 40), 400},
		{`{"profile_id":"publication","pull_request_number":0}`, strings.Repeat("s", 40), 400},
		{`{"profile_id":"publication","pull_request_number":7,"origin_case_id":"old"}`, strings.Repeat("s", 40), 400},
		{`{"profile_id":"publication","pull_request_number":null}`, strings.Repeat("s", 40), 400},
		{`{"profile_id":"publication","pull_request_number":7}`, strings.Repeat("s", 40), 503},
	} {
		request := httptest.NewRequest(http.MethodPost, "/capabilities/get_remediation_state", strings.NewReader(scenario.body))
		request.Header.Set("Authorization", "Bearer "+scenario.token)
		response := httptest.NewRecorder()
		tool.handleGetRemediationState(response, request)
		if response.Code != scenario.status {
			t.Fatalf("%s: status=%d body=%s", scenario.body, response.Code, response.Body)
		}
	}
	profile.callerSet = map[string]struct{}{"other-agent": {}}
	tool.profiles.profiles[profile.PublicationProfileID] = profile
	request := httptest.NewRequest(http.MethodPost, "/capabilities/get_remediation_state", strings.NewReader(`{"profile_id":"publication","pull_request_number":7}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 40))
	response := httptest.NewRecorder()
	tool.handleGetRemediationState(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("profile read scope bypassed: %d", response.Code)
	}
}

func TestRemediationReadReadinessIsIndependentOfWrites(t *testing.T) {
	redis := miniredis.RunT(t)
	t.Setenv("REDIS_URL", "redis://"+redis.Addr()+"/0")
	data, err := os.ReadFile("../../profiles/retail-order-publication.json")
	if err != nil {
		t.Fatal(err)
	}
	var profile publicationProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatal(err)
	}
	profile.PublicationReady = false
	data, err = json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "publication-profiles.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAINTENANCE_PUBLICATION_PROFILES_FILE", path)
	t.Setenv("MAINTENANCE_PUBLICATION_COORDINATOR_TOKEN", strings.Repeat("s", 40))
	t.Setenv("MAINTENANCE_PUBLICATION_ENABLE_WRITES", "false")
	t.Setenv("TRUVAG3_REDIS_NAMESPACE", "read-only-test")
	t.Setenv("GITHUB_WRITE_TOKEN", strings.Repeat("t", 40))
	tool, err := newMaintenancePublicationToolFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tool.redisClient.Close() }()
	if tool.operations.github == nil || tool.operations.issuers != nil {
		t.Fatal("read requires write readiness or initializes write dependencies")
	}
	if _, err := tool.operations.publish(context.Background(), profile, publishRequest{}); err == nil {
		t.Fatal("read configuration enabled publication")
	}
	found := false
	for _, capability := range tool.GetCapabilities() {
		if capability.Name == "get_remediation_state" {
			found = capability.InputSummary != nil && capability.OutputSummary != nil
		}
	}
	if !found {
		t.Fatal("fresh reader not discoverable with both schemas")
	}
	t.Setenv("GITHUB_WRITE_TOKEN", "invalid")
	if tool, err := newMaintenancePublicationToolFromEnvironment(); err == nil {
		_ = tool.redisClient.Close()
		t.Fatal("invalid explicit GitHub credential accepted")
	}
	t.Setenv("GITHUB_WRITE_TOKEN", "")
	tool, err = newMaintenancePublicationToolFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tool.redisClient.Close() }()
	if tool.operations.github != nil {
		t.Fatal("missing optional credential became a partially configured client")
	}
}

func TestPublicationRequestCorrelation(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/get_remediation_state", nil)
	request.Header.Set("X-TruvaG3-Request-ID", "header-request")
	if publicationRequestID(request) != "header-request" {
		t.Fatal("downstream header correlation lost")
	}
	request = request.WithContext(core.WithRequestID(request.Context(), "context-request"))
	if publicationRequestID(request) != "context-request" {
		t.Fatal("context correlation lost")
	}
	request = request.WithContext(telemetry.WithBaggage(request.Context(), "request_id", "baggage-request"))
	if publicationRequestID(request) != "baggage-request" {
		t.Fatal("primary baggage correlation not preferred")
	}
	writer := httptest.NewRecorder()
	setPublicationTraceHeaders(writer, request)
	if writer.Header().Get("X-Request-ID") != "baggage-request" {
		t.Fatal("response correlation header differs from log correlation")
	}
}
