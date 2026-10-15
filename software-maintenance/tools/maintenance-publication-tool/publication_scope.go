package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

// One record per repository/base, shared across profiles and replicas. Pending
// intent does not expire with the worker lease: an uncertain GitHub effect must
// be reconciled by the same publication before a different case may write.
type publicationScope struct {
	SchemaVersion  int                    `json:"schema_version"`
	ScopeID        string                 `json:"scope_id"`
	Generation     string                 `json:"generation"`
	Pending        *pendingPublication    `json:"pending,omitempty"`
	LastResolution *publicationResolution `json:"last_resolution,omitempty"`
}

type pendingPublication struct {
	PublicationID string `json:"publication_id"`
	CaseID        string `json:"case_id"`
	Branch        string `json:"branch"`
	BaseSHA       string `json:"base_sha"`
	Stage         string `json:"stage"`
	CommitSHA     string `json:"commit_sha,omitempty"`
}

func validPendingPublication(pending *pendingPublication) bool {
	return pending != nil && publicationIDPattern.MatchString(pending.PublicationID) && pending.CaseID != "" && len(pending.CaseID) <= 256 &&
		utf8.ValidString(pending.CaseID) && !strings.ContainsAny(pending.CaseID, "\x00\r\n") &&
		pending.Branch != "" && len(pending.Branch) <= 1024 && !strings.ContainsAny(pending.Branch, "\x00\r\n") && utf8.ValidString(pending.Branch) &&
		gitSHAPattern.MatchString(pending.BaseSHA) && (pending.CommitSHA == "" || gitSHAPattern.MatchString(pending.CommitSHA)) &&
		(pending.Stage == "reserved" || pending.Stage == "preparing" || pending.Stage == "pr_requested" || pending.Stage == "mutation_started")
}

func publicationScopeID(profile publicationProfile) string {
	digest := sha256.Sum256([]byte(strings.ToLower(profile.Owner+"/"+profile.Repository) + "\x00" + profile.BaseRef))
	return hex.EncodeToString(digest[:])
}

func (store *publicationStore) scopeKey(profile publicationProfile) string {
	return store.keyspace.Tagged(publicationToolName, publicationScopeID(profile), "repository-publication")
}

func (store *publicationStore) scopeLeaseKey(profile publicationProfile) string {
	return store.keyspace.Tagged(publicationToolName, publicationScopeID(profile), "repository-publication-owner")
}

// Both leases use the existing profile setting. Keep a request's bounded HTTP
// operation within its lease; no background renewal loop is needed.
func publicationLeaseTTL(profile publicationProfile) time.Duration {
	return max(profile.claimTTL(), 2*profile.timeout())
}

type publicationOwnerKey struct{}

func withPublicationOwner(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, publicationOwnerKey{}, owner)
}

func publicationOwner(ctx context.Context) string {
	owner, _ := ctx.Value(publicationOwnerKey{}).(string)
	return owner
}

func (store *publicationStore) readScope(ctx context.Context, profile publicationProfile) (publicationScope, string, error) {
	state := publicationScope{SchemaVersion: 1, ScopeID: publicationScopeID(profile)}
	raw, err := store.client.Get(ctx, store.scopeKey(profile)).Result()
	if errors.Is(err, redis.Nil) {
		return state, "", nil
	}
	if err != nil {
		return state, "", failPublication("state_unavailable", false, err)
	}
	var stored publicationScope
	if err := decodePublicationStrictBytes([]byte(raw), &stored); err != nil || stored.SchemaVersion != 1 || stored.ScopeID != publicationScopeID(profile) ||
		stored.Generation != "" && !sha256Pattern.MatchString(stored.Generation) ||
		stored.Pending != nil && !validPendingPublication(stored.Pending) ||
		stored.LastResolution != nil && !validPublicationResolution(*stored.LastResolution) {
		return publicationScope{}, "", failPublication("state_invalid", false, err)
	}
	return stored, raw, nil
}

