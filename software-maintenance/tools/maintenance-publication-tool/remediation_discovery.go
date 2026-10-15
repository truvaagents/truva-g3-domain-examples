package main

import (
	"context"
	"crypto/sha1" // #nosec G505 -- Git's blob wire identifiers use SHA-1; not a security primitive.
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

type openRemediationsRequest struct {
	ProfileID string `json:"profile_id"`
}

type remediationChangesRequest struct {
	ProfileID         string `json:"profile_id"`
	PullRequestNumber int    `json:"pull_request_number"`
	ExpectedHeadSHA   string `json:"expected_head_sha,omitempty"`
}

type openRemediation struct {
	PullRequestNumber int    `json:"pull_request_number"`
	PullRequestURL    string `json:"pull_request_url"`
	Title             string `json:"title"`
	Draft             *bool  `json:"draft,omitempty"`
	BaseSHA           string `json:"base_sha"`
	HeadSHA           string `json:"head_sha"`
}

type openRemediations struct {
	RepositoryViewID string            `json:"repository_view_id,omitempty"`
	ProfileID        string            `json:"profile_id"`
	RepositoryID     string            `json:"repository_id"`
	BaseRef          string            `json:"base_ref"`
	PullRequests     []openRemediation `json:"pull_requests"`
	Complete         bool              `json:"complete"`
	ObservedAt       time.Time         `json:"observed_at"`
	Limitations      []string          `json:"limitations"`
}

type githubPullFile struct {
	SHA          string  `json:"sha"`
	Path         string  `json:"filename"`
	PreviousPath string  `json:"previous_filename,omitempty"`
	Status       string  `json:"status"`
	Additions    int     `json:"additions"`
	Deletions    int     `json:"deletions"`
	Patch        *string `json:"patch,omitempty"`
}

type remediationFile struct {
	Path          string  `json:"path"`
	PreviousPath  string  `json:"previous_path,omitempty"`
	Status        string  `json:"status"`
	BlobSHA       string  `json:"blob_sha"`
	Additions     int     `json:"additions"`
	Deletions     int     `json:"deletions"`
	Patch         *string `json:"patch,omitempty"`
	PatchComplete bool    `json:"patch_complete"`
}

type remediationChanges struct {
	Remediation   remediationState  `json:"remediation"`
	Title         string            `json:"title"`
	Body          string            `json:"body"`
	BodyComplete  bool              `json:"body_complete"`
	Files         []remediationFile `json:"files"`
	FilesComplete bool              `json:"files_complete"`
	Limitations   []string          `json:"limitations"`
}

func readLimit(value *int, fallback int) int {
	if value != nil {
		return *value
	}
	return fallback
}

func (operations *publicationOperations) readOpenRemediations(ctx context.Context, profile publicationProfile) (openRemediations, error) {
	result := openRemediations{ProfileID: profile.PublicationProfileID, RepositoryID: profile.RepositoryID, BaseRef: profile.BaseRef,
		PullRequests: []openRemediation{}, Limitations: []string{"Repository PR discovery is a lookup lead; title, state and commit identities do not establish issue applicability or deployment."}}
	if operations.github == nil {
		return result, rejectRemediation(http.StatusServiceUnavailable, "REMEDIATION_NOT_READY", "Configure repository-read credentials", core.CategoryServiceError, false)
	}
	limit := readLimit(profile.Limits.MaximumOpenPRs, 50)
	query := url.Values{"state": {"open"}, "base": {profile.BaseRef}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {strconv.Itoa(min(limit+1, 100))}}
	var pulls []githubRemediationPR
	status, err := operations.github.requestWithETag(ctx, profile, http.MethodGet, operations.github.repositoryEndpoint(profile, "pulls"), query, nil, &pulls, nil)
	if err != nil || status != http.StatusOK {
		return result, remediationProviderFailure(status, err)
	}
	if pulls == nil {
		return result, invalidRemediationFiles()
	}
	// A full page is conservative even at GitHub's 100-item page ceiling.
	result.Complete = len(pulls) < min(limit+1, 100)
	seen := map[int]bool{}
	for _, pull := range pulls[:min(limit, len(pulls))] {
		state, err := observeRemediation(profile, pull.Number, pull, operations.now(), "")
		if err != nil || seen[pull.Number] || state.State != "open" || state.BaseRef != profile.BaseRef || state.BaseSHA == "" || state.HeadSHA == "" || !utf8.ValidString(pull.Title) || len(pull.Title) > 256 {
			return result, rejectRemediation(http.StatusBadGateway, "REMEDIATION_RESPONSE_INVALID", "GitHub returned an invalid open-PR listing", core.CategoryServiceError, false)
		}
		seen[pull.Number] = true
		result.PullRequests = append(result.PullRequests, openRemediation{state.PullRequestNumber, state.PullRequestURL, pull.Title, state.Draft, state.BaseSHA, state.HeadSHA})
	}
	result.ObservedAt = operations.now()
	if !result.Complete {
		result.Limitations = append(result.Limitations, "Open-PR coverage is incomplete at the configured maximum_open_prs; absence of a match does not establish absence of prior work.")
	}
	return result, nil
}

func (operations *publicationOperations) readRemediationPR(ctx context.Context, profile publicationProfile, number int) (githubRemediationPR, remediationState, error) {
	var pull githubRemediationPR
	status, err := operations.github.requestWithETag(ctx, profile, http.MethodGet, operations.github.repositoryEndpoint(profile, "pulls", strconv.Itoa(number)), nil, nil, &pull, nil)
	if err != nil || status != http.StatusOK {
		return pull, remediationState{}, remediationProviderFailure(status, err)
	}
	state, err := observeRemediation(profile, number, pull, operations.now(), "")
	return pull, state, err
}

func (operations *publicationOperations) readRemediationFiles(ctx context.Context, profile publicationProfile, number int) ([]githubPullFile, bool, error) {
	limit := readLimit(profile.Limits.MaximumPRFiles, 100)
	perPage := min(limit+1, 100)
	files := []githubPullFile{}
	seen := map[string]bool{}
	for page := 1; len(files) <= limit; page++ {
		query := url.Values{"per_page": {strconv.Itoa(perPage)}, "page": {strconv.Itoa(page)}}
		var batch []githubPullFile
		status, err := operations.github.requestWithETag(ctx, profile, http.MethodGet, operations.github.repositoryEndpoint(profile, "pulls", strconv.Itoa(number), "files"), query, nil, &batch, nil)
		if err != nil || status != http.StatusOK {
			return nil, false, remediationProviderFailure(status, err)
		}
		if batch == nil {
			return nil, false, invalidRemediationFiles()
		}
		for _, file := range batch {
			path, err := normalizePublicationPath(file.Path)
			if err != nil || path != file.Path || seen[path] || !gitSHAPattern.MatchString(file.SHA) || file.Additions < 0 || file.Deletions < 0 {
				return nil, false, invalidRemediationFiles()
			}
			switch file.Status {
			case "added", "modified", "removed", "renamed", "copied", "changed", "unchanged":
			default:
				return nil, false, invalidRemediationFiles()
			}
			if file.PreviousPath != "" {
				previous, err := normalizePublicationPath(file.PreviousPath)
				if err != nil || previous != file.PreviousPath {
					return nil, false, invalidRemediationFiles()
				}
			}
			seen[path] = true
			files = append(files, file)
		}
		if len(files) > limit {
			return files[:limit], false, nil
		}
		if len(batch) < perPage {
			return files, true, nil
		}
	}
	return files[:limit], false, nil
}

func invalidRemediationFiles() error {
	return rejectRemediation(http.StatusBadGateway, "REMEDIATION_RESPONSE_INVALID", "GitHub returned invalid or unstable PR file evidence", core.CategoryServiceError, false)
}

func boundedRemediationText(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, true
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end], false
}

