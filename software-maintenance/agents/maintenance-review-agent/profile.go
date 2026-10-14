package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
)

var (
	reviewNamePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	caseIDPattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	candidateIDPattern    = regexp.MustCompile(`^cand_[a-f0-9]{64}$`)
	testArtifactIDPattern = regexp.MustCompile(`^test_[a-f0-9]{64}$`)
	verificationIDPattern = regexp.MustCompile(`^verify_[a-f0-9]{64}$`)
	reviewIDPattern       = regexp.MustCompile(`^review_[a-f0-9]{64}$`)
	workspaceIDPattern    = regexp.MustCompile(`^ws_[a-f0-9]{64}$`)
	snapshotIDPattern     = regexp.MustCompile(`^src_[a-f0-9]{64}$`)
	jobIDPattern          = regexp.MustCompile(`^job_[a-f0-9]{64}$`)
	artifactIDPattern     = regexp.MustCompile(`^art_[a-f0-9]{64}$`)
	sha256Pattern         = regexp.MustCompile(`^[a-f0-9]{64}$`)
	gitSHAPattern         = regexp.MustCompile(`^[a-f0-9]{40}$`)
	profileVersionPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	goTestPattern         = regexp.MustCompile(`^Test[A-Za-z0-9_/]{1,127}$`)
	runnerDigestPattern   = regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)
)

type reviewProfileStore struct {
	profiles map[string]reviewProfile
}

func loadReviewProfiles(filename string) (*reviewProfileStore, error) {
	if strings.TrimSpace(filename) == "" {
		return nil, errors.New("MAINTENANCE_REVIEW_PROFILES_FILE is required")
	}
	// #nosec G304,G703 -- operator-selected startup configuration, never request-controlled.
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read review profile: %w", err)
	}
	var profile reviewProfile
	if err := decodeReviewStrictBytes(data, &profile); err != nil {
		return nil, fmt.Errorf("decode review profile: %w", err)
	}
	if err := validateReviewProfile(&profile); err != nil {
		return nil, fmt.Errorf("validate review profile: %w", err)
	}
	limit, err := responsecontract.ResolveSummaryLimit(os.LookupEnv, "MAINTENANCE_REVIEW_MAXIMUM_SUMMARY_BYTES")
	if err != nil {
		return nil, err
	}
	profile.Limits.MaximumSummaryBytes = limit
	digest := sha256.Sum256(data)
	profile.profileSHA256 = hex.EncodeToString(digest[:])
	return &reviewProfileStore{profiles: map[string]reviewProfile{profile.ReviewProfileID: profile}}, nil
}

func validateReviewProfile(profile *reviewProfile) error {
	if profile.SchemaVersion != 1 || !reviewNamePattern.MatchString(profile.ReviewProfileID) ||
		!reviewNamePattern.MatchString(profile.ServiceProfileID) || !reviewNamePattern.MatchString(profile.WorkspaceProfileID) ||
		!reviewNamePattern.MatchString(profile.RepositoryID) {
		return errors.New("review profile identity is invalid")
	}
	if !profileVersionPattern.MatchString(profile.ExpectedServiceProfileVersion) ||
		!sha256Pattern.MatchString(profile.ExpectedSourceProfileSHA256) || !sha256Pattern.MatchString(profile.ExpectedWorkspaceProfileSHA256) {
		return errors.New("review prerequisite profile identities are invalid")
	}
	if len(profile.AllowedCallers) == 0 || len(profile.AllowedCallers) > 8 {
		return errors.New("review allowed caller policy is invalid")
	}
	profile.callerSet = make(map[string]struct{}, len(profile.AllowedCallers))
	for _, caller := range profile.AllowedCallers {
		if !reviewNamePattern.MatchString(caller) {
			return errors.New("review allowed caller is invalid")
		}
		if _, exists := profile.callerSet[caller]; exists {
			return errors.New("review allowed callers must be unique")
		}
		profile.callerSet[caller] = struct{}{}
	}
	limits := profile.Limits
	findingLimit, err := responsecontract.ResolveFindingLimit(limits.FindingBytesJSON, limits.MaximumReferenceBytes)
	if err != nil {
		return err
	}
	profile.Limits.MaximumFindingBytes = findingLimit
	if limits.MaximumRequestBytes < 1024 || limits.MaximumRequestBytes > 128<<10 ||
		limits.MaximumObjectiveBytes < 128 || limits.MaximumObjectiveBytes > 32<<10 ||
		limits.MaximumCandidateFiles < 2 || limits.MaximumCandidateFiles > 128 ||
		limits.MaximumCandidateBytes < 1024 || limits.MaximumCandidateBytes > 4<<20 ||
		limits.MaximumFindings < 1 || limits.MaximumFindings > 64 ||
		limits.MaximumReferenceBytes < 64 || limits.MaximumReferenceBytes > 1024 ||
		limits.ReceiptTTLSeconds < 60*60 || limits.ReceiptTTLSeconds > 30*24*60*60 {
		return errors.New("review profile limits are outside supported bounds")
	}
	if len(profile.Limitations) == 0 || len(profile.Limitations) > 16 {
		return errors.New("review profile limitations are invalid")
	}
	for _, limitation := range profile.Limitations {
		if limitation == "" || len(limitation) > 1024 || !validReviewText(limitation) {
			return errors.New("review profile contains an invalid limitation")
		}
	}
	return nil
}

func (profile reviewProfile) allows(caller string) bool {
	_, ok := profile.callerSet[caller]
	return ok
}

func (profile reviewProfile) receiptTTL() time.Duration {
	return time.Duration(profile.Limits.ReceiptTTLSeconds) * time.Second
}

func (store *reviewProfileStore) get(profileID string) (reviewProfile, bool) {
	profile, ok := store.profiles[profileID]
	return profile, ok
}

func (store *reviewProfileStore) maximumRequestBytes() int64 {
	var maximum int64
	for _, profile := range store.profiles {
		if profile.Limits.MaximumRequestBytes > maximum {
			maximum = profile.Limits.MaximumRequestBytes
		}
	}
	return maximum
}
