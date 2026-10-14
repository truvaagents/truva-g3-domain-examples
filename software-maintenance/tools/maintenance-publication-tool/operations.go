package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type publicationOperations struct {
	profiles *publicationProfileStore
	store    *publicationStore
	issuers  *trustedCapabilityClient
	github   *githubPublicationClient
	now      func() time.Time
}

func (operations *publicationOperations) publish(ctx context.Context, profile publicationProfile, request publishRequest) (publicationRecord, error) {
	if !profile.PublicationReady || operations.issuers == nil || operations.github == nil {
		return publicationRecord{}, failPublication("publication_not_ready", false, nil)
	}
	title, body, err := validatePublicationText(profile, request.Title, request.Body)
	if err != nil {
		return publicationRecord{}, err
	}
	bundle, err := operations.issuers.workspaceBundle(ctx, request, profile.timeout())
	if err != nil {
		return publicationRecord{}, err
	}
	if err := validateWorkspaceBundle(profile, request, bundle, operations.now()); err != nil {
		return publicationRecord{}, err
	}
	review, err := operations.issuers.reviewReceipt(ctx, request, profile.timeout())
	if err != nil {
		return publicationRecord{}, err
	}
	if err := validateReviewReceipt(profile, request, bundle, review, operations.now()); err != nil {
		return publicationRecord{}, err
	}

	publicationID := publicationIdentity(profile, request)
	titleDigest := sha256.Sum256([]byte(title))
	bodyDigest := sha256.Sum256([]byte(body))
	now := operations.now()
	record := publicationRecord{
		SchemaVersion: 1, PublicationID: publicationID, ProfileID: profile.PublicationProfileID, ProfileSHA256: profile.profileSHA256,
		CaseID: request.CaseID, RepositoryID: profile.RepositoryID, RepositoryOwner: profile.Owner, RepositoryName: profile.Repository,
		WorkspaceID: bundle.Workspace.WorkspaceID, WorkspaceProfileID: bundle.Workspace.ProfileID,
		WorkspaceProfileSHA256: bundle.Workspace.ProfileSHA256, SourceProfileSHA256: bundle.Workspace.SourceProfileSHA256,
		ReviewProfileSHA256: review.ReviewProfileSHA256,
		SnapshotID:          bundle.Workspace.SnapshotID, CandidateID: request.CandidateID,
		TestArtifactID: bundle.Candidate.TestArtifactID, VerificationID: request.VerificationID, ReviewID: request.ReviewID,
		TitleSHA256: hex.EncodeToString(titleDigest[:]), BodySHA256: hex.EncodeToString(bodyDigest[:]),
		BaseRef: profile.BaseRef, BaseSHA: bundle.Workspace.RevisionSHA, BaseTreeSHA: bundle.Workspace.TreeSHA,
		Branch: publicationBranch(profile, request), Draft: true, Status: "reserved",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(profile.recordTTL()),
		Limitations: append([]string(nil), profile.Limitations...),
	}
	record, err = operations.store.reserve(ctx, record, profile.recordTTL())
	if err != nil {
		return publicationRecord{}, err
	}
	if err := validateReservedPublication(record, profile, request, bundle, titleDigest, bodyDigest, operations.now()); err != nil {
		return publicationRecord{}, err
	}
	if record.Status == "succeeded" {
		return record, nil
	}
	claim, err := operations.store.acquireClaim(ctx, publicationID, profile.claimTTL())
	if err != nil {
		if errors.Is(err, errPublicationClaimed) {
			return publicationRecord{}, failPublication("publication_claimed", true, err)
		}
		return publicationRecord{}, err
	}
	defer operations.store.releaseClaim(context.WithoutCancel(ctx), publicationID, claim)

	record, err = operations.store.get(ctx, publicationID)
	if err != nil {
		return publicationRecord{}, err
	}
	if record.Status == "succeeded" {
		return record, nil
	}
	fail := func(operationErr error) (publicationRecord, error) {
		var failure *publicationFailure
		var readFailure *remediationFailure
		if errors.As(operationErr, &failure) {
			record.FailureCode = failure.kind
		} else if errors.As(operationErr, &readFailure) {
			record.FailureCode = readFailure.code
		} else {
			record.FailureCode = "publication_failed"
		}
		record.Status = "failed"
		record.UpdatedAt = operations.now()
		_ = operations.store.put(context.WithoutCancel(ctx), record, publicationTTL(operations.now(), record.ExpiresAt))
		return publicationRecord{}, operationErr
	}

	head, err := operations.github.getBranchHead(ctx, profile, profile.BaseRef)
	if err != nil {
		return fail(err)
	}
	if head != record.BaseSHA {
		return fail(failPublication("base_ref_changed", false, nil))
	}
	commit, err := operations.github.getCommit(ctx, profile, record.BaseSHA)
	if err != nil {
		return fail(err)
	}
	if commit.Commit.Tree.SHA != record.BaseTreeSHA {
		return fail(failPublication("github_base_mismatch", false, nil))
	}
	checkCtx, cancel := context.WithTimeout(ctx, profile.timeout())
	checkErr := operations.guardDuplicateProductionChange(checkCtx, profile, bundle.Candidate, record)
	cancel()
	if checkErr != nil {
		// Missing evidence is a publication blocker. Retry with the same evidence
		// later, rather than inviting executor-local parameter substitution.
		var failure *remediationFailure
		if errors.As(checkErr, &failure) {
			copy := *failure
			copy.retryable = false
			checkErr = &copy
		}
		return fail(checkErr)
	}

	entries := make([]githubTreeEntry, 0, len(bundle.Candidate.Files))
	for _, file := range bundle.Candidate.Files {
		entry := githubTreeEntry{Path: file.Path, Mode: file.Mode, Type: "blob"}
		if file.Delete {
			entry.Mode = "100644"
			entry.SHA = nil
		} else {
			blobSHA, createErr := operations.github.createBlob(ctx, profile, file.ContentBase64)
			if createErr != nil {
				return fail(createErr)
			}
			entry.SHA = &blobSHA
		}
		entries = append(entries, entry)
	}
	treeSHA, err := operations.github.createTree(ctx, profile, record.BaseTreeSHA, entries)
	if err != nil {
		return fail(err)
	}
	record.CandidateTreeSHA = treeSHA
	record.Status = "tree_created"
	record.FailureCode = ""
	record.UpdatedAt = operations.now()
	if err := operations.store.put(ctx, record, publicationTTL(operations.now(), record.ExpiresAt)); err != nil {
		return publicationRecord{}, err
	}

	// The agent owns the explanation; the validated, reservation-bound PR title
	// also names the commit. Full receipt references preserve traceability.
	message := fmt.Sprintf("%s\n\nMaintenance-Case: %s\nMaintenance-Candidate: %s", title, request.CaseID, request.CandidateID)
	commitSHA, err := operations.github.createCommit(ctx, profile, message, treeSHA, record.BaseSHA, bundle.Candidate.CreatedAt)
	if err != nil {
		return fail(err)
	}
	record.CommitSHA = commitSHA
	record.Status = "commit_created"
	record.UpdatedAt = operations.now()
	if err := operations.store.put(ctx, record, publicationTTL(operations.now(), record.ExpiresAt)); err != nil {
		return publicationRecord{}, err
	}
	if err := operations.github.ensureBranch(ctx, profile, record.Branch, commitSHA); err != nil {
		return fail(err)
	}
	record.Status = "branch_created"
	record.UpdatedAt = operations.now()
	if err := operations.store.put(ctx, record, publicationTTL(operations.now(), record.ExpiresAt)); err != nil {
		return publicationRecord{}, err
	}
	pull, err := operations.github.ensureDraftPullRequest(ctx, profile, record.Branch, record.BaseSHA, commitSHA, title, body)
	if err != nil {
		return fail(err)
	}
	record.PullRequestID = pull.ID
	record.PullRequestNumber = pull.Number
	record.PullRequestURL = pull.HTMLURL
	record.Draft = pull.Draft
	record.Status = "succeeded"
	record.FailureCode = ""
	record.UpdatedAt = operations.now()
	if err := operations.store.put(ctx, record, publicationTTL(operations.now(), record.ExpiresAt)); err != nil {
		return publicationRecord{}, err
	}
	return record, nil
}