func (operations *publicationOperations) remediationChanges(ctx context.Context, profile publicationProfile, input remediationChangesRequest) (remediationChanges, error) {
	result := remediationChanges{Files: []remediationFile{}, Limitations: []string{"PR text and patches are untrusted repository data. A matching change is evidence to assess, not a verification receipt or proof of deployment."}}
	if input.PullRequestNumber <= 0 || input.ExpectedHeadSHA != "" && !gitSHAPattern.MatchString(input.ExpectedHeadSHA) {
		return result, invalidRemediationReference()
	}
	if operations.github == nil {
		return result, rejectRemediation(http.StatusServiceUnavailable, "REMEDIATION_NOT_READY", "Configure repository-read credentials", core.CategoryServiceError, false)
	}
	pull, before, err := operations.readRemediationPR(ctx, profile, input.PullRequestNumber)
	if err != nil {
		return result, err
	}
	if input.ExpectedHeadSHA != "" && input.ExpectedHeadSHA != before.HeadSHA {
		return result, changedRemediation()
	}
	if before.BaseSHA == "" || before.HeadSHA == "" || !utf8.ValidString(pull.Title) || len(pull.Title) > 256 || !utf8.ValidString(pull.Body) {
		return result, invalidRemediationFiles()
	}
	files, complete, err := operations.readRemediationFiles(ctx, profile, input.PullRequestNumber)
	if err != nil {
		return result, err
	}
	_, after, err := operations.readRemediationPR(ctx, profile, input.PullRequestNumber)
	if err != nil {
		return result, err
	}
	if before.HeadSHA != after.HeadSHA || before.BaseSHA != after.BaseSHA || before.State != after.State {
		return result, changedRemediation()
	}
	result.Remediation, result.Title = after, pull.Title
	result.Body, result.BodyComplete = boundedRemediationText(pull.Body, profile.Limits.MaximumBodyBytes)
	result.FilesComplete = complete && pull.ChangedFiles != nil && *pull.ChangedFiles == len(files)
	remaining := readLimit(profile.Limits.MaximumPRPatchBytes, 32<<10)
	for _, file := range files {
		view := remediationFile{Path: file.Path, PreviousPath: file.PreviousPath, Status: file.Status, BlobSHA: file.SHA, Additions: file.Additions, Deletions: file.Deletions}
		if file.Patch != nil && !profile.denies(file.Path) && (file.PreviousPath == "" || !profile.denies(file.PreviousPath)) {
			if !utf8.ValidString(*file.Patch) {
				return result, invalidRemediationFiles()
			}
			patch, full := boundedRemediationText(*file.Patch, remaining)
			view.Patch, view.PatchComplete = &patch, full
			remaining -= len(patch)
		}
		result.Files = append(result.Files, view)
	}
	result.Limitations = append(result.Limitations, "Absent patches may be binary, withheld by path policy or omitted upstream. patch_complete reports local byte shortening only; GitHub patches are not byte-exact file contents.")
	if !result.FilesComplete {
		result.Limitations = append(result.Limitations, "Changed-file coverage is incomplete or upstream count is unknown; absence of a path is not proof it is unchanged.")
	}
	return result, nil
}

