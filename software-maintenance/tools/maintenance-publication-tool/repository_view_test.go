package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

func scopeTestContext(t *testing.T) context.Context {
	digest := sha256.Sum256([]byte(t.Name() + os.Getenv("HOSTNAME")))
	return withPublicationOwner(t.Context(), hex.EncodeToString(digest[:]))
}

func scopeStoreFixture(t *testing.T) (*publicationStore, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	keyspace, err := core.NewRedisKeyspace("publication-view-fixture")
	if err != nil {
		t.Fatal(err)
	}
	return &publicationStore{client: client, keyspace: keyspace}, server
}

func requireRemediationCode(t *testing.T, err error, code string) {
	t.Helper()
	var failure *remediationFailure
	if !errors.As(err, &failure) || failure.code != code || failure.retryable {
		t.Fatalf("want non-retryable %s, got %v", code, err)
	}
}

func TestPublicationScopeConcurrentOwnersAndUncertainEffects(t *testing.T) {
	store, server := scopeStoreFixture(t)
	profile, _ := remediationFixture()
	profile.BaseRef = "main"
	start := make(chan struct{})
	results := make(chan publicationRecord, 12)
	failures := make(chan error, 12)
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			record := publicationRecord{PublicationID: "pub_" + strings.Repeat("0123456789ab"[i:i+1], 64), CaseID: "case-fixture", Branch: "maintenance/fixture", BaseSHA: strings.Repeat("1", 40)}
			if err := store.beginScope(scopeTestContext(t), profile, record); err != nil {
				failures <- err
			} else {
				results <- record
			}
		}(i)
	}
	close(start)
	workers.Wait()
	close(results)
	close(failures)
	if len(results) != 1 || len(failures) != 11 {
		t.Fatalf("owners=%d refusals=%d", len(results), len(failures))
	}
	for err := range failures {
		requireRemediationCode(t, err, "PUBLICATION_COORDINATION_PENDING")
	}
	winner := <-results
	if err := store.markScopeMutation(scopeTestContext(t), profile, winner.PublicationID); err != nil {
		t.Fatal(err)
	}
	claim, err := store.acquireClaim(scopeTestContext(t), winner.PublicationID, time.Minute)
	if err != nil || claim == "" {
		t.Fatal(err)
	}
	server.FastForward(2 * time.Minute)
	restarted := &publicationStore{client: store.client, keyspace: store.keyspace}
	if err := restarted.beginScope(scopeTestContext(t), profile, winner); err != nil {
		t.Fatal(err)
	}
	otherProfile := profile
	otherProfile.PublicationProfileID = "other-profile-same-repository"
	other := publicationRecord{PublicationID: "pub_" + strings.Repeat("f", 64), CaseID: "another-case", Branch: "maintenance/other", BaseSHA: strings.Repeat("1", 40)}
	requireRemediationCode(t, restarted.beginScope(scopeTestContext(t), otherProfile, other), "PUBLICATION_COORDINATION_PENDING")
	if err := restarted.finishScope(scopeTestContext(t), profile, winner.PublicationID, false); err != nil {
		t.Fatal(err)
	}
	requireRemediationCode(t, restarted.beginScope(scopeTestContext(t), profile, other), "PUBLICATION_COORDINATION_PENDING")
	if err := restarted.beginScope(scopeTestContext(t), profile, winner); err != nil {
		t.Fatalf("same intent cannot recover: %v", err)
	}
	if err := restarted.finishScope(scopeTestContext(t), profile, winner.PublicationID, true); err != nil {
		t.Fatal(err)
	}
	completed, _, err := restarted.readScope(scopeTestContext(t), profile)
	if err != nil || completed.Pending != nil || completed.Generation == "" {
		t.Fatalf("completion not committed: %+v %v", completed, err)
	}
	if err := restarted.finishScope(scopeTestContext(t), profile, winner.PublicationID, true); err != nil {
		t.Fatal(err)
	}
	again, _, _ := restarted.readScope(scopeTestContext(t), profile)
	if again.Generation != completed.Generation {
		t.Fatal("idempotent retry invalidated later views")
	}
	if err := restarted.beginScope(scopeTestContext(t), profile, other); err != nil {
		t.Fatal(err)
	}
	if err := restarted.finishScope(scopeTestContext(t), profile, winner.PublicationID, true); err != nil {
		t.Fatal(err)
	}
	state, _, _ := restarted.readScope(scopeTestContext(t), profile)
	if state.Pending == nil || state.Pending.PublicationID != other.PublicationID {
		t.Fatal("stale completion cleared another publication")
	}
	// A different base has independent write coordination.
	otherProfile.BaseRef = "release"
	if err := restarted.beginScope(scopeTestContext(t), otherProfile, winner); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationScopeAbortAndCorruption(t *testing.T) {
	store, _ := scopeStoreFixture(t)
	profile, _ := remediationFixture()
	profile.BaseRef = "main"
	record := publicationRecord{PublicationID: "pub_" + strings.Repeat("a", 64), CaseID: "case-fixture", Branch: "maintenance/fixture", BaseSHA: strings.Repeat("1", 40)}
	if err := store.beginScope(scopeTestContext(t), profile, record); err != nil {
		t.Fatal(err)
	}
	requireRemediationCode(t, store.checkScopeMutation(scopeTestContext(t), profile, record.PublicationID), "PUBLICATION_COORDINATION_PENDING")
	if err := store.finishScope(scopeTestContext(t), profile, record.PublicationID, false); err != nil {
		t.Fatal(err)
	}
	state, _, err := store.readScope(scopeTestContext(t), profile)
	if err != nil || state.Pending != nil || state.Generation != "" {
		t.Fatalf("no-effect abort changed repository version: %+v %v", state, err)
	}
	if err := store.client.Set(scopeTestContext(t), store.scopeKey(profile), `{}`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.readScope(scopeTestContext(t), profile); err == nil {
		t.Fatal("corrupt coordination silently reset")
	}
}

func TestRepositoryViewIdentity(t *testing.T) {
	profile, pull := remediationFixture()
	yes := true
	first := openRemediation{PullRequestNumber: pull.Number, PullRequestURL: pull.HTMLURL, Title: "first", Draft: &yes, BaseSHA: pull.Base.SHA, HeadSHA: pull.Head.SHA}
	second := first
	second.PullRequestNumber++
	view := repositoryViewIdentity(profile, "", []openRemediation{first, second})
	if view != repositoryViewIdentity(profile, "", []openRemediation{second, first}) {
		t.Fatal("listing order changed repository identity")
	}
	if view == repositoryViewIdentity(profile, "", []openRemediation{first}) || view == repositoryViewIdentity(profile, strings.Repeat("a", 64), []openRemediation{first, second}) {
		t.Fatal("PR membership or committed local effect ignored")
	}
	for _, change := range []string{"head", "base", "title", "draft", "profile"} {
		changed, policy := first, profile
		switch change {
		case "head":
			changed.HeadSHA = strings.Repeat("4", 40)
		case "base":
			changed.BaseSHA = strings.Repeat("5", 40)
		case "title":
			changed.Title = "different explanation"
		case "draft":
			changed.Draft = nil
		case "profile":
			policy.profileSHA256 = strings.Repeat("b", 64)
		}
		if view == repositoryViewIdentity(policy, "", []openRemediation{changed, second}) {
			t.Fatalf("%s change ignored", change)
		}
	}
}

func TestRepositoryViewRequiresCompleteStableCoverage(t *testing.T) {
	for _, scenario := range []string{"incomplete", "commit_during_read"} {
		t.Run(scenario, func(t *testing.T) {
			store, _ := scopeStoreFixture(t)
			profile, pull := remediationFixture()
			profile.BaseRef = "main"
			limit := 1
			profile.Limits.MaximumOpenPRs = &limit
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("unexpected write")
					w.WriteHeader(500)
					return
				}
				if scenario == "commit_during_read" {
					record := publicationRecord{PublicationID: "pub_" + strings.Repeat("a", 64), CaseID: "fixture", Branch: "maintenance/fixture", BaseSHA: pull.Base.SHA}
					for _, err := range []error{store.beginScope(scopeTestContext(t), profile, record), store.markScopeMutation(scopeTestContext(t), profile, record.PublicationID), store.finishScope(scopeTestContext(t), profile, record.PublicationID, true)} {
						if err != nil {
							t.Error(err)
						}
					}
					_ = json.NewEncoder(w).Encode([]githubRemediationPR{})
					return
				}
				second := pull
				second.Number++
				second.HTMLURL = "https://github.invalid/fixture/service/pull/8"
				_ = json.NewEncoder(w).Encode([]githubRemediationPR{pull, second})
			}))
			defer server.Close()
			origin, _ := url.Parse(server.URL)
			op := publicationOperations{store: store, github: &githubPublicationClient{baseURL: origin, httpClient: server.Client()}, now: func() time.Time { return time.Now().UTC() }}
			view, err := op.listOpenRemediations(scopeTestContext(t), profile)
			if scenario == "commit_during_read" {
				requireRemediationCode(t, err, "REPOSITORY_VIEW_CHANGED")
			} else if err != nil || view.Complete || view.RepositoryViewID != "" {
				t.Fatalf("incomplete inventory issued a publication precondition: %+v %v", view, err)
			}
		})
	}
}