func (operations *publicationOperations) get(ctx context.Context, profile publicationProfile, request getPublicationRequest) (publicationRecord, error) {
	if !caseIDPattern.MatchString(request.CaseID) || !publicationIDPattern.MatchString(request.PublicationID) {
		return publicationRecord{}, failPublication("invalid_reference", false, nil)
	}
	record, err := operations.store.get(ctx, request.PublicationID)
	if err != nil {
		return publicationRecord{}, err
	}
	identityRequest := publishRequest{CaseID: record.CaseID, CandidateID: record.CandidateID, VerificationID: record.VerificationID, ReviewID: record.ReviewID}
	if record.SchemaVersion != 1 || record.PublicationID != request.PublicationID || record.PublicationID != publicationIdentity(profile, identityRequest) || record.ProfileID != profile.PublicationProfileID || record.ProfileSHA256 != profile.profileSHA256 || record.CaseID != request.CaseID || record.RepositoryID != profile.RepositoryID || record.RepositoryOwner != profile.Owner || record.RepositoryName != profile.Repository || record.WorkspaceProfileID != profile.WorkspaceProfileID || record.ExpiresAt.Before(operations.now()) {
		return publicationRecord{}, failPublication("record_invalid", false, nil)
	}
	return record, nil
}

func validatePublicationText(profile publicationProfile, title, body string) (string, string, error) {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" || body == "" || len(title) > profile.Limits.MaximumTitleBytes || len(body) > profile.Limits.MaximumBodyBytes || !utf8.ValidString(title) || !utf8.ValidString(body) || strings.ContainsAny(title, "\r\n") {
		return "", "", failPublication("invalid_explanation", false, nil)
	}
	for _, value := range []string{title, body} {
		for _, char := range value {
			if char == 0 || char == 0x7f || char < 0x20 && char != '\n' && char != '\r' && char != '\t' {
				return "", "", failPublication("invalid_explanation", false, nil)
			}
		}
	}
	return title, body, nil
}

