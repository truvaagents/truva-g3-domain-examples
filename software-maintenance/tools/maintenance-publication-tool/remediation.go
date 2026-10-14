package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

// Exactly one reference form is accepted. The caller cannot supply an API URL
// or repository; both are selected by the authenticated, authorized profile.
type remediationRequest struct {
	ProfileID         string `json:"profile_id"`
	PullRequestNumber *int   `json:"pull_request_number,omitempty"`
	PublicationID     string `json:"publication_id,omitempty"`
	OriginCaseID      string `json:"origin_case_id,omitempty"`
}

type remediationState struct {
	SchemaVersion                  int       `json:"schema_version"`
	ProfileID                      string    `json:"profile_id"`
	ProfileSHA256                  string    `json:"profile_sha256"`
	RepositoryID                   string    `json:"repository_id"`
	RepositoryOwner                string    `json:"repository_owner"`
	RepositoryName                 string    `json:"repository_name"`
	PullRequestID                  int64     `json:"pull_request_id"`
	PullRequestNumber              int       `json:"pull_request_number"`
	PullRequestURL                 string    `json:"pull_request_url"`
	State                          string    `json:"state"`
	Draft                          *bool     `json:"draft,omitempty"`
	Merged                         *bool     `json:"merged,omitempty"`
	BaseRef                        string    `json:"base_ref,omitempty"`
	BaseSHA                        string    `json:"base_sha,omitempty"`
	HeadRef                        string    `json:"head_ref,omitempty"`
	HeadSHA                        string    `json:"head_sha,omitempty"`
	HeadRepository                 string    `json:"head_repository,omitempty"`
	MergeSHA                       string    `json:"merge_sha,omitempty"`
	ObservedAt                     time.Time `json:"observed_at"`
	SourceVersion                  string    `json:"source_version,omitempty"`
	ReferenceKind                  string    `json:"reference_kind"`
	OriginPublicationID            string    `json:"origin_publication_id,omitempty"`
	OriginCaseID                   string    `json:"origin_case_id,omitempty"`
	OriginPublicationProfileSHA256 string    `json:"origin_publication_profile_sha256,omitempty"`
	ProfileChangedSincePublication *bool     `json:"profile_changed_since_publication,omitempty"`
	PublishedCommitSHA             string    `json:"published_commit_sha,omitempty"`
	HeadMatchesPublicationCommit   *bool     `json:"head_matches_publication_commit,omitempty"`
	Limitations                    []string  `json:"limitations"`
}

// Pointer fields preserve the distinction between absent and false. In
// particular, closed + absent merged is unknown, not closed-unmerged.
type githubRemediationPR struct {
	Title          string                  `json:"title"`
	Body           string                  `json:"body"`
	ChangedFiles   *int                    `json:"changed_files"`
	ID             int64                   `json:"id"`
	Number         int                     `json:"number"`
	HTMLURL        string                  `json:"html_url"`
	State          string                  `json:"state"`
	Draft          *bool                   `json:"draft"`
	Merged         *bool                   `json:"merged"`
	MergeCommitSHA string                  `json:"merge_commit_sha"`
	Base           githubRemediationBranch `json:"base"`
	Head           githubRemediationBranch `json:"head"`
}

type githubRemediationBranch struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo *struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

type remediationFailure struct {
	status    int
	code      string
	message   string
	category  core.ErrorCategory
	retryable bool
	details   map[string]string
}

func (failure *remediationFailure) Error() string { return failure.code }

func rejectRemediation(status int, code, message string, category core.ErrorCategory, retryable bool) error {
	return &remediationFailure{status: status, code: code, message: message, category: category, retryable: retryable}
}