func TestPublicationWriteChecksOwnershipBeforeHTTP(t *testing.T) {
	profile, _ := remediationFixture()
	var writes atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writes.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	origin, _ := url.Parse(server.URL)
	client := &githubPublicationClient{baseURL: origin, httpClient: server.Client()}
	ctx := withPublicationWriteCheck(scopeTestContext(t), func(context.Context) error { return publicationCoordinationFailure(nil) })
	_, err := client.createBlob(ctx, profile, base64.StdEncoding.EncodeToString([]byte("fixture")))
	requireRemediationCode(t, err, "PUBLICATION_COORDINATION_PENDING")
	if writes.Load() != 0 {
		t.Fatal("ownership rejection reached GitHub")
	}
}

func TestRepositoryViewChangedBeforeByteDifferentFix(t *testing.T) {
	store, _ := scopeStoreFixture(t)
	profile, pull := remediationFixture()
	profile.BaseRef, profile.Limits.MaximumBodyBytes = "main", 4096
	pull.Title = "Existing once-per-order discount repair"
	count := 1
	pull.ChangedFiles = &count
	var listed atomic.Bool
	var writes atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			w.WriteHeader(500)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			pulls := []githubRemediationPR{}
			if listed.Load() {
				pulls = append(pulls, pull)
			}
			_ = json.NewEncoder(w).Encode(pulls)
		case strings.HasSuffix(r.URL.Path, "/files"):
			// R21's actual production blob differs from the direct-expression fix.
			_ = json.NewEncoder(w).Encode([]githubPullFile{{Path: "checkout.go", Status: "modified", SHA: "23d0b1aabe95b773fb9150a7f9b85dde79b2062d"}})
		default:
			_ = json.NewEncoder(w).Encode(pull)
		}
	}))
	defer server.Close()
	origin, _ := url.Parse(server.URL)
	op := publicationOperations{store: store, github: &githubPublicationClient{baseURL: origin, httpClient: server.Client()}, now: func() time.Time { return time.Now().UTC() }}
	view, err := op.listOpenRemediations(scopeTestContext(t), profile)
	if err != nil || !repositoryViewIDPattern.MatchString(view.RepositoryViewID) {
		t.Fatal(err)
	}
	listed.Store(true)
	candidate := candidateRecord{RevisionSHA: pull.Base.SHA, ChangeCount: 1, Files: []workspaceFile{{Path: "checkout.go", ContentBase64: base64.StdEncoding.EncodeToString([]byte("package main\n\nfunc checkout(receipt *Receipt) ledger {\n\treturn ledger{Total: receipt.UnitPrice*receipt.Quantity - receipt.Discount, Writes: 1}\n}\n"))}}}
	requireRemediationCode(t, op.checkRepositoryView(scopeTestContext(t), profile, publishRequest{RepositoryViewID: view.RepositoryViewID}, candidate, publicationRecord{}), "REPOSITORY_VIEW_CHANGED")
	fresh, err := op.listOpenRemediations(scopeTestContext(t), profile)
	if err != nil {
		t.Fatal(err)
	}
	// The boundary validates state, not semantic equivalence or file overlap.
	if err := op.checkRepositoryView(scopeTestContext(t), profile, publishRequest{RepositoryViewID: fresh.RepositoryViewID}, candidate, publicationRecord{}); err != nil {
		t.Fatalf("current view rejected distinct bytes in the same file: %v", err)
	}
	requireRemediationCode(t, op.checkRepositoryView(scopeTestContext(t), profile, publishRequest{}, candidate, publicationRecord{}), "REPOSITORY_VIEW_REQUIRED")
	if writes.Load() != 0 {
		t.Fatalf("read/precondition made %d Git writes", writes.Load())
	}
	// Even an upstream listing lag cannot reuse a view from before our own effect.
	record := publicationRecord{PublicationID: "pub_" + strings.Repeat("a", 64), CaseID: "case-fixture", Branch: "maintenance/fixture", BaseSHA: strings.Repeat("1", 40)}
	if err := store.beginScope(scopeTestContext(t), profile, record); err != nil {
		t.Fatal(err)
	}
	if err := store.markScopeMutation(scopeTestContext(t), profile, record.PublicationID); err != nil {
		t.Fatal(err)
	}
	if err := store.finishScope(scopeTestContext(t), profile, record.PublicationID, true); err != nil {
		t.Fatal(err)
	}
	requireRemediationCode(t, op.checkRepositoryView(context.Background(), profile, publishRequest{RepositoryViewID: fresh.RepositoryViewID}, candidate, publicationRecord{}), "REPOSITORY_VIEW_CHANGED")
}