func validateWorkspaceBundle(profile publicationProfile, request publishRequest, bundle workspaceBundle, now time.Time) error {
	workspace := bundle.Workspace
	candidate := bundle.Candidate
	verification := bundle.Verification
	workspaceDigest := sha256.Sum256([]byte(workspace.ProfileID + "\x00" + workspace.ProfileSHA256 + "\x00" + workspace.SourceProfileSHA256 + "\x00" + workspace.CaseID + "\x00" + workspace.SnapshotID))
	if workspace.SchemaVersion != 1 || !workspaceIDPattern.MatchString(workspace.WorkspaceID) || workspace.WorkspaceID != "ws_"+hex.EncodeToString(workspaceDigest[:]) || workspace.ProfileID != profile.WorkspaceProfileID || workspace.ProfileSHA256 != profile.ExpectedWorkspaceProfileSHA256 || workspace.SourceProfileSHA256 != profile.ExpectedSourceProfileSHA256 || workspace.CaseID != request.CaseID || workspace.RepositoryID != profile.RepositoryID || !snapshotIDPattern.MatchString(workspace.SnapshotID) || !gitSHAPattern.MatchString(workspace.RevisionSHA) || !gitSHAPattern.MatchString(workspace.TreeSHA) || !sha256Pattern.MatchString(workspace.ArchiveSHA256) || workspace.SnapshotID != "src_"+workspace.ArchiveSHA256 || !workspace.VerificationReady || workspace.ExpiresAt.Before(now) {
		return failPublication("workspace_receipt_invalid", false, nil)
	}
	if candidate.SchemaVersion != 1 || candidate.CandidateID != request.CandidateID || candidate.WorkspaceID != workspace.WorkspaceID || candidate.ProfileID != workspace.ProfileID || candidate.ProfileSHA256 != workspace.ProfileSHA256 || candidate.CaseID != request.CaseID || candidate.RepositoryID != workspace.RepositoryID || candidate.SnapshotID != workspace.SnapshotID || candidate.RevisionSHA != workspace.RevisionSHA || !testArtifactIDPattern.MatchString(candidate.TestArtifactID) || !goTestPattern.MatchString(candidate.ExpectedTest) || candidate.ChangeCount < 1 || candidate.TestCount < 1 || len(candidate.Files) != candidate.ChangeCount+candidate.TestCount || len(candidate.Files) > profile.Limits.MaximumFiles || candidate.CandidateBytes < 0 || candidate.CandidateBytes > profile.Limits.MaximumCandidateBytes || candidate.CreatedAt.Before(workspace.CreatedAt) || candidate.ExpiresAt.Before(now) {
		return failPublication("candidate_receipt_invalid", false, nil)
	}
	canonical, total, err := canonicalPublicationFiles(profile, candidate.Files, candidate.ChangeCount)
	if err != nil || total != candidate.CandidateBytes {
		return failPublication("candidate_content_invalid", false, err)
	}
	expectedCandidate, expectedTest := publicationCandidateIdentity(workspace.WorkspaceID, candidate.ExpectedTest, canonical, candidate.ChangeCount)
	if expectedCandidate != candidate.CandidateID || expectedTest != candidate.TestArtifactID {
		return failPublication("candidate_identity_mismatch", false, nil)
	}
	verificationDigest := sha256.Sum256([]byte(candidate.CandidateID + "\x00" + workspace.ProfileID + "\x00" + workspace.ProfileSHA256 + "\x00verification-v1"))
	if verification.SchemaVersion != 1 || verification.VerificationID != request.VerificationID || verification.VerificationID != "verify_"+hex.EncodeToString(verificationDigest[:]) || verification.CandidateID != candidate.CandidateID || verification.ProfileID != workspace.ProfileID || verification.ProfileSHA256 != workspace.ProfileSHA256 || verification.Status != "succeeded" || !verification.ExpectedTestObserved || !verification.SuitePassed || !verification.PublicationEligible || verification.CreatedAt.Before(candidate.CreatedAt) || verification.UpdatedAt.Before(verification.CreatedAt) {
		return failPublication("verification_receipt_invalid", false, nil)
	}
	if err := validateVerificationJobs(bundle, now); err != nil {
		return err
	}
	return nil
}