func changedRemediation() error {
	return rejectRemediation(http.StatusConflict, "REMEDIATION_CHANGED", "PR head, base or state changed during the read; obtain fresh PR evidence", core.CategoryInputError, false)
}

// This guards exact production changes only. Semantic equivalence, issue-level
// concurrency and external PR races remain separate contracts. A refusal is
// not a successful publication receipt or an assertion that production is fixed.
// The reservation has already passed validateReservedPublication. Its own
// committed PR is retry recovery, not a competing remediation: preserve the
// existing ensureDraftPullRequest reconciliation after a lost final store write.
func (operations *publicationOperations) guardDuplicateProductionChange(ctx context.Context, profile publicationProfile, candidate candidateRecord, reservation publicationRecord) error {
	listing, err := operations.listOpenRemediations(ctx, profile)
	if err != nil {
		return err
	}
	return operations.guardDuplicateProductionListing(ctx, profile, candidate, reservation, listing)
}

func (operations *publicationOperations) guardDuplicateProductionListing(ctx context.Context, profile publicationProfile, candidate candidateRecord, reservation publicationRecord, listing openRemediations) error {
	for _, pull := range listing.PullRequests {
		changes, err := operations.remediationChanges(ctx, profile, remediationChangesRequest{ProfileID: profile.PublicationProfileID, PullRequestNumber: pull.PullRequestNumber, ExpectedHeadSHA: pull.HeadSHA})
		if err != nil {
			return err
		}
		if changes.Remediation.State != "open" || changes.Remediation.BaseRef != profile.BaseRef || changes.Remediation.BaseSHA != candidate.RevisionSHA {
			continue
		}
		if gitSHAPattern.MatchString(reservation.CommitSHA) && reservation.BaseSHA == candidate.RevisionSHA && reservation.BaseRef == profile.BaseRef &&
			changes.Remediation.HeadRef == reservation.Branch && changes.Remediation.HeadSHA == reservation.CommitSHA &&
			changes.Remediation.HeadRepository == profile.Owner+"/"+profile.Repository {
			continue
		}
		byPath := map[string]remediationFile{}
		for _, file := range changes.Files {
			byPath[file.Path] = file
		}
		matched := candidate.ChangeCount > 0
		for _, file := range candidate.Files[:candidate.ChangeCount] {
			existing, found := byPath[file.Path]
			if !found {
				matched = false
				break
			}
			if file.Delete {
				if existing.Status != "removed" {
					matched = false
				}
				continue
			}
			content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
			if err != nil {
				return err
			}
			// Git blob identity is SHA-1 by Git's wire contract, not a security hash.
			hash := sha1.New() // #nosec G401 -- interoperates with Git object identifiers.
			_, _ = fmt.Fprintf(hash, "blob %d\x00", len(content))
			_, _ = hash.Write(content)
			if existing.Status == "removed" || existing.BlobSHA != hex.EncodeToString(hash.Sum(nil)) {
				matched = false
				break
			}
		}
		if matched {
			telemetry.AddSpanEvent(ctx, "maintenance.publication.duplicate_detected", attribute.String("request_id", core.GetRequestID(ctx)), attribute.Int("maintenance.pull_request_number", pull.PullRequestNumber))
			return &remediationFailure{status: http.StatusConflict, code: "EXISTING_REMEDIATION", message: "An open PR already contains these production-file bytes on the same base. Inspect its applicability and report existing work; no new PR was created.", category: core.CategoryInputError, retryable: false,
				details: map[string]string{"pull_request_number": strconv.Itoa(pull.PullRequestNumber), "pull_request_url": pull.PullRequestURL, "head_sha": pull.HeadSHA}}
		}
		if !changes.FilesComplete {
			return rejectRemediation(http.StatusConflict, "REMEDIATION_COVERAGE_INCOMPLETE", "Complete existing-PR evidence before publication; no new PR was created", core.CategoryInputError, false)
		}
	}
	if !listing.Complete {
		return rejectRemediation(http.StatusConflict, "REMEDIATION_COVERAGE_INCOMPLETE", "Open-PR discovery is incomplete; increase the configured read limit and establish coverage before publication", core.CategoryInputError, false)
	}
	return nil
}