// Explicit no-AI Kind probe. Uses the pod's supported Redis configuration, an
// isolated namespace and synthetic repository identity; no GitHub request occurs.
func TestPublicationScopeLiveRedis(t *testing.T) {
	if os.Getenv("MAINTENANCE_PUBLICATION_VERIFY_LIVE_REDIS") != "1" {
		t.Skip("explicit in-cluster Redis verification only")
	}
	config, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	client, err := core.NewRedisUniversalClient(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	keyspace, err := core.NewRedisKeyspace("publication-live-" + time.Now().UTC().Format("20060102t150405.000000000"))
	if err != nil {
		t.Fatal(err)
	}
	store := &publicationStore{client: client, keyspace: keyspace}
	profile, _ := remediationFixture()
	profile.BaseRef = "main"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Del(ctx, store.scopeKey(profile), store.scopeLeaseKey(profile)).Err(); err != nil {
			t.Errorf("isolated probe cleanup: %v", err)
		}
	})
	start := make(chan struct{})
	results := make(chan publicationRecord, 9)
	failures := make(chan error, 9)
	var workers sync.WaitGroup
	for i := 0; i < 9; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			record := publicationRecord{PublicationID: "pub_" + strings.Repeat("012345678"[i:i+1], 64), CaseID: "live-fixture", Branch: "maintenance/live", BaseSHA: strings.Repeat("1", 40)}
			if err := store.beginScope(scopeTestContext(t), profile, record); err != nil {
				failures <- err
			} else {
				results <- record
			}
		}(i)
	}
	close(start)
	workers.Wait()
	if len(results) != 1 || len(failures) != 8 {
		t.Fatalf("owners=%d refusals=%d", len(results), len(failures))
	}
	close(failures)
	for err := range failures {
		requireRemediationCode(t, err, "PUBLICATION_COORDINATION_PENDING")
	}
	winner := <-results
	if err := store.markScopeMutation(scopeTestContext(t), profile, winner.PublicationID); err != nil {
		t.Fatal(err)
	}
	if ttl, err := client.TTL(scopeTestContext(t), store.scopeKey(profile)).Result(); err != nil || ttl >= 0 {
		t.Fatalf("uncertain intent expires: %v %v", ttl, err)
	}
	other := publicationRecord{PublicationID: "pub_" + strings.Repeat("f", 64), CaseID: "different-case", Branch: "maintenance/other", BaseSHA: strings.Repeat("1", 40)}
	requireRemediationCode(t, store.beginScope(scopeTestContext(t), profile, other), "PUBLICATION_COORDINATION_PENDING")
	if err := store.finishScope(scopeTestContext(t), profile, winner.PublicationID, false); err != nil {
		t.Fatal(err)
	}
	requireRemediationCode(t, store.beginScope(scopeTestContext(t), profile, other), "PUBLICATION_COORDINATION_PENDING")
	if err := store.beginScope(scopeTestContext(t), profile, winner); err != nil {
		t.Fatal(err)
	}
	if err := store.finishScope(scopeTestContext(t), profile, winner.PublicationID, true); err != nil {
		t.Fatal(err)
	}
	state, _, err := store.readScope(scopeTestContext(t), profile)
	if err != nil || state.Pending != nil || state.Generation == "" {
		t.Fatalf("completion: %+v %v", state, err)
	}
	if err := store.beginScope(scopeTestContext(t), profile, other); err != nil {
		t.Fatal(err)
	}
	t.Log("9 simultaneous Redis callers: 1 owner, 8 refusals; uncertain intent retained; completion advanced repository generation; next publication admitted")
}

