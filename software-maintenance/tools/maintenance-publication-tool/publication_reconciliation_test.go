package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

func TestJoinedPublicationFailuresRenderOnce(t *testing.T) {
	tool := &maintenancePublicationTool{BaseTool: core.NewTool(publicationToolName)}
	for _, entry := range []string{"operation", "read"} {
		t.Run(entry, func(t *testing.T) {
			refusal := publicationCoordinationFailure(nil)
			err := errors.Join(refusal, failPublication("state_unavailable", false, errors.New("backend fixture")))
			writer := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/", nil)
			if entry == "operation" {
				tool.failOperation(writer, request, "publish_draft_pr", "fixture", time.Now(), err)
			} else {
				tool.failRemediationRead(writer, request, "list_open_remediations", "fixture", time.Now(), err)
			}
			var result struct {
				Success bool           `json:"success"`
				Error   core.ToolError `json:"error"`
			}
			if err := json.Unmarshal(writer.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if writer.Code != 409 || result.Success || result.Error.Code != "PUBLICATION_COORDINATION_PENDING" || result.Error.Retryable || result.Error.Details["publication_failure"] != "state_unavailable" {
				t.Fatalf("lost combined failure: %d %s", writer.Code, writer.Body.String())
			}
		})
	}
}

func TestPublicationAttemptFencesExpiredOwner(t *testing.T) {
	store, server := scopeStoreFixture(t)
	profile, _ := remediationFixture()
	profile.BaseRef = "main"
	record := publicationRecord{PublicationID: "pub_" + strings.Repeat("a", 64), CaseID: "fixture", Branch: "maintenance/fixture", BaseSHA: strings.Repeat("1", 40)}
	claimA, err := store.acquireClaim(t.Context(), record.PublicationID, publicationLeaseTTL(profile))
	if err != nil {
		t.Fatal(err)
	}
	ctxA := withPublicationOwner(t.Context(), claimA)
	if err := store.beginScope(ctxA, profile, record); err != nil {
		t.Fatal(err)
	}
	server.FastForward(3 * time.Second)
	claimB, err := store.acquireClaim(t.Context(), record.PublicationID, publicationLeaseTTL(profile))
	if err != nil {
		t.Fatal(err)
	}
	ctxB := withPublicationOwner(t.Context(), claimB)
	if err := store.beginScope(ctxB, profile, record); err != nil {
		t.Fatal(err)
	}
	requireRemediationCode(t, store.finishScope(ctxA, profile, record.PublicationID, false), "PUBLICATION_COORDINATION_PENDING")
	if err := store.releaseScopeOwner(ctxA, profile); err != nil {
		t.Fatal(err)
	}
	if err := store.markScopeMutation(ctxB, profile, record.PublicationID); err != nil {
		t.Fatal(err)
	}
	requireRemediationCode(t, store.checkScopeMutation(ctxA, profile, record.PublicationID), "PUBLICATION_COORDINATION_PENDING")
	record.Status = "succeeded"
	if err := store.putClaimed(ctxB, record, time.Hour); err != nil {
		t.Fatal(err)
	}
	record.Status = "failed"
	requireRemediationCode(t, store.putClaimed(ctxA, record, time.Hour), "PUBLICATION_COORDINATION_PENDING")
	saved, err := store.get(t.Context(), record.PublicationID)
	if err != nil || saved.Status != "succeeded" {
		t.Fatalf("stale write corrupted record: %+v %v", saved, err)
	}
	// Individual operations renew both leases, so total publication time may
	// exceed the lease duration without admitting a second worker.
	for i := 0; i < 4; i++ {
		server.FastForward(time.Second)
		if err := store.renewClaim(ctxB, record.PublicationID, publicationLeaseTTL(profile)); err != nil {
			t.Fatal(err)
		}
		if err := store.touchScope(ctxB, profile, record.PublicationID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.acquireClaim(t.Context(), record.PublicationID, time.Minute); !errors.Is(err, errPublicationClaimed) {
		t.Fatalf("active claim lost: %v", err)
	}
}

func TestReconcilePublicationWithoutFreshApprovalOrReceipts(t *testing.T) {
	for _, scenario := range []string{"reserved", "preparing", "open", "closed", "unknown", "changed_head", "legacy_missing", "active", "foreign_case"} {
		t.Run(scenario, func(t *testing.T) {
			store, redisServer := scopeStoreFixture(t)
			profile, _ := remediationFixture()
			profile.BaseRef = "main"
			record := publicationRecord{PublicationID: "pub_" + strings.Repeat("a", 64), CaseID: "fixture", Branch: "maintenance/fixture", BaseSHA: strings.Repeat("1", 40), CommitSHA: strings.Repeat("2", 40)}
			owner, err := store.acquireClaim(t.Context(), record.PublicationID, publicationLeaseTTL(profile))
			if err != nil {
				t.Fatal(err)
			}
			ctx := withPublicationOwner(t.Context(), owner)
			if err := store.beginScope(ctx, profile, record); err != nil {
				t.Fatal(err)
			}
			if scenario != "reserved" {
				if err := store.markScopeMutation(ctx, profile, record.PublicationID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "reserved" && scenario != "preparing" {
				if err := store.markPullRequest(ctx, profile, record); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "legacy_missing" {
				state, raw, err := store.readScope(ctx, profile)
				if err != nil {
					t.Fatal(err)
				}
				state.Pending.Stage, state.Pending.CommitSHA = "mutation_started", ""
				if err := store.replaceScope(ctx, profile, raw, state); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "active" {
				redisServer.FastForward(3 * time.Second)
			}
			var reads, writes atomic.Int32
			github := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes.Add(1)
					w.WriteHeader(500)
					return
				}
				reads.Add(1)
				if r.URL.Query().Get("state") != "all" {
					t.Error("reconciliation omitted closed PRs")
				}
				pulls := []githubPullRequest{}
				if scenario != "unknown" {
					pull := githubPullRequest{ID: 1, Number: 7, HTMLURL: "https://github.invalid/fixture/service/pull/7", State: "open"}
					if scenario == "closed" {
						pull.State = "closed"
					}
					pull.Base.Ref, pull.Base.SHA, pull.Base.Repo.FullName = "main", strings.Repeat("3", 40), "fixture/service"
					pull.Head.Ref, pull.Head.SHA, pull.Head.Repo.FullName = record.Branch, record.CommitSHA, "fixture/service"
					if scenario == "changed_head" {
						pull.Head.SHA = strings.Repeat("4", 40)
					}
					pulls = append(pulls, pull)
				}
				_ = json.NewEncoder(w).Encode(pulls)
			}))
			defer github.Close()
			origin, _ := url.Parse(github.URL)
			op := publicationOperations{store: store, github: &githubPublicationClient{baseURL: origin, httpClient: github.Client()}, now: time.Now}
			input := getPublicationRequest{ProfileID: profile.PublicationProfileID, CaseID: record.CaseID, PublicationID: record.PublicationID}
			if scenario == "foreign_case" {
				input.CaseID = "other"
			}
			result, err := op.reconcilePublication(t.Context(), profile, input)
			blocked := scenario == "unknown" || scenario == "changed_head" || scenario == "legacy_missing" || scenario == "active" || scenario == "foreign_case"
			if blocked {
				code := "PUBLICATION_OUTCOME_UNRESOLVED"
				if scenario == "active" || scenario == "foreign_case" {
					code = "PUBLICATION_COORDINATION_PENDING"
				}
				requireRemediationCode(t, err, code)
			} else {
				want := map[string]string{"reserved": "abandoned_before_write", "preparing": "abandoned_before_pr", "open": "observed_open_pr", "closed": "observed_closed_pr"}[scenario]
				if err != nil || result.Disposition != want {
					t.Fatalf("resolution=%+v error=%v", result, err)
				}
				again, err := op.reconcilePublication(t.Context(), profile, input)
				resultJSON, _ := json.Marshal(result)
				againJSON, _ := json.Marshal(again)
				if err != nil || string(againJSON) != string(resultJSON) {
					t.Fatalf("idempotent resolution changed: %+v %v", again, err)
				}
			}
			state, _, err := store.readScope(t.Context(), profile)
			if err != nil || (state.Pending != nil) != blocked {
				t.Fatalf("incorrect pending state: %+v %v", state, err)
			}
			if writes.Load() != 0 {
				t.Fatal("reconciliation wrote GitHub")
			}
			if (scenario == "reserved" || scenario == "preparing" || scenario == "active" || scenario == "foreign_case" || scenario == "legacy_missing") && reads.Load() != 0 {
				t.Fatal("unexpected GitHub read")
			}
		})
	}
}

func TestPublicationScopeMutationRequiresOwner(t *testing.T) {
	store, _ := scopeStoreFixture(t)
	profile, _ := remediationFixture()
	profile.BaseRef = "main"
	record := publicationRecord{PublicationID: "pub_" + strings.Repeat("a", 64), CaseID: "fixture", Branch: "maintenance/fixture", BaseSHA: strings.Repeat("1", 40)}
	requireRemediationCode(t, store.beginScope(context.Background(), profile, record), "PUBLICATION_COORDINATION_PENDING")
}

type claimInterceptRedis struct {
	redis.UniversalClient
	beforeClaim func()
}

func (client *claimInterceptRedis) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	if client.beforeClaim != nil {
		before := client.beforeClaim
		client.beforeClaim = nil
		before()
	}
	return client.UniversalClient.SetNX(ctx, key, value, expiration)
}

func TestReconcileDoesNotResurrectCompletedIntent(t *testing.T) {
	store, _ := scopeStoreFixture(t)
	profile, _ := remediationFixture()
	profile.BaseRef = "main"
	record := publicationRecord{PublicationID: "pub_" + strings.Repeat("a", 64), CaseID: "fixture", Branch: "maintenance/fixture", BaseSHA: strings.Repeat("1", 40)}
	owner, err := store.acquireClaim(t.Context(), record.PublicationID, publicationLeaseTTL(profile))
	if err != nil {
		t.Fatal(err)
	}
	ctx := withPublicationOwner(t.Context(), owner)
	if err := store.beginScope(ctx, profile, record); err != nil {
		t.Fatal(err)
	}
	var completedGeneration string
	store.client = &claimInterceptRedis{UniversalClient: store.client, beforeClaim: func() {
		if err := store.finishScope(ctx, profile, record.PublicationID, true); err != nil {
			t.Fatal(err)
		}
		store.releaseClaim(ctx, record.PublicationID, owner)
		state, _, err := store.readScope(ctx, profile)
		if err != nil {
			t.Fatal(err)
		}
		completedGeneration = state.Generation
	}}
	op := publicationOperations{store: store, now: time.Now}
	result, err := op.reconcilePublication(t.Context(), profile, getPublicationRequest{PublicationID: record.PublicationID, CaseID: record.CaseID})
	if err != nil || result.Disposition != "not_pending" {
		t.Fatalf("incorrect concurrent completion: %+v %v", result, err)
	}
	state, _, err := store.readScope(t.Context(), profile)
	if err != nil || state.Pending != nil || state.LastResolution != nil || state.Generation != completedGeneration {
		t.Fatalf("completed intent recreated: %+v %v", state, err)
	}
}

// This opt-in probe uses only isolated synthetic Redis keys. It makes no AI or
// GitHub calls, and never changes a real publication's state.
func TestPublicationRecoveryLiveRedis(t *testing.T) {
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
	keyspace, err := core.NewRedisKeyspace("publication-live-recovery-" + time.Now().UTC().Format("20060102t150405.000000000"))
	if err != nil {
		t.Fatal(err)
	}
	store := &publicationStore{client: client, keyspace: keyspace}
	profile, _ := remediationFixture()
	profile.BaseRef = "main"
	record := publicationRecord{PublicationID: "pub_" + strings.Repeat("a", 64), CaseID: "fixture", Branch: "maintenance/fixture", BaseSHA: strings.Repeat("1", 40)}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Each pair shares a hash tag; keep cleanup compatible with Redis Cluster.
		for _, keys := range [][]string{{store.scopeKey(profile), store.scopeLeaseKey(profile)}, {store.recordKey(record.PublicationID), store.claimKey(record.PublicationID)}} {
			if err := client.Del(ctx, keys...).Err(); err != nil {
				t.Errorf("probe cleanup: %v", err)
			}
		}
	})
	claimA, err := store.acquireClaim(t.Context(), record.PublicationID, publicationLeaseTTL(profile))
	if err != nil {
		t.Fatal(err)
	}
	ctxA := withPublicationOwner(t.Context(), claimA)
	if err := store.beginScope(ctxA, profile, record); err != nil {
		t.Fatal(err)
	}
	time.Sleep(publicationLeaseTTL(profile) + 50*time.Millisecond)
	claimB, err := store.acquireClaim(t.Context(), record.PublicationID, publicationLeaseTTL(profile))
	if err != nil {
		t.Fatal(err)
	}
	ctxB := withPublicationOwner(t.Context(), claimB)
	if err := store.beginScope(ctxB, profile, record); err != nil {
		t.Fatal(err)
	}
	requireRemediationCode(t, store.finishScope(ctxA, profile, record.PublicationID, false), "PUBLICATION_COORDINATION_PENDING")
	if err := store.releaseScopeOwner(ctxA, profile); err != nil {
		t.Fatal(err)
	}
	if err := store.markScopeMutation(ctxB, profile, record.PublicationID); err != nil {
		t.Fatal(err)
	}
	record.Status = "branch_created"
	if err := store.putClaimed(ctxB, record, time.Minute); err != nil {
		t.Fatal(err)
	}
	record.Status = "failed"
	requireRemediationCode(t, store.putClaimed(ctxA, record, time.Minute), "PUBLICATION_COORDINATION_PENDING")
	store.releaseClaim(ctxA, record.PublicationID, claimA)
	if _, err := store.acquireClaim(t.Context(), record.PublicationID, time.Minute); !errors.Is(err, errPublicationClaimed) {
		t.Fatalf("stale cleanup removed active claim: %v", err)
	}
	if err := store.finishScope(ctxB, profile, record.PublicationID, false); err != nil {
		t.Fatal(err)
	}
	store.releaseClaim(ctxB, record.PublicationID, claimB)
	// Simulate receipt expiry while durable pre-PR intent remains.
	if err := client.Del(t.Context(), store.recordKey(record.PublicationID)).Err(); err != nil {
		t.Fatal(err)
	}
	op := publicationOperations{store: store, now: time.Now}
	input := getPublicationRequest{ProfileID: profile.PublicationProfileID, CaseID: record.CaseID, PublicationID: record.PublicationID}
	result, err := op.reconcilePublication(t.Context(), profile, input)
	if err != nil || result.Disposition != "abandoned_before_pr" {
		t.Fatalf("reconciliation=%+v error=%v", result, err)
	}
	state, _, err := store.readScope(t.Context(), profile)
	if err != nil || state.Pending != nil || state.Generation == "" || state.LastResolution == nil {
		t.Fatalf("unresolved scope: %+v %v", state, err)
	}
	t.Log("RECOVERY_RESULT stale cleanup and record writes refused; active claim retained; expired-receipt pre-PR intent reconciled without GitHub or approval")
}