// Same-slot compare-and-set works in standalone and cluster Redis. JSON is
// decoded in Go, not Lua; generations are opaque strings, never floating numbers.
func (store *publicationStore) replaceScope(ctx context.Context, profile publicationProfile, expected string, state publicationScope) error {
	return store.exchangeScope(ctx, profile, expected, state, false, state.Pending == nil)
}

func (store *publicationStore) exchangeScope(ctx context.Context, profile publicationProfile, expected string, state publicationScope, acquire, release bool) error {
	owner := publicationOwner(ctx)
	if !sha256Pattern.MatchString(owner) {
		return publicationCoordinationFailure(state.Pending)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return failPublication("state_encode", false, err)
	}
	// Both keys share the repository hash tag. Ownership, state CAS, renewal and
	// release are atomic; stale cleanup cannot clear a replacement's reservation.
	const compareAndSet = `local current = redis.call('GET', KEYS[1]); local owner = redis.call('GET', KEYS[2]); if not ((not current and ARGV[1] == '') or current == ARGV[1]) then return 0 end; if owner ~= ARGV[3] and not (ARGV[5] == '1' and not owner) then return 0 end; redis.call('SET', KEYS[1], ARGV[2]); if ARGV[6] == '1' then redis.call('DEL', KEYS[2]) else redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[4]) end; return 1`
	flag := func(value bool) string {
		if value {
			return "1"
		}
		return "0"
	}
	changed, err := store.client.Eval(ctx, compareAndSet, []string{store.scopeKey(profile), store.scopeLeaseKey(profile)}, expected, string(data), owner, publicationLeaseTTL(profile).Milliseconds(), flag(acquire), flag(release)).Int64()
	if err != nil {
		return failPublication("state_unavailable", false, err)
	}
	if changed != 1 {
		return publicationCoordinationFailure(nil)
	}
	if expected == string(data) && !acquire && !release {
		return nil
	} // Lease renewal is not a business transition.
	status := "idle"
	if state.Pending != nil {
		status = state.Pending.Stage
	}
	telemetry.AddSpanEvent(ctx, "maintenance.publication.coordination_updated",
		attribute.String("request_id", core.GetRequestID(ctx)), attribute.String("maintenance.publication_scope", state.ScopeID), attribute.String("maintenance.coordination_status", status))
	return nil
}

func publicationCoordinationFailure(pending *pendingPublication) error {
	details := map[string]string{}
	if pending != nil {
		details["publication_id"], details["origin_case_id"] = pending.PublicationID, pending.CaseID
		details["branch"], details["base_sha"] = pending.Branch, pending.BaseSHA
	}
	return &remediationFailure{status: http.StatusConflict, code: "PUBLICATION_COORDINATION_PENDING", category: core.CategoryInputError,
		message: "Repository publication is pending or changed concurrently. Use reconcile_publication with its original case/publication references to inspect and resolve the recorded intent. The external outcome may be unknown.", details: details}
}

func (store *publicationStore) beginScope(ctx context.Context, profile publicationProfile, record publicationRecord) error {
	state, raw, err := store.readScope(ctx, profile)
	if err != nil {
		return err
	}
	if state.Pending != nil {
		if state.Pending.PublicationID != record.PublicationID || state.Pending.CaseID != record.CaseID || state.Pending.Branch != record.Branch || state.Pending.BaseSHA != record.BaseSHA {
			return publicationCoordinationFailure(state.Pending)
		}
		return store.exchangeScope(ctx, profile, raw, state, true, false)
	}
	state.Pending = &pendingPublication{PublicationID: record.PublicationID, CaseID: record.CaseID, Branch: record.Branch, BaseSHA: record.BaseSHA, Stage: "reserved"}
	state.LastResolution = nil
	if !validPendingPublication(state.Pending) {
		return failPublication("state_invalid", false, nil)
	}
	return store.exchangeScope(ctx, profile, raw, state, true, false)
}