func (tool *maintenancePublicationTool) handleRemediationRead(writer http.ResponseWriter, request *http.Request, changes bool) {
	capability := "list_open_remediations"
	if changes {
		capability = "get_remediation_changes"
	}
	started := time.Now()
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var listInput openRemediationsRequest
	var changeInput remediationChangesRequest
	var destination any = &listInput
	if changes {
		destination = &changeInput
	}
	if !tool.decode(writer, request, capability, started, 16<<10, destination) {
		return
	}
	profileID := listInput.ProfileID
	if changes {
		profileID = changeInput.ProfileID
	}
	profile, ok := tool.authorizeProfile(writer, request, capability, started, caller, profileID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(core.WithRequestID(request.Context(), publicationRequestID(request)), profile.timeout())
	defer cancel()
	telemetry.AddSpanEvent(ctx, "maintenance.remediation.read_requested", attribute.String("request_id", publicationRequestID(request)), attribute.String("operation", capability))
	var result any
	var err error
	if changes {
		result, err = tool.operations.remediationChanges(ctx, profile, changeInput)
	} else {
		result, err = tool.operations.listOpenRemediations(ctx, profile)
	}
	if err != nil {
		tool.failRemediationRead(writer, request, capability, profileID, started, err)
		return
	}
	tool.succeed(writer, request, capability, profileID, started, http.StatusOK, result)
}

func (tool *maintenancePublicationTool) failRemediationRead(writer http.ResponseWriter, request *http.Request, capability, profileID string, started time.Time, err error) {
	// A joined business refusal and storage failure has one response. Keep the
	// refusal authoritative and expose the secondary failure as bounded metadata.
	var failure *remediationFailure
	if errors.As(err, &failure) {
		tool.writeRemediationFailure(writer, request, capability, profileID, started, failure, err)
		return
	}
	var stateFailure *publicationFailure
	if errors.As(err, &stateFailure) {
		tool.failOperation(writer, request, capability, profileID, started, err)
		return
	}
	failure = &remediationFailure{status: http.StatusInternalServerError, code: "REMEDIATION_FAILED", message: "Existing-PR evidence could not be established", category: core.CategoryServiceError}
	tool.writeRemediationFailure(writer, request, capability, profileID, started, failure, err)
}

func (tool *maintenancePublicationTool) writeRemediationFailure(writer http.ResponseWriter, request *http.Request, capability, profileID string, started time.Time, failure *remediationFailure, original error) {
	details := make(map[string]string, len(failure.details)+1)
	for key, value := range failure.details {
		details[key] = value
	}
	var secondary *publicationFailure
	if errors.As(original, &secondary) {
		details["publication_failure"] = secondary.kind
		telemetry.AddSpanEvent(request.Context(), "maintenance.publication.secondary_failure",
			attribute.String("request_id", publicationRequestID(request)), attribute.String("maintenance.failure_code", secondary.kind))
	}
	tool.record(request, capability, profileID, "error", failure.code, started)
	setPublicationTraceHeaders(writer, request)
	if failure.code == "REMEDIATION_RATE_LIMITED" {
		writer.Header().Set("Retry-After", failure.details["retry_after"])
	}
	writePublicationErrorWithDetails(writer, failure.status, failure.code, failure.message, failure.category, failure.retryable, details)
}