func canonicalPublicationFiles(profile publicationProfile, files []workspaceFile, changeCount int) ([]workspaceFile, int64, error) {
	if changeCount < 1 || changeCount >= len(files) {
		return nil, 0, errors.New("invalid candidate partition")
	}
	result := append([]workspaceFile(nil), files...)
	seen := make(map[string]struct{}, len(result))
	var total int64
	for index, file := range result {
		normalized, err := normalizePublicationPath(file.Path)
		isTest := index >= changeCount
		if err != nil || normalized != file.Path || profile.denies(file.Path) || isTest != strings.HasSuffix(file.Path, "_test.go") || isTest && file.Delete || file.BaseSHA256 != "absent" && !sha256Pattern.MatchString(file.BaseSHA256) {
			return nil, 0, errors.New("candidate path or operation is invalid")
		}
		if _, exists := seen[file.Path]; exists {
			return nil, 0, errors.New("candidate paths are not unique")
		}
		seen[file.Path] = struct{}{}
		if file.Delete {
			if file.ContentBase64 != "" || file.Mode != "" || file.BaseSHA256 == "absent" {
				return nil, 0, errors.New("candidate deletion is invalid")
			}
		} else {
			if file.Mode != "100644" && file.Mode != "100755" {
				return nil, 0, errors.New("candidate file mode is invalid")
			}
			content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
			if err != nil || base64.StdEncoding.EncodeToString(content) != file.ContentBase64 || bytes.IndexByte(content, 0) >= 0 {
				return nil, 0, errors.New("candidate content is invalid")
			}
			total += int64(len(content))
			if total > profile.Limits.MaximumCandidateBytes {
				return nil, 0, errors.New("candidate bytes exceed publication policy")
			}
		}
	}
	if !sort.SliceIsSorted(result[:changeCount], func(i, j int) bool { return result[i].Path < result[j].Path }) || !sort.SliceIsSorted(result[changeCount:], func(i, j int) bool { return result[changeCount+i].Path < result[changeCount+j].Path }) {
		return nil, 0, errors.New("candidate files are not canonical")
	}
	return result, total, nil
}

func publicationCandidateIdentity(workspaceID, expectedTest string, files []workspaceFile, changeCount int) (string, string) {
	digest := sha256.New()
	_, _ = io.WriteString(digest, workspaceID+"\x00"+expectedTest+"\x00")
	for _, file := range files {
		encoded, _ := json.Marshal(file)
		_, _ = digest.Write(encoded)
		_, _ = digest.Write([]byte{0})
	}
	tests := sha256.New()
	for _, file := range files[changeCount:] {
		encoded, _ := json.Marshal(file)
		_, _ = tests.Write(encoded)
		_, _ = tests.Write([]byte{0})
	}
	return "cand_" + hex.EncodeToString(digest.Sum(nil)), "test_" + hex.EncodeToString(tests.Sum(nil))
}

