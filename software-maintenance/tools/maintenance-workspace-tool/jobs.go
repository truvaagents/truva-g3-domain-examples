package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

func (operations *workspaceOperations) runExperiment(ctx context.Context, request runExperimentRequest) (jobRecord, error) {
	if !caseIDPattern.MatchString(request.CaseID) || !caseIDPattern.MatchString(request.IdempotencyKey) {
		return jobRecord{}, errors.New("idempotency key is invalid")
	}
	workspace, profile, err := operations.loadWorkspace(ctx, request.WorkspaceID)
	if err != nil {
		return jobRecord{}, err
	}
	if request.CaseID != workspace.CaseID {
		return jobRecord{}, errors.New("case scope does not match the workspace")
	}
	if !profile.VerificationReady {
		return jobRecord{}, errors.New("workspace runner profile is not ready for execution")
	}
	command, ok := profile.commandByID[request.CommandID]
	if !ok {
		return jobRecord{}, errors.New("experiment command is not allowlisted")
	}
	subPath := workspace.WorkspaceID + "/baseline"
	candidateID := ""
	switch request.Target {
	case "baseline":
		if request.CandidateID != "" {
			return jobRecord{}, errors.New("baseline experiments do not accept a candidate")
		}
	case "reproduction", "candidate":
		candidate, candidateWorkspace, candidateProfile, loadErr := operations.loadCandidate(ctx, request.CandidateID)
		if loadErr != nil || candidateWorkspace.WorkspaceID != workspace.WorkspaceID || candidateProfile.WorkspaceProfileID != profile.WorkspaceProfileID {
			return jobRecord{}, errors.New("candidate does not belong to the workspace")
		}
		candidateID = candidate.CandidateID
		subPath = workspace.WorkspaceID + "/" + candidate.CandidateID + "-" + request.Target
	default:
		return jobRecord{}, errors.New("experiment target is invalid")
	}
	return operations.submitJob(ctx, workspace, profile, candidateID, "", request.Target, request.CommandID, request.IdempotencyKey, subPath, command)
}

