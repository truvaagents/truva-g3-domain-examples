package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
)

var (
	repairNamePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	caseIDPattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	observationIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	snapshotIDPattern     = regexp.MustCompile(`^src_[a-f0-9]{64}$`)
	candidateIDPattern    = regexp.MustCompile(`^cand_[a-f0-9]{64}$`)
	testArtifactIDPattern = regexp.MustCompile(`^test_[a-f0-9]{64}$`)
	verificationIDPattern = regexp.MustCompile(`^verify_[a-f0-9]{64}$`)
	workspaceIDPattern    = regexp.MustCompile(`^ws_[a-f0-9]{64}$`)
	jobIDPattern          = regexp.MustCompile(`^job_[a-f0-9]{64}$`)
	sha256Pattern         = regexp.MustCompile(`^[a-f0-9]{64}$`)
	gitSHAPattern         = regexp.MustCompile(`^[a-f0-9]{40}$`)
	profileVersionPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	goTestPattern         = regexp.MustCompile(`^Test[A-Za-z0-9_/]{1,127}$`)
)

type repairProfileStore struct {
	profiles map[string]repairProfile
}

func loadRepairProfiles(filename string) (*repairProfileStore, error) {
	if strings.TrimSpace(filename) == "" {
		return nil, errors.New("MAINTENANCE_REPAIR_PROFILES_FILE is required")
	}
	// #nosec G304,G703 -- operator-selected startup configuration, never request-controlled.
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read repair profile: %w", err)
	}
	var profile repairProfile
	if err := decodeRepairStrictBytes(data, &profile); err != nil {
		return nil, fmt.Errorf("decode repair profile: %w", err)
	}
	if err := validateRepairProfile(&profile); err != nil {
		return nil, fmt.Errorf("validate repair profile: %w", err)
	}
	limit, err := responsecontract.ResolveSummaryLimit(os.LookupEnv, "MAINTENANCE_REPAIR_MAXIMUM_SUMMARY_BYTES")
	if err != nil {
		return nil, err
	}
	profile.Limits.MaximumSummaryBytes = limit
	digest := sha256.Sum256(data)
	profile.profileSHA256 = hex.EncodeToString(digest[:])
	return &repairProfileStore{profiles: map[string]repairProfile{profile.RepairProfileID: profile}}, nil
}

func validateRepairProfile(profile *repairProfile) error {
	if profile.SchemaVersion != 1 || !repairNamePattern.MatchString(profile.RepairProfileID) ||
		!repairNamePattern.MatchString(profile.ServiceProfileID) || !repairNamePattern.MatchString(profile.WorkspaceProfileID) ||
		!repairNamePattern.MatchString(profile.RepositoryID) || !profileVersionPattern.MatchString(profile.ExpectedServiceProfileVersion) ||
		!sha256Pattern.MatchString(profile.ExpectedSourceProfileSHA256) || !sha256Pattern.MatchString(profile.ExpectedWorkspaceProfileSHA256) {
		return errors.New("repair profile identity is invalid")
	}
	if len(profile.AllowedCallers) == 0 || len(profile.AllowedCallers) > 8 {
		return errors.New("repair allowed caller policy is invalid")
	}
	profile.callerSet = make(map[string]struct{}, len(profile.AllowedCallers))
	for _, caller := range profile.AllowedCallers {
		if !repairNamePattern.MatchString(caller) {
			return errors.New("repair allowed caller is invalid")
		}
		if _, exists := profile.callerSet[caller]; exists {
			return errors.New("repair allowed callers must be unique")
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
		limits.MaximumFindings < 1 || limits.MaximumFindings > 64 ||
		limits.MaximumReferenceBytes < 64 || limits.MaximumReferenceBytes > 1024 {
		return errors.New("repair profile limits are outside supported bounds")
	}
	if len(profile.Limitations) == 0 || len(profile.Limitations) > 16 {
		return errors.New("repair profile limitations are invalid")
	}
	for _, limitation := range profile.Limitations {
		if limitation == "" || len(limitation) > 1024 || !validRepairText(limitation) {
			return errors.New("repair profile contains an invalid limitation")
		}
	}
	return nil
}

func (profile repairProfile) allows(caller string) bool {
	_, ok := profile.callerSet[caller]
	return ok
}

func (store *repairProfileStore) get(profileID string) (repairProfile, bool) {
	profile, ok := store.profiles[profileID]
	return profile, ok
}

func (store *repairProfileStore) maximumRequestBytes() int64 {
	var maximum int64
	for _, profile := range store.profiles {
		if profile.Limits.MaximumRequestBytes > maximum {
			maximum = profile.Limits.MaximumRequestBytes
		}
	}
	return maximum
}