func validateVerificationJobs(bundle workspaceBundle, now time.Time) error {
	verification := bundle.Verification
	expected := append([]string{verification.ReproductionJobID}, verification.CandidateJobIDs...)
	if !jobIDPattern.MatchString(verification.ReproductionJobID) || len(verification.CandidateJobIDs) == 0 || len(bundle.Jobs) != len(expected) {
		return failPublication("verification_jobs_invalid", false, nil)
	}
	seen := make(map[string]struct{}, len(expected))
	for index, job := range bundle.Jobs {
		if index >= len(expected) || job.JobID != expected[index] || !jobIDPattern.MatchString(job.JobID) {
			return failPublication("verification_jobs_invalid", false, nil)
		}
		if _, exists := seen[job.JobID]; exists {
			return failPublication("verification_jobs_invalid", false, nil)
		}
		seen[job.JobID] = struct{}{}
		idempotency := "verification-candidate-" + job.CommandID
		expectedTarget := "candidate"
		if index == 0 {
			idempotency = "verification-reproduction"
			expectedTarget = "reproduction"
		}
		identity := sha256.Sum256([]byte(bundle.Workspace.WorkspaceID + "\x00" + bundle.Candidate.CandidateID + "\x00" + verification.VerificationID + "\x00" + expectedTarget + "\x00" + job.CommandID + "\x00" + idempotency))
		validCommon := job.SchemaVersion == 1 && job.JobID == "job_"+hex.EncodeToString(identity[:]) && job.WorkspaceID == bundle.Workspace.WorkspaceID && job.CandidateID == bundle.Candidate.CandidateID && job.VerificationID == verification.VerificationID && job.ProfileID == bundle.Workspace.ProfileID && job.ProfileSHA256 == bundle.Workspace.ProfileSHA256 && job.CaseID == bundle.Workspace.CaseID && job.Target == expectedTarget && publicationNamePattern.MatchString(job.CommandID) && sha256Pattern.MatchString(job.CommandSHA256) && runnerDigestPattern.MatchString(job.RunnerImage) && !job.ArtifactTruncated && !job.TimedOut && artifactIDPattern.MatchString(job.ArtifactID) && sha256Pattern.MatchString(job.ArtifactSHA256) && job.ArtifactID == "art_"+job.ArtifactSHA256 && job.ArtifactBytes > 0 && job.ExpiresAt.After(now)
		if !validCommon {
			return failPublication("verification_jobs_invalid", false, nil)
		}
		if index == 0 {
			if job.Status != "failed" || job.ExitCode == nil || *job.ExitCode == 0 {
				return failPublication("reproduction_job_invalid", false, nil)
			}
		} else if job.Status != "succeeded" || job.ExitCode == nil || *job.ExitCode != 0 {
			return failPublication("candidate_job_invalid", false, nil)
		}
	}
	return nil
}

func validateReviewReceipt(profile publicationProfile, request publishRequest, bundle workspaceBundle, review reviewReceipt, now time.Time) error {
	if review.SchemaVersion != 1 || review.ReviewID != request.ReviewID || review.CaseID != request.CaseID || review.CandidateID != request.CandidateID || review.TestArtifactID != bundle.Candidate.TestArtifactID || review.VerificationID != request.VerificationID || review.WorkspaceProfileSHA256 != bundle.Workspace.ProfileSHA256 || review.ReviewProfileSHA256 != profile.ExpectedReviewProfileSHA256 || review.Status != "approved" || len(review.BlockingFindings) != 0 || len(review.ContractReferences) == 0 || len(review.ContractReferences) > 32 || len(review.EvidenceReferences) == 0 || len(review.EvidenceReferences) > 64 || !validReferenceSet(review.ContractReferences) || !validReferenceSet(review.EvidenceReferences) || review.ReviewerExecutionID == "" || len(review.ReviewerExecutionID) > 128 || review.CreatedAt.Before(bundle.Verification.UpdatedAt) || review.ExpiresAt.Before(now) {
		return failPublication("review_receipt_invalid", false, nil)
	}
	expected := reviewIdentity(review)
	if review.ReviewID != expected {
		return failPublication("review_identity_mismatch", false, nil)
	}
	return nil
}

func validReferenceSet(values []string) bool {
	previous := ""
	for _, value := range values {
		if value == "" || len(value) > 256 || value <= previous || strings.TrimSpace(value) != value {
			return false
		}
		for _, char := range value {
			if char < 0x20 || char == 0x7f {
				return false
			}
		}
		previous = value
	}
	return true
}

