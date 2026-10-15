package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

var repositoryViewIDPattern = regexp.MustCompile(`^prview_[a-f0-9]{64}$`)

func repositoryViewIdentity(profile publicationProfile, generation string, pulls []openRemediation) string {
	canonical := append([]openRemediation{}, pulls...)
	slices.SortFunc(canonical, func(a, b openRemediation) int { return a.PullRequestNumber - b.PullRequestNumber })
	encoded, _ := json.Marshal(struct {
		SchemaVersion int               `json:"schema_version"`
		ScopeID       string            `json:"scope_id"`
		ProfileSHA256 string            `json:"profile_sha256"`
		Generation    string            `json:"generation"`
		Pulls         []openRemediation `json:"pull_requests"`
	}{1, publicationScopeID(profile), profile.profileSHA256, generation, canonical})
	digest := sha256.Sum256(encoded)
	return "prview_" + hex.EncodeToString(digest[:])
}

func (operations *publicationOperations) scopeGeneration(ctx context.Context, profile publicationProfile) (string, error) {
	if operations.store == nil {
		return "", nil // Read-only composition has no local publication generation.
	}
	state, _, err := operations.store.readScope(ctx, profile)
	return state.Generation, err
}

func (operations *publicationOperations) listOpenRemediations(ctx context.Context, profile publicationProfile) (openRemediations, error) {
	before, err := operations.scopeGeneration(ctx, profile)
	if err != nil {
		return openRemediations{}, err
	}
	listing, err := operations.readOpenRemediations(ctx, profile)
	if err != nil {
		return listing, err
	}
	after, err := operations.scopeGeneration(ctx, profile)
	if err != nil {
		return listing, err
	}
	if before != after {
		return listing, repositoryViewChanged(ctx, listing)
	}
	if listing.Complete {
		listing.RepositoryViewID = repositoryViewIdentity(profile, after, listing.PullRequests)
	}
	telemetry.AddSpanEvent(ctx, "maintenance.publication.repository_view_observed",
		attribute.String("request_id", core.GetRequestID(ctx)), attribute.Bool("maintenance.coverage_complete", listing.Complete),
		attribute.String("maintenance.repository_view_id", listing.RepositoryViewID), attribute.Int("maintenance.open_pr_count", len(listing.PullRequests)))
	return listing, nil
}

func repositoryViewChanged(ctx context.Context, listing openRemediations) error {
	numbers := make([]string, 0, len(listing.PullRequests))
	for _, pull := range listing.PullRequests {
		numbers = append(numbers, strconv.Itoa(pull.PullRequestNumber))
	}
	telemetry.AddSpanEvent(ctx, "maintenance.publication.repository_view_rejected",
		attribute.String("request_id", core.GetRequestID(ctx)), attribute.Int("maintenance.open_pr_count", len(numbers)))
	return &remediationFailure{status: http.StatusConflict, code: "REPOSITORY_VIEW_CHANGED", category: core.CategoryInputError,
		message: "Repository PR state changed since the view supplied for this publication decision. Inspect fresh open-PR evidence and reassess whether new work is warranted. No new PR was created by this attempt.",
		details: map[string]string{"pull_request_numbers": strings.Join(numbers, ","), "observed_at": listing.ObservedAt.Format("2006-01-02T15:04:05Z07:00")}}
}

func (operations *publicationOperations) checkRepositoryView(ctx context.Context, profile publicationProfile, input publishRequest, candidate candidateRecord, reservation publicationRecord) error {
	if !repositoryViewIDPattern.MatchString(input.RepositoryViewID) {
		return rejectRemediation(http.StatusBadRequest, "REPOSITORY_VIEW_REQUIRED", "Supply the exact repository_view_id from complete list_open_remediations evidence used for this publication decision", core.CategoryInputError, false)
	}
	listing, err := operations.listOpenRemediations(ctx, profile)
	if err != nil {
		return err
	}
	if !listing.Complete {
		return rejectRemediation(http.StatusConflict, "REMEDIATION_COVERAGE_INCOMPLETE", "Establish complete open-PR coverage before publication; no new PR was created", core.CategoryInputError, false)
	}
	if listing.RepositoryViewID != input.RepositoryViewID {
		return repositoryViewChanged(ctx, listing)
	}
	return operations.guardDuplicateProductionListing(ctx, profile, candidate, reservation, listing)
}