func (store *publicationStore) markScopeMutation(ctx context.Context, profile publicationProfile, publicationID string) error {
	state, raw, err := store.readScope(ctx, profile)
	if err != nil {
		return err
	}
	if state.Pending == nil || state.Pending.PublicationID != publicationID {
		return publicationCoordinationFailure(state.Pending)
	}
	if state.Pending.Stage != "reserved" {
		return store.replaceScope(ctx, profile, raw, state)
	}
	state.Pending.Stage = "preparing"
	return store.replaceScope(ctx, profile, raw, state)
}

func (store *publicationStore) checkScopeMutation(ctx context.Context, profile publicationProfile, publicationID string) error {
	state, raw, err := store.readScope(ctx, profile)
	if err != nil {
		return err
	}
	if state.Pending == nil || state.Pending.PublicationID != publicationID || state.Pending.Stage == "reserved" {
		return publicationCoordinationFailure(state.Pending)
	}
	return store.replaceScope(ctx, profile, raw, state)
}

func (store *publicationStore) markPullRequest(ctx context.Context, profile publicationProfile, record publicationRecord) error {
	state, raw, err := store.readScope(ctx, profile)
	if err != nil {
		return err
	}
	if state.Pending == nil || state.Pending.PublicationID != record.PublicationID || !gitSHAPattern.MatchString(record.CommitSHA) {
		return publicationCoordinationFailure(state.Pending)
	}
	state.Pending.Stage, state.Pending.CommitSHA = "pr_requested", record.CommitSHA
	return store.replaceScope(ctx, profile, raw, state)
}

func (store *publicationStore) finishScope(ctx context.Context, profile publicationProfile, publicationID string, completed bool) error {
	state, raw, err := store.readScope(ctx, profile)
	if err != nil {
		return err
	}
	if state.Pending == nil || state.Pending.PublicationID != publicationID {
		return nil // Idempotent completion cannot release another publication.
	}
	if !completed && state.Pending.Stage != "reserved" {
		telemetry.AddSpanEvent(ctx, "maintenance.publication.coordination_retained",
			attribute.String("request_id", core.GetRequestID(ctx)), attribute.String("maintenance.publication_id", publicationID))
		return store.exchangeScope(ctx, profile, raw, state, false, true) // Retain intent, release this attempt only.
	}
	if completed {
		generation := make([]byte, 32)
		if _, err := rand.Read(generation); err != nil {
			return failPublication("state_encode", false, err)
		}
		state.Generation = hex.EncodeToString(generation)
	}
	state.Pending = nil
	return store.replaceScope(ctx, profile, raw, state)
}

type publicationWriteCheckKey struct{}
type publicationKeepAliveKey struct{}

func (store *publicationStore) releaseScopeOwner(ctx context.Context, profile publicationProfile) error {
	const script = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end; return 0`
	if err := store.client.Eval(ctx, script, []string{store.scopeLeaseKey(profile)}, publicationOwner(ctx)).Err(); err != nil {
		return failPublication("state_unavailable", false, err)
	}
	return nil
}

func withPublicationKeepAlive(ctx context.Context, check func(context.Context) error) context.Context {
	return context.WithValue(ctx, publicationKeepAliveKey{}, check)
}

func keepPublicationAlive(ctx context.Context) error {
	if check, ok := ctx.Value(publicationKeepAliveKey{}).(func(context.Context) error); ok {
		return check(ctx)
	}
	return nil
}

func (store *publicationStore) touchScope(ctx context.Context, profile publicationProfile, publicationID string) error {
	state, raw, err := store.readScope(ctx, profile)
	if err != nil {
		return err
	}
	if state.Pending == nil || state.Pending.PublicationID != publicationID {
		return publicationCoordinationFailure(state.Pending)
	}
	return store.replaceScope(ctx, profile, raw, state)
}

func withPublicationWriteCheck(ctx context.Context, check func(context.Context) error) context.Context {
	return context.WithValue(ctx, publicationWriteCheckKey{}, check)
}

func checkPublicationWrite(ctx context.Context) error {
	if check, ok := ctx.Value(publicationWriteCheckKey{}).(func(context.Context) error); ok {
		return check(ctx)
	}
	return nil
}