// Run participants simultaneously in distinct pods with one explicit synthetic
// Redis namespace, then run reconcile in that namespace. This never calls GitHub.
func TestPublicationScopeMultiPodRedis(t *testing.T) {
	mode := os.Getenv("MAINTENANCE_PUBLICATION_VERIFY_MODE")
	if mode != "participant" && mode != "reconcile" {
		t.Skip("explicit multi-pod verification only")
	}
	namespace := os.Getenv("MAINTENANCE_PUBLICATION_VERIFY_NAMESPACE")
	if !strings.HasPrefix(namespace, "publication-live-") {
		t.Fatal("isolated live-probe namespace required")
	}
	keyspace, err := core.NewRedisKeyspace(namespace)
	if err != nil {
		t.Fatal(err)
	}
	config, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	client, err := core.NewRedisUniversalClient(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store := &publicationStore{client: client, keyspace: keyspace}
	profile, _ := remediationFixture()
	profile.BaseRef = "main"
	if mode == "participant" {
		var owners, refused atomic.Int32
		var workers sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 3; i++ {
			workers.Add(1)
			go func(i int) {
				defer workers.Done()
				<-start
				digest := sha256.Sum256([]byte(os.Getenv("HOSTNAME") + ":" + strconv.Itoa(i)))
				record := publicationRecord{PublicationID: "pub_" + hex.EncodeToString(digest[:]), CaseID: "live-" + os.Getenv("HOSTNAME"), Branch: "maintenance/live", BaseSHA: strings.Repeat("1", 40)}
				if err := store.beginScope(scopeTestContext(t), profile, record); err == nil {
					owners.Add(1)
				} else {
					var failure *remediationFailure
					if !errors.As(err, &failure) || failure.code != "PUBLICATION_COORDINATION_PENDING" || failure.retryable {
						t.Errorf("unexpected contention error: %v", err)
						return
					}
					refused.Add(1)
				}
			}(i)
		}
		if value := os.Getenv("MAINTENANCE_PUBLICATION_VERIFY_START"); value != "" {
			at, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			delay := time.Until(time.UnixMilli(at))
			if delay > 30*time.Second {
				t.Fatal("probe start exceeds bounded wait")
			}
			if delay > 0 {
				time.Sleep(delay)
			}
		}
		close(start)
		workers.Wait()
		t.Logf("SCOPE_RESULT owners=%d refusals=%d", owners.Load(), refused.Load())
		return // The shared synthetic intent is retained for the reconciliation probe.
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Del(ctx, store.scopeKey(profile), store.scopeLeaseKey(profile)).Err(); err != nil {
			t.Errorf("probe cleanup: %v", err)
		}
	})
	state, _, err := store.readScope(scopeTestContext(t), profile)
	if err != nil || state.Pending == nil {
		t.Fatalf("missing participant intent: %+v %v", state, err)
	}
	winner := state.Pending.PublicationID
	time.Sleep(publicationLeaseTTL(profile))
	record := publicationRecord{PublicationID: winner, CaseID: state.Pending.CaseID, Branch: state.Pending.Branch, BaseSHA: state.Pending.BaseSHA}
	if err := store.beginScope(scopeTestContext(t), profile, record); err != nil {
		t.Fatal(err)
	}
	if err := store.markScopeMutation(scopeTestContext(t), profile, winner); err != nil {
		t.Fatal(err)
	}
	if err := store.finishScope(scopeTestContext(t), profile, winner, false); err != nil {
		t.Fatal(err)
	}
	other := publicationRecord{PublicationID: "pub_" + strings.Repeat("f", 64), CaseID: "other", Branch: "maintenance/other", BaseSHA: strings.Repeat("1", 40)}
	requireRemediationCode(t, store.beginScope(scopeTestContext(t), profile, other), "PUBLICATION_COORDINATION_PENDING")
	if err := store.beginScope(scopeTestContext(t), profile, record); err != nil {
		t.Fatal(err)
	}
	if err := store.finishScope(scopeTestContext(t), profile, winner, true); err != nil {
		t.Fatal(err)
	}
	if err := store.beginScope(scopeTestContext(t), profile, other); err != nil {
		t.Fatal(err)
	}
	t.Log("SCOPE_RECONCILED uncertain intent blocked another writer; confirmed completion allowed the next writer")
}