func (operations *publicationOperations) remediation(ctx context.Context, profile publicationProfile, input remediationRequest) (remediationState, error) {
	var receipt *publicationRecord
	var number int
	if input.PullRequestNumber != nil {
		if *input.PullRequestNumber <= 0 || input.PublicationID != "" || input.OriginCaseID != "" {
			return remediationState{}, invalidRemediationReference()
		}
		number = *input.PullRequestNumber
	} else {
		if !publicationIDPattern.MatchString(input.PublicationID) || !caseIDPattern.MatchString(input.OriginCaseID) {
			return remediationState{}, invalidRemediationReference()
		}
		record, err := operations.remediationPublication(ctx, profile, input)
		if err != nil {
			if errors.Is(err, errPublicationNotFound) {
				return remediationState{}, rejectRemediation(http.StatusNotFound, "REMEDIATION_REFERENCE_NOT_FOUND", "Publication reference was not found or has expired", core.CategoryNotFound, false)
			}
			var failure *publicationFailure
			if errors.As(err, &failure) && (failure.kind == "record_invalid" || failure.kind == "state_invalid") {
				return remediationState{}, rejectRemediation(http.StatusUnprocessableEntity, "REMEDIATION_REFERENCE_INVALID", "Publication reference does not match its original case, repository, profile version, or lifetime", core.CategoryInputError, false)
			}
			return remediationState{}, rejectRemediation(http.StatusServiceUnavailable, "REMEDIATION_STORE_UNAVAILABLE", "Publication reference could not be read", core.CategoryServiceError, true)
		}
		if record.Status != "succeeded" || record.PullRequestNumber <= 0 || record.PullRequestID <= 0 || !gitSHAPattern.MatchString(record.CommitSHA) {
			return remediationState{}, invalidRemediationReference()
		}
		receipt = &record
		number = record.PullRequestNumber
	}
	if operations.github == nil {
		return remediationState{}, rejectRemediation(http.StatusServiceUnavailable, "REMEDIATION_NOT_READY", "Configure the publication tool's GitHub credential to enable repository reads", core.CategoryServiceError, false)
	}
	var pull githubRemediationPR
	var etag string
	status, err := operations.github.requestWithETag(ctx, profile, http.MethodGet,
		operations.github.repositoryEndpoint(profile, "pulls", strconv.Itoa(number)), nil, nil, &pull, &etag)
	if err != nil {
		return remediationState{}, remediationProviderFailure(status, err)
	}
	if status != http.StatusOK {
		return remediationState{}, remediationProviderFailure(status, errors.New("unexpected success status"))
	}
	state, err := observeRemediation(profile, number, pull, operations.now(), etag)
	if err != nil {
		return remediationState{}, err
	}
	if receipt != nil {
		if receipt.PullRequestID != state.PullRequestID {
			return remediationState{}, rejectRemediation(http.StatusBadGateway, "REMEDIATION_IDENTITY_MISMATCH", "GitHub PR identity differs from the publication reference", core.CategoryServiceError, false)
		}
		state.ReferenceKind = "publication_receipt"
		state.OriginPublicationID, state.OriginCaseID = receipt.PublicationID, receipt.CaseID
		state.OriginPublicationProfileSHA256 = receipt.ProfileSHA256
		profileChanged := receipt.ProfileSHA256 != profile.profileSHA256
		state.ProfileChangedSincePublication = &profileChanged
		if profileChanged {
			state.Limitations = append(state.Limitations, "The publication profile changed since this historical publication; its provenance does not establish current applicability or publication authority.")
		}
		state.PublishedCommitSHA = receipt.CommitSHA
		if state.HeadSHA != "" {
			matches := state.HeadSHA == receipt.CommitSHA
			state.HeadMatchesPublicationCommit = &matches
		}
	}
	return state, nil
}

func invalidRemediationReference() error {
	return rejectRemediation(http.StatusBadRequest, "INVALID_REMEDIATION_REFERENCE", "Supply either a positive pull_request_number, or publication_id with its exact origin_case_id, in the authorized profile", core.CategoryInputError, false)
}