func (operations *workspaceOperations) runVerification(ctx context.Context, candidateID string) (verificationRecord, error) {
	candidate, workspace, profile, err := operations.loadCandidate(ctx, candidateID)
	if err != nil {
		return verificationRecord{}, err
	}
	if !profile.VerificationReady {
		return verificationRecord{}, errors.New("workspace runner profile is not ready for verification")
	}
	digest := sha256.Sum256([]byte(candidate.CandidateID + "\x00" + profile.WorkspaceProfileID + "\x00" + profile.profileSHA256 + "\x00verification-v1"))
	verificationID := "verify_" + hex.EncodeToString(digest[:])
	var existing verificationRecord
	if err := operations.store.get(ctx, "verification", verificationID, &existing); err == nil {
		if err := operations.refreshVerification(ctx, verificationID); err != nil {
			return verificationRecord{}, err
		}
		if err := operations.store.get(ctx, "verification", verificationID, &existing); err != nil {
			return verificationRecord{}, err
		}
		return existing, nil
	} else if !errors.Is(err, errWorkspaceRecordNotFound) {
		return verificationRecord{}, err
	}
	createdAt := operations.now()
	record := verificationRecord{
		SchemaVersion: 1, VerificationID: verificationID, CandidateID: candidate.CandidateID,
		ProfileID: profile.WorkspaceProfileID, ProfileSHA256: profile.profileSHA256,
		Status: "pending", CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	reproductionCommand := profile.commandByID[profile.ReproductionCommandID]
	reproduction, err := operations.submitJob(ctx, workspace, profile, candidate.CandidateID, verificationID, "reproduction", reproductionCommand.ID, "verification-reproduction", workspace.WorkspaceID+"/"+candidate.CandidateID+"-reproduction", reproductionCommand)
	if err != nil {
		return verificationRecord{}, err
	}
	record.ReproductionJobID = reproduction.JobID
	for _, commandID := range profile.VerificationCommandIDs {
		command := profile.commandByID[commandID]
		job, err := operations.submitJob(ctx, workspace, profile, candidate.CandidateID, verificationID, "candidate", commandID, "verification-candidate-"+commandID, workspace.WorkspaceID+"/"+candidate.CandidateID+"-candidate", command)
		if err != nil {
			return verificationRecord{}, err
		}
		record.CandidateJobIDs = append(record.CandidateJobIDs, job.JobID)
	}
	if err := operations.store.putImmutable(ctx, "verification", verificationID, record, profile.ttl()); err != nil {
		return verificationRecord{}, err
	}
	if err := operations.store.get(ctx, "verification", verificationID, &record); err != nil {
		return verificationRecord{}, err
	}
	return record, nil
}

func (operations *workspaceOperations) submitJob(ctx context.Context, workspace workspaceRecord, profile workspaceProfile, candidateID, verificationID, target, commandID, idempotencyKey, subPath string, command commandProfile) (jobRecord, error) {
	digest := sha256.Sum256([]byte(workspace.WorkspaceID + "\x00" + candidateID + "\x00" + verificationID + "\x00" + target + "\x00" + commandID + "\x00" + idempotencyKey))
	jobID := "job_" + hex.EncodeToString(digest[:])
	var existing jobRecord
	if err := operations.store.get(ctx, "job", jobID, &existing); err == nil {
		// Submission can resume before runVerification has saved its aggregate.
		// Reconcile the job, but let the aggregate owner create/refresh its receipt.
		return operations.observeJob(ctx, jobID, false)
	} else if !errors.Is(err, errWorkspaceRecordNotFound) {
		return jobRecord{}, err
	}
	createdAt := operations.now()
	commandBytes, err := json.Marshal(command)
	if err != nil {
		return jobRecord{}, errors.New("encode command identity")
	}
	commandDigest := sha256.Sum256(commandBytes)
	record := jobRecord{
		SchemaVersion: 1, JobID: jobID, KubernetesJob: kubernetesJobName(jobID), WorkspaceID: workspace.WorkspaceID,
		CandidateID: candidateID, VerificationID: verificationID, ProfileID: profile.WorkspaceProfileID, ProfileSHA256: profile.profileSHA256,
		CaseID: workspace.CaseID, Target: target, CommandID: commandID, CommandSHA256: hex.EncodeToString(commandDigest[:]),
		RunnerImage: profile.RunnerImage, Status: "pending",
		CreatedAt: createdAt, ExpiresAt: createdAt.Add(profile.ttl()),
	}
	if err := operations.runner.submit(ctx, profile, record, subPath, command); err != nil {
		return jobRecord{}, serviceFailure(err, true)
	}
	if err := operations.store.putImmutable(ctx, "job", jobID, record, profile.ttl()); err != nil {
		return jobRecord{}, err
	}
	if err := operations.store.get(ctx, "job", jobID, &record); err != nil {
		return jobRecord{}, err
	}
	return record, nil
}

func (operations *workspaceOperations) getJob(ctx context.Context, jobID string) (jobRecord, error) {
	return operations.observeJob(ctx, jobID, true)
}

func (operations *workspaceOperations) observeJob(ctx context.Context, jobID string, refresh bool) (jobRecord, error) {
	if !jobIDPattern.MatchString(jobID) {
		return jobRecord{}, errors.New("job identifier is invalid")
	}
	var record jobRecord
	version, err := operations.store.getEvidenceVersion(ctx, "job", jobID, &record)
	if err != nil {
		return jobRecord{}, err
	}
	profile, ok := operations.profiles.get(record.ProfileID)
	command, commandExists := profile.commandByID[record.CommandID]
	commandBytes, _ := json.Marshal(command)
	commandDigest := sha256.Sum256(commandBytes)
	if !ok || !commandExists || record.JobID != jobID || record.ProfileSHA256 != profile.profileSHA256 || record.RunnerImage != profile.RunnerImage || record.CommandSHA256 != hex.EncodeToString(commandDigest[:]) || record.ExpiresAt.Before(operations.now()) {
		return jobRecord{}, errors.New("job record is invalid or expired")
	}
	if record.Status == "succeeded" || record.Status == "failed" || record.Status == "timed_out" || record.Status == "cancelled" {
		if err := operations.ensureArtifactMetadata(ctx, record); err != nil {
			return jobRecord{}, err
		}
		if refresh && record.VerificationID != "" {
			if err := operations.refreshVerification(ctx, record.VerificationID); err != nil {
				return jobRecord{}, err
			}
		}
		return record, nil
	}
	state, err := operations.runner.inspect(ctx, record.KubernetesJob, profile.Limits.MaximumJobLogBytes)
	if err != nil {
		return jobRecord{}, serviceFailure(err, true)
	}
	record.Status = state.Status
	record.ExitCode = state.ExitCode
	record.TimedOut = state.TimedOut
	record.ArtifactTruncated = state.LogTruncated
	record.StartedAt = state.StartedAt
	record.CompletedAt = state.CompletedAt
	if len(state.Logs) > 0 {
		digest := sha256.Sum256(state.Logs)
		hexDigest := hex.EncodeToString(digest[:])
		artifactID := "art_" + hexDigest
		artifact := artifactRecord{
			SchemaVersion: 1, ArtifactID: artifactID, JobID: record.JobID, ProfileID: record.ProfileID, CaseID: record.CaseID,
			SHA256: hexDigest, Bytes: int64(len(state.Logs)), MediaType: "text/plain; charset=utf-8",
			CreatedAt: operations.now(), ExpiresAt: record.ExpiresAt,
		}
		if err := operations.store.putArtifact(ctx, artifact, state.Logs, timeUntil(operations.now(), record.ExpiresAt)); err != nil {
			return jobRecord{}, err
		}
		record.ArtifactID = artifactID
		record.ArtifactSHA256 = hexDigest
		record.ArtifactBytes = int64(len(state.Logs))
	}
	if err := operations.store.commitEvidence(ctx, []evidenceVersion{version}, record); err != nil {
		if !errors.Is(err, errEvidenceChanged) {
			return jobRecord{}, err
		}
		// Another observer won. Return the current record, never reapply the
		// stale observation or overwrite terminal evidence.
		if err := operations.store.get(ctx, "job", jobID, &record); err != nil {
			return jobRecord{}, err
		}
	}
	if refresh && record.VerificationID != "" {
		if err := operations.refreshVerification(ctx, record.VerificationID); err != nil {
			return jobRecord{}, err
		}
	}
	return record, nil
}

func (operations *workspaceOperations) ensureArtifactMetadata(ctx context.Context, record jobRecord) error {
	if record.ArtifactID == "" {
		return nil
	}
	if !artifactIDPattern.MatchString(record.ArtifactID) || !sha256Pattern.MatchString(record.ArtifactSHA256) ||
		record.ArtifactID != "art_"+record.ArtifactSHA256 || record.ArtifactBytes < 1 {
		return errors.New("job artifact identity is invalid")
	}
	createdAt := record.CompletedAt
	if createdAt.IsZero() {
		createdAt = record.CreatedAt
	}
	metadata := artifactRecord{
		SchemaVersion: 1, ArtifactID: record.ArtifactID, JobID: record.JobID, ProfileID: record.ProfileID, CaseID: record.CaseID,
		SHA256: record.ArtifactSHA256, Bytes: record.ArtifactBytes, MediaType: "text/plain; charset=utf-8",
		CreatedAt: createdAt, ExpiresAt: record.ExpiresAt,
	}
	return operations.store.putArtifactMetadata(ctx, metadata, timeUntil(operations.now(), record.ExpiresAt))
}

func (operations *workspaceOperations) refreshVerification(ctx context.Context, verificationID string) error {
	_, _, err := operations.refreshVerificationSnapshot(ctx, verificationID)
	return err
}

func (operations *workspaceOperations) refreshVerificationSnapshot(ctx context.Context, verificationID string) (verificationRecord, []jobRecord, error) {
	if !verificationIDPattern.MatchString(verificationID) {
		return verificationRecord{}, nil, errors.New("verification identifier is invalid")
	}
	var verification verificationRecord
	version, err := operations.store.getEvidenceVersion(ctx, "verification", verificationID, &verification)
	if err != nil {
		return verificationRecord{}, nil, err
	}
	candidate, _, profile, err := operations.loadCandidate(ctx, verification.CandidateID)
	if err != nil {
		return verificationRecord{}, nil, err
	}
	if verification.SchemaVersion != 1 || verification.VerificationID != verificationID || verification.ProfileID != profile.WorkspaceProfileID || verification.ProfileSHA256 != profile.profileSHA256 {
		return verificationRecord{}, nil, errors.New("verification record identity is invalid")
	}
	versions := []evidenceVersion{version}
	var jobs []jobRecord
	jobIDs := append([]string{verification.ReproductionJobID}, verification.CandidateJobIDs...)
	allTerminal := true
	allCandidatePassed := true
	expectedFailure := false
	for index, jobID := range jobIDs {
		var job jobRecord
		jobVersion, err := operations.store.getEvidenceVersion(ctx, "job", jobID, &job)
		if err != nil {
			return verificationRecord{}, nil, err
		}
		if job.WorkspaceID != candidate.WorkspaceID {
			return verificationRecord{}, nil, errors.New("verification job belongs to a different workspace")
		}
		versions = append(versions, jobVersion)
		jobs = append(jobs, job)
		if job.Status != "succeeded" && job.Status != "failed" && job.Status != "timed_out" && job.Status != "cancelled" {
			allTerminal = false
			continue
		}
		if index == 0 {
			if job.Status == "failed" && job.ExitCode != nil && *job.ExitCode != 0 && job.ArtifactID != "" && !job.ArtifactTruncated {
				_, logs, readErr := operations.store.getArtifactSlice(ctx, job.CaseID, job.ArtifactID, 0, profile.Limits.MaximumJobLogBytes)
				if readErr != nil {
					return verificationRecord{}, nil, readErr // Unavailable evidence is not a changed test outcome.
				}
				expectedFailure = observedNamedTestFailure(logs, candidate.ExpectedTest)
			}
		} else if job.Status != "succeeded" || job.ExitCode == nil || *job.ExitCode != 0 || job.ArtifactTruncated {
			allCandidatePassed = false
		}
	}
	updateVerificationOutcome(&verification, allTerminal, allCandidatePassed, expectedFailure, profile.VerificationReady, operations.now())
	if err := operations.store.commitEvidence(ctx, versions, verification); err != nil {
		return verificationRecord{}, nil, err
	}
	return verification, jobs, nil
}

// UpdatedAt records an outcome change, not a receipt read. Candidate content and
// job identities are fixed for this verification; getJob preserves terminal job
// evidence. Still recompute the outcome so missing/invalid reproduction evidence
// or a changed outcome cannot retain publication eligibility.
func updateVerificationOutcome(verification *verificationRecord, allTerminal, allCandidatePassed, expectedFailure, ready bool, now time.Time) bool {
	status := "running"
	if allTerminal {
		if expectedFailure && allCandidatePassed {
			status = "succeeded"
		} else {
			status = "failed"
		}
	}
	suitePassed := allTerminal && allCandidatePassed
	eligible := status == "succeeded" && ready
	if verification.Status == status && verification.ExpectedTestObserved == expectedFailure &&
		verification.SuitePassed == suitePassed && verification.PublicationEligible == eligible {
		return false
	}
	verification.Status = status
	verification.ExpectedTestObserved = expectedFailure
	verification.SuitePassed = suitePassed
	verification.PublicationEligible = eligible
	verification.UpdatedAt = now
	return true
}

func (operations *workspaceOperations) readArtifact(ctx context.Context, request readArtifactRequest) (readArtifactResponse, error) {
	if request.Format != "" && request.Format != "base64" && request.Format != "text" {
		return readArtifactResponse{}, errors.New("artifact format must be text or base64")
	}
	if !artifactIDPattern.MatchString(request.ArtifactID) || request.ByteStart < 0 {
		return readArtifactResponse{}, errors.New("artifact identifier or offset is invalid")
	}
	var metadata artifactRecord
	if err := operations.store.getArtifactMetadata(ctx, request.CaseID, request.ArtifactID, &metadata); err != nil {
		return readArtifactResponse{}, err
	}
	profile, ok := operations.profiles.get(metadata.ProfileID)
	if !ok {
		return readArtifactResponse{}, errors.New("artifact profile is invalid")
	}
	if request.ByteLimit == 0 {
		request.ByteLimit = profile.Limits.MaximumArtifactReadBytes
	}
	if request.ByteLimit < 1 || request.ByteLimit > profile.Limits.MaximumArtifactReadBytes || request.ByteStart >= metadata.Bytes || metadata.ArtifactID != request.ArtifactID || metadata.ExpiresAt.Before(operations.now()) {
		return readArtifactResponse{}, errors.New("artifact range is invalid")
	}
	verifiedMetadata, content, err := operations.store.getArtifactSlice(ctx, request.CaseID, request.ArtifactID, request.ByteStart, request.ByteLimit)
	if err != nil {
		return readArtifactResponse{}, err
	}
	if verifiedMetadata.SHA256 != metadata.SHA256 || len(content) == 0 {
		return readArtifactResponse{}, errors.New("artifact content is unavailable")
	}
	end := request.ByteStart + int64(len(content))
	return readArtifactResponse{
		ArtifactID: metadata.ArtifactID, SHA256: metadata.SHA256, MediaType: metadata.MediaType,
		ByteStart: request.ByteStart, ByteEnd: end, TotalBytes: metadata.Bytes,
		ContentBase64: base64.StdEncoding.EncodeToString(content), Truncated: end < metadata.Bytes,
	}, nil
}

func textArtifactPage(page readArtifactResponse) (textArtifactResponse, error) {
	content, err := base64.StdEncoding.DecodeString(page.ContentBase64)
	if err != nil {
		return textArtifactResponse{}, errors.New("artifact encoding is invalid")
	}
	result := textArtifactResponse{ArtifactID: page.ArtifactID, SHA256: page.SHA256, MediaType: page.MediaType,
		ByteStart: page.ByteStart, ByteEnd: page.ByteEnd, TotalBytes: page.TotalBytes, Truncated: page.Truncated}
	if utf8.Valid(content) {
		text := string(content)
		result.ContentText, result.Encoding = &text, "utf-8"
	} else {
		result.ContentBase64, result.Encoding = &page.ContentBase64, "base64"
	}
	return result, nil
}

func (operations *workspaceOperations) getJobWithWait(ctx context.Context, jobID string, wait, poll time.Duration) (jobRecord, error) {
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		// The observation window controls waiting only, not multi-write reconciliation.
		record, err := operations.getJob(ctx, jobID)
		if ctx.Err() != nil {
			return jobRecord{}, ctx.Err()
		}
		if err != nil {
			return jobRecord{}, err
		}
		switch record.Status {
		case "succeeded", "failed", "timed_out", "cancelled":
			return record, nil
		}
		if waitCtx.Err() != nil {
			return record, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return jobRecord{}, ctx.Err()
		case <-waitCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return jobRecord{}, ctx.Err()
			}
			return record, nil
		case <-timer.C:
			if ctx.Err() != nil {
				return jobRecord{}, ctx.Err()
			}
			if waitCtx.Err() != nil {
				return record, nil
			}
		}
	}
}

func timeUntil(now, expiry time.Time) time.Duration {
	remaining := expiry.Sub(now)
	if remaining <= 0 {
		return time.Second
	}
	return remaining
}
