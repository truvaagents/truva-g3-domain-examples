package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

// Reconciliation reports external effects, not verification, approval or issue
// applicability. It can release tool-owned coordination but never writes GitHub.
type publicationResolution struct {
	PublicationID     string    `json:"publication_id"`
	CaseID            string    `json:"case_id"`
	Disposition       string    `json:"disposition"`
	PullRequestNumber int       `json:"pull_request_number,omitempty"`
	PullRequestURL    string    `json:"pull_request_url,omitempty"`
	ObservedAt        time.Time `json:"observed_at"`
}

func validPublicationResolution(result publicationResolution) bool {
	if !publicationIDPattern.MatchString(result.PublicationID) || !caseIDPattern.MatchString(result.CaseID) || result.ObservedAt.IsZero() {
		return false
	}
	switch result.Disposition {
	case "abandoned_before_write", "abandoned_before_pr":
		return result.PullRequestNumber == 0 && result.PullRequestURL == ""
	case "observed_open_pr", "observed_closed_pr":
		parsed, err := url.Parse(result.PullRequestURL)
		return err == nil && parsed.Scheme == "https" && parsed.Host != "" && len(result.PullRequestURL) <= 2048 && result.PullRequestNumber > 0
	default:
		return false
	}
}

func unresolvedPublication(pending *pendingPublication) error {
	failure := publicationCoordinationFailure(pending).(*remediationFailure)
	failure.code = "PUBLICATION_OUTCOME_UNRESOLVED"
	failure.message = "The original publication's external outcome is not established. Coordination remains retained. Inspect the original branch/PR and retry reconciliation when authoritative evidence is available. No GitHub writes are performed by reconciliation."
	return failure
}

func (operations *publicationOperations) reconcilePublication(ctx context.Context, profile publicationProfile, input getPublicationRequest) (result publicationResolution, returnedErr error) {
	if operations.store == nil {
		return result, failPublication("publication_not_ready", false, nil)
	}
	state, _, err := operations.store.readScope(ctx, profile)
	if err != nil {
		return result, err
	}
	if state.Pending == nil {
		if state.LastResolution != nil && state.LastResolution.PublicationID == input.PublicationID && state.LastResolution.CaseID == input.CaseID {
			return *state.LastResolution, nil
		}
		return publicationResolution{PublicationID: input.PublicationID, CaseID: input.CaseID, Disposition: "not_pending", ObservedAt: operations.now()}, nil
	}
	if state.Pending.PublicationID != input.PublicationID || state.Pending.CaseID != input.CaseID {
		return result, publicationCoordinationFailure(state.Pending)
	}
	claim, err := operations.store.acquireClaim(ctx, input.PublicationID, publicationLeaseTTL(profile))
	if err != nil {
		if errors.Is(err, errPublicationClaimed) {
			return result, publicationCoordinationFailure(state.Pending)
		}
		return result, err
	}
	ctx = withPublicationOwner(ctx, claim)
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), profile.timeout())
		defer cancel()
		if err := operations.store.releaseScopeOwner(cleanupCtx, profile); err != nil {
			telemetry.RecordSpanError(ctx, err)
			returnedErr = errors.Join(returnedErr, err)
		}
		operations.store.releaseClaim(cleanupCtx, input.PublicationID, claim)
	}()
	// A publication may finish between the first read and claim acquisition.
	// Acquire only an existing pending intent; never recreate one from that read.
	state, raw, err := operations.store.readScope(ctx, profile)
	if err != nil {
		return result, err
	}
	if state.Pending == nil {
		if state.LastResolution != nil && state.LastResolution.PublicationID == input.PublicationID && state.LastResolution.CaseID == input.CaseID {
			return *state.LastResolution, nil
		}
		return publicationResolution{PublicationID: input.PublicationID, CaseID: input.CaseID, Disposition: "not_pending", ObservedAt: operations.now()}, nil
	}
	if state.Pending.PublicationID != input.PublicationID || state.Pending.CaseID != input.CaseID {
		return result, publicationCoordinationFailure(state.Pending)
	}
	if err := operations.store.exchangeScope(ctx, profile, raw, state, true, false); err != nil {
		return result, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), profile.timeout())
		defer cancel()
		if err := operations.store.releaseScopeAttempt(cleanupCtx, profile, input.PublicationID); err != nil {
			telemetry.RecordSpanError(ctx, err)
			returnedErr = errors.Join(returnedErr, err)
		}
	}()
	// Re-read after acquiring ownership: a previous worker may have advanced the
	// intent between our initial observation and the claim transfer.
	state, _, err = operations.store.readScope(ctx, profile)
	if err != nil {
		return result, err
	}
	if state.Pending == nil || state.Pending.PublicationID != input.PublicationID {
		return result, publicationCoordinationFailure(state.Pending)
	}
	pending := *state.Pending
	result = publicationResolution{PublicationID: input.PublicationID, CaseID: input.CaseID, ObservedAt: operations.now()}
	switch pending.Stage {
	case "reserved":
		result.Disposition = "abandoned_before_write"
	case "preparing":
		// New writers persist pr_requested before any PR creation. A lost worker
		// may leave unreachable Git objects or its scoped branch, never a PR.
		result.Disposition = "abandoned_before_pr"
	default:
		if pending.CommitSHA == "" {
			// Legacy mutation_started records predate the durable PR checkpoint.
			// Their expired/missing receipts are uncertainty, never permission.
			saved, err := operations.store.get(ctx, pending.PublicationID)
			if err != nil {
				return result, unresolvedPublication(&pending)
			}
			if saved.PublicationID != pending.PublicationID || saved.CaseID != pending.CaseID || saved.Branch != pending.Branch || saved.BaseSHA != pending.BaseSHA ||
				!strings.EqualFold(saved.RepositoryOwner+"/"+saved.RepositoryName, profile.Owner+"/"+profile.Repository) || saved.BaseRef != profile.BaseRef {
				return result, unresolvedPublication(&pending)
			}
			pending.CommitSHA = saved.CommitSHA
		}
		if operations.github == nil || !gitSHAPattern.MatchString(pending.CommitSHA) {
			return result, unresolvedPublication(&pending)
		}
		ctx = withPublicationKeepAlive(ctx, func(callCtx context.Context) error {
			if err := operations.store.renewClaim(callCtx, input.PublicationID, publicationLeaseTTL(profile)); err != nil {
				return err
			}
			return operations.store.touchScope(callCtx, profile, input.PublicationID)
		})
		pulls, err := operations.github.findPullRequestsWithState(ctx, profile, pending.Branch, "all")
		if err != nil {
			return result, err
		}
		if len(pulls) != 1 {
			return result, unresolvedPublication(&pending)
		}
		pull := pulls[0]
		parsed, parseErr := url.Parse(pull.HTMLURL)
		repository := profile.Owner + "/" + profile.Repository
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || pull.ID <= 0 || pull.Number <= 0 ||
			(pull.State != "open" && pull.State != "closed") || pull.Base.Ref != profile.BaseRef || !strings.EqualFold(pull.Base.Repo.FullName, repository) ||
			pull.Head.Ref != pending.Branch || pull.Head.SHA != pending.CommitSHA || !strings.EqualFold(pull.Head.Repo.FullName, repository) {
			return result, unresolvedPublication(&pending)
		}
		result.Disposition, result.PullRequestNumber, result.PullRequestURL = "observed_"+pull.State+"_pr", pull.Number, pull.HTMLURL
	}
	result.ObservedAt = operations.now()
	if !validPublicationResolution(result) {
		return publicationResolution{}, unresolvedPublication(&pending)
	}
	if err := operations.store.resolveScope(ctx, profile, result); err != nil {
		return publicationResolution{}, err
	}
	telemetry.AddSpanEvent(ctx, "maintenance.publication.reconciled", attribute.String("request_id", core.GetRequestID(ctx)), attribute.String("maintenance.disposition", result.Disposition))
	return result, nil
}