func remediationProviderFailure(status int, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return rejectRemediation(http.StatusGatewayTimeout, "REMEDIATION_TIMEOUT", "Fresh GitHub read timed out; current PR state is unavailable", core.CategoryServiceError, true)
	}
	if status == http.StatusNotFound {
		return rejectRemediation(http.StatusNotFound, "REMEDIATION_NOT_FOUND", "GitHub did not expose this PR in the authorized repository; this does not establish closure or resolution", core.CategoryNotFound, false)
	}
	var limited *githubReadRateLimit
	if errors.As(err, &limited) {
		return &remediationFailure{status: http.StatusTooManyRequests, code: "REMEDIATION_RATE_LIMITED",
			message:  "GitHub rate-limited repository reads; retry this reference after the reported deadline. Current PR state is unknown.",
			category: core.CategoryRateLimit, retryable: false, details: limited.details(time.Now())}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return rejectRemediation(http.StatusBadGateway, "REMEDIATION_PROVIDER_ACCESS_DENIED", "GitHub rejected the tool's repository-read credential; current PR state is unavailable", core.CategoryAuthError, false)
	}
	var failure *publicationFailure
	if errors.As(err, &failure) && failure.retryable {
		return rejectRemediation(http.StatusServiceUnavailable, "REMEDIATION_UNAVAILABLE", "Fresh GitHub read is unavailable; current PR state is unknown", core.CategoryServiceError, true)
	}
	return rejectRemediation(http.StatusBadGateway, "REMEDIATION_RESPONSE_INVALID", "GitHub returned an invalid or unsupported PR observation", core.CategoryServiceError, false)
}

func observeRemediation(profile publicationProfile, number int, pull githubRemediationPR, observedAt time.Time, etag string) (remediationState, error) {
	invalid := func() (remediationState, error) {
		return remediationState{}, rejectRemediation(http.StatusBadGateway, "REMEDIATION_RESPONSE_INVALID", "GitHub returned an invalid or mismatched PR observation", core.CategoryServiceError, false)
	}
	parsed, err := url.Parse(pull.HTMLURL)
	expectedPath := "/" + profile.Owner + "/" + profile.Repository + "/pull/" + strconv.Itoa(number)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.EqualFold(parsed.Path, expectedPath) || len(pull.HTMLURL) > 2048 || pull.ID <= 0 || pull.Number != number || pull.Base.Repo == nil || !strings.EqualFold(pull.Base.Repo.FullName, profile.Owner+"/"+profile.Repository) {
		return invalid()
	}
	for _, sha := range []string{pull.Base.SHA, pull.Head.SHA, pull.MergeCommitSHA} {
		if sha != "" && !gitSHAPattern.MatchString(sha) {
			return invalid()
		}
	}
	if len(pull.Base.Ref) > 1024 || len(pull.Head.Ref) > 1024 || pull.Head.Repo != nil && len(pull.Head.Repo.FullName) > 256 {
		return invalid()
	}
	state := remediationState{
		SchemaVersion: 1, ProfileID: profile.PublicationProfileID, ProfileSHA256: profile.profileSHA256,
		RepositoryID: profile.RepositoryID, RepositoryOwner: profile.Owner, RepositoryName: profile.Repository,
		PullRequestID: pull.ID, PullRequestNumber: pull.Number, PullRequestURL: pull.HTMLURL,
		State: "unknown", Draft: pull.Draft, Merged: pull.Merged,
		BaseRef: pull.Base.Ref, BaseSHA: pull.Base.SHA, HeadRef: pull.Head.Ref, HeadSHA: pull.Head.SHA,
		ObservedAt: observedAt.UTC(), SourceVersion: etag, ReferenceKind: "repository_pull_request",
		Limitations: []string{"This fresh GitHub observation does not establish issue applicability, deployed revision, or production resolution."},
	}
	if pull.Head.Repo != nil {
		state.HeadRepository = pull.Head.Repo.FullName
	}
	switch pull.State {
	case "open":
		if pull.Merged != nil && *pull.Merged {
			return invalid()
		}
		state.State = "open"
	case "closed":
		if pull.Merged != nil {
			state.State = "closed_unmerged"
			if *pull.Merged {
				state.State, state.MergeSHA = "merged", pull.MergeCommitSHA
			}
		}
	case "": // Missing state remains unknown.
	default:
		return invalid()
	}
	if state.State == "unknown" || pull.Draft == nil || pull.Merged == nil || pull.Base.SHA == "" || pull.Head.SHA == "" || state.State == "merged" && state.MergeSHA == "" {
		state.Limitations = append(state.Limitations, "Some upstream state, draft, merge, or commit facts were absent; omitted fields remain unknown.")
	}
	return state, nil
}