func reviewIdentity(receipt reviewReceipt) string {
	identity := struct {
		SchemaVersion          int       `json:"schema_version"`
		CaseID                 string    `json:"case_id"`
		CandidateID            string    `json:"candidate_id"`
		TestArtifactID         string    `json:"test_artifact_id"`
		VerificationID         string    `json:"verification_id"`
		WorkspaceProfileSHA256 string    `json:"workspace_profile_sha256"`
		ReviewProfileSHA256    string    `json:"review_profile_sha256"`
		Status                 string    `json:"status"`
		ContractReferences     []string  `json:"contract_references"`
		EvidenceReferences     []string  `json:"evidence_references"`
		BlockingFindings       []string  `json:"blocking_findings"`
		ReviewerExecutionID    string    `json:"reviewer_execution_id"`
		CreatedAt              time.Time `json:"created_at"`
		ExpiresAt              time.Time `json:"expires_at"`
	}{
		SchemaVersion: receipt.SchemaVersion, CaseID: receipt.CaseID, CandidateID: receipt.CandidateID,
		TestArtifactID: receipt.TestArtifactID, VerificationID: receipt.VerificationID,
		WorkspaceProfileSHA256: receipt.WorkspaceProfileSHA256, Status: receipt.Status,
		ReviewProfileSHA256: receipt.ReviewProfileSHA256,
		ContractReferences:  receipt.ContractReferences, EvidenceReferences: receipt.EvidenceReferences,
		BlockingFindings: receipt.BlockingFindings, ReviewerExecutionID: receipt.ReviewerExecutionID,
		CreatedAt: receipt.CreatedAt, ExpiresAt: receipt.ExpiresAt,
	}
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(append([]byte("maintenance-review-v1\x00"), encoded...))
	return "review_" + hex.EncodeToString(digest[:])
}

func publicationIdentity(profile publicationProfile, request publishRequest) string {
	digest := sha256.Sum256([]byte(profile.PublicationProfileID + "\x00" + profile.profileSHA256 + "\x00" + request.CaseID + "\x00" + request.CandidateID + "\x00" + request.VerificationID + "\x00" + request.ReviewID))
	return "pub_" + hex.EncodeToString(digest[:])
}

func publicationBranch(profile publicationProfile, request publishRequest) string {
	return profile.BranchPrefix + "/" + branchComponent(request.CaseID, 40) + "-" + request.CandidateID[5:17]
}

func branchComponent(value string, limit int) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		result = "case"
	}
	if len(result) > limit {
		result = strings.TrimRight(result[:limit], "-")
	}
	return result
}

func validateReservedPublication(record publicationRecord, profile publicationProfile, request publishRequest, bundle workspaceBundle, titleDigest, bodyDigest [sha256.Size]byte, now time.Time) error {
	if record.SchemaVersion != 1 || record.PublicationID != publicationIdentity(profile, request) || record.ProfileID != profile.PublicationProfileID || record.ProfileSHA256 != profile.profileSHA256 || record.CaseID != request.CaseID || record.RepositoryID != profile.RepositoryID || record.RepositoryOwner != profile.Owner || record.RepositoryName != profile.Repository || record.WorkspaceID != bundle.Workspace.WorkspaceID || record.WorkspaceProfileID != profile.WorkspaceProfileID || record.WorkspaceProfileSHA256 != bundle.Workspace.ProfileSHA256 || record.SourceProfileSHA256 != bundle.Workspace.SourceProfileSHA256 || record.ReviewProfileSHA256 != profile.ExpectedReviewProfileSHA256 || record.SnapshotID != bundle.Workspace.SnapshotID || record.CandidateID != request.CandidateID || record.TestArtifactID != bundle.Candidate.TestArtifactID || record.VerificationID != request.VerificationID || record.ReviewID != request.ReviewID || record.TitleSHA256 != hex.EncodeToString(titleDigest[:]) || record.BodySHA256 != hex.EncodeToString(bodyDigest[:]) || record.BaseRef != profile.BaseRef || record.BaseSHA != bundle.Workspace.RevisionSHA || record.BaseTreeSHA != bundle.Workspace.TreeSHA || record.Branch != publicationBranch(profile, request) || !record.Draft || record.ExpiresAt.Before(now) {
		return failPublication("publication_reservation_conflict", false, nil)
	}
	return nil
}

func publicationTTL(now, expiry time.Time) time.Duration {
	remaining := expiry.Sub(now)
	if remaining <= 0 {
		return time.Second
	}
	return remaining
}