func (store *publicationStore) releaseScopeAttempt(ctx context.Context, profile publicationProfile, publicationID string) error {
	state, raw, err := store.readScope(ctx, profile)
	if err != nil {
		return err
	}
	if state.Pending == nil || state.Pending.PublicationID != publicationID {
		return nil
	}
	return store.exchangeScope(ctx, profile, raw, state, false, true)
}

func (store *publicationStore) resolveScope(ctx context.Context, profile publicationProfile, result publicationResolution) error {
	state, raw, err := store.readScope(ctx, profile)
	if err != nil {
		return err
	}
	if state.Pending == nil || state.Pending.PublicationID != result.PublicationID || state.Pending.CaseID != result.CaseID {
		return publicationCoordinationFailure(state.Pending)
	}
	generation := make([]byte, 32)
	if _, err := rand.Read(generation); err != nil {
		return failPublication("state_encode", false, err)
	}
	state.Generation, state.Pending, state.LastResolution = hex.EncodeToString(generation), nil, &result
	return store.replaceScope(ctx, profile, raw, state)
}

func (tool *maintenancePublicationTool) handleReconcilePublication(writer http.ResponseWriter, request *http.Request) {
	const capability = "reconcile_publication"
	started := time.Now()
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input getPublicationRequest
	if !tool.decode(writer, request, capability, started, 16<<10, &input) {
		return
	}
	profile, ok := tool.authorizeProfile(writer, request, capability, started, caller, input.ProfileID)
	if !ok {
		return
	}
	if !caseIDPattern.MatchString(input.CaseID) || !publicationIDPattern.MatchString(input.PublicationID) {
		tool.fail(writer, request, capability, input.ProfileID, started, http.StatusBadRequest, "INVALID_PUBLICATION_REFERENCE", "Use the original case_id and publication_id returned by the pending publication", core.CategoryInputError, false)
		return
	}
	ctx, cancel := context.WithTimeout(core.WithRequestID(request.Context(), publicationRequestID(request)), profile.timeout())
	defer cancel()
	result, err := tool.operations.reconcilePublication(ctx, profile, input)
	if err != nil {
		tool.failOperation(writer, request, capability, input.ProfileID, started, err)
		return
	}
	tool.succeed(writer, request, capability, input.ProfileID, started, http.StatusOK, result)
}