func (tool *maintenancePublicationTool) handleGetRemediationState(writer http.ResponseWriter, request *http.Request) {
	const capability = "get_remediation_state"
	started := time.Now()
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input remediationRequest
	if !tool.decode(writer, request, capability, started, 16<<10, &input) {
		return
	}
	profile, ok := tool.authorizeProfile(writer, request, capability, started, caller, input.ProfileID)
	if !ok {
		return
	}
	telemetry.AddSpanEvent(request.Context(), "maintenance.remediation.read_requested", attribute.String("request_id", publicationRequestID(request)))
	state, err := tool.operations.remediation(request.Context(), profile, input)
	if err != nil {
		var failure *remediationFailure
		if !errors.As(err, &failure) {
			failure = &remediationFailure{status: http.StatusInternalServerError, code: "REMEDIATION_FAILED", message: "Fresh remediation state could not be established", category: core.CategoryServiceError}
		}
		tool.record(request, capability, profile.PublicationProfileID, "error", failure.code, started)
		setPublicationTraceHeaders(writer, request)
		if failure.code == "REMEDIATION_RATE_LIMITED" {
			writer.Header().Set("Retry-After", failure.details["retry_after"])
			telemetry.SetSpanAttributes(request.Context(), attribute.String("maintenance.remediation.retry_not_before", failure.details["retry_not_before"]))
		}
		writePublicationErrorWithDetails(writer, failure.status, failure.code, failure.message, failure.category, failure.retryable, failure.details)
		return
	}
	telemetry.SetSpanAttributes(request.Context(),
		attribute.String("maintenance.remediation.state", state.State),
		attribute.Int("maintenance.pull_request_number", state.PullRequestNumber),
		attribute.String("maintenance.remediation.reference_kind", state.ReferenceKind),
	)
	tool.succeed(writer, request, capability, profile.PublicationProfileID, started, http.StatusOK, state)
}

func remediationOutputSummary() *core.SchemaSummary {
	return &core.SchemaSummary{RequiredFields: []core.FieldHint{
		{Name: "schema_version", Type: "number"}, {Name: "profile_id", Type: "string"}, {Name: "profile_sha256", Type: "string"},
		{Name: "repository_id", Type: "string"}, {Name: "repository_owner", Type: "string"}, {Name: "repository_name", Type: "string"},
		{Name: "pull_request_id", Type: "number"}, {Name: "pull_request_number", Type: "number"}, {Name: "pull_request_url", Type: "string"},
		{Name: "state", Type: "string", Description: "Fresh observed open, closed_unmerged, merged, or unknown; not an applicability or deployment verdict."},
		{Name: "observed_at", Type: "string"}, {Name: "reference_kind", Type: "string"}, {Name: "limitations", Type: "array"},
	}, OptionalFields: []core.FieldHint{
		{Name: "draft", Type: "boolean"}, {Name: "merged", Type: "boolean"}, {Name: "base_ref", Type: "string"}, {Name: "base_sha", Type: "string"},
		{Name: "head_ref", Type: "string"}, {Name: "head_sha", Type: "string"}, {Name: "head_repository", Type: "string"}, {Name: "merge_sha", Type: "string"},
		{Name: "source_version", Type: "string"}, {Name: "origin_publication_id", Type: "string"}, {Name: "origin_case_id", Type: "string"},
		{Name: "origin_publication_profile_sha256", Type: "string", Description: "Immutable profile hash of the original publication; profile_sha256 identifies the current authorized read profile."},
		{Name: "profile_changed_since_publication", Type: "boolean"},
		{Name: "published_commit_sha", Type: "string"}, {Name: "head_matches_publication_commit", Type: "boolean"},
	}}
}
