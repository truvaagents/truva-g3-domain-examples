package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

var (
	publicationNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	repositoryPartPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
	caseIDPattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	publicationIDPattern   = regexp.MustCompile(`^pub_[a-f0-9]{64}$`)
	candidateIDPattern     = regexp.MustCompile(`^cand_[a-f0-9]{64}$`)
	testArtifactIDPattern  = regexp.MustCompile(`^test_[a-f0-9]{64}$`)
	verificationIDPattern  = regexp.MustCompile(`^verify_[a-f0-9]{64}$`)
	reviewIDPattern        = regexp.MustCompile(`^review_[a-f0-9]{64}$`)
	workspaceIDPattern     = regexp.MustCompile(`^ws_[a-f0-9]{64}$`)
	snapshotIDPattern      = regexp.MustCompile(`^src_[a-f0-9]{64}$`)
	jobIDPattern           = regexp.MustCompile(`^job_[a-f0-9]{64}$`)
	artifactIDPattern      = regexp.MustCompile(`^art_[a-f0-9]{64}$`)
	sha256Pattern          = regexp.MustCompile(`^[a-f0-9]{64}$`)
	gitSHAPattern          = regexp.MustCompile(`^[a-f0-9]{40}$`)
	goTestPattern          = regexp.MustCompile(`^Test[A-Za-z0-9_/]{1,127}$`)
	runnerDigestPattern    = regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)
)

type publicationProfileStore struct {
	profiles map[string]publicationProfile
}

func loadPublicationProfiles(filename string) (*publicationProfileStore, error) {
	if strings.TrimSpace(filename) == "" {
		return nil, errors.New("MAINTENANCE_PUBLICATION_PROFILES_FILE is required")
	}
	// #nosec G304,G703 -- operator-selected startup configuration, never a request path.
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read publication profile: %w", err)
	}
	var profile publicationProfile
	if err := decodePublicationStrictBytes(data, &profile); err != nil {
		return nil, fmt.Errorf("decode publication profile: %w", err)
	}
	if err := validatePublicationProfile(&profile); err != nil {
		return nil, fmt.Errorf("validate publication profile: %w", err)
	}
	digest := sha256.Sum256(data)
	profile.profileSHA256 = hex.EncodeToString(digest[:])
	return &publicationProfileStore{profiles: map[string]publicationProfile{profile.PublicationProfileID: profile}}, nil
}

func validatePublicationProfile(profile *publicationProfile) error {
	if profile.SchemaVersion != 1 || !publicationNamePattern.MatchString(profile.PublicationProfileID) || !publicationNamePattern.MatchString(profile.RepositoryID) || !publicationNamePattern.MatchString(profile.WorkspaceProfileID) {
		return errors.New("publication profile identity is invalid")
	}
	if !sha256Pattern.MatchString(profile.ExpectedSourceProfileSHA256) || !sha256Pattern.MatchString(profile.ExpectedWorkspaceProfileSHA256) || profile.ExpectedReviewProfileSHA256 != "" && !sha256Pattern.MatchString(profile.ExpectedReviewProfileSHA256) || profile.PublicationReady && profile.ExpectedReviewProfileSHA256 == "" {
		return errors.New("publication prerequisite profile digests are invalid")
	}
	if !repositoryPartPattern.MatchString(profile.Owner) || !repositoryPartPattern.MatchString(profile.Repository) || !repositoryPartPattern.MatchString(profile.BaseRef) || !publicationNamePattern.MatchString(profile.BranchPrefix) {
		return errors.New("repository or branch policy is invalid")
	}
	if strings.TrimSpace(profile.CommitAuthorName) == "" || len(profile.CommitAuthorName) > 100 {
		return errors.New("commit author name is invalid")
	}
	address, err := mail.ParseAddress(profile.CommitAuthorEmail)
	if err != nil || address.Address != profile.CommitAuthorEmail || len(profile.CommitAuthorEmail) > 254 {
		return errors.New("commit author email is invalid")
	}
	if len(profile.AllowedCallers) == 0 || len(profile.AllowedCallers) > 8 {
		return errors.New("allowed caller policy is invalid")
	}
	profile.callerSet = make(map[string]struct{}, len(profile.AllowedCallers))
	for _, caller := range profile.AllowedCallers {
		if !publicationNamePattern.MatchString(caller) {
			return errors.New("allowed caller is invalid")
		}
		profile.callerSet[caller] = struct{}{}
	}
	if len(profile.DeniedPaths) == 0 || len(profile.DeniedPaths) > 64 {
		return errors.New("denied path policy is invalid")
	}
	for index, denied := range profile.DeniedPaths {
		normalized, err := normalizePublicationPath(denied)
		if err != nil || normalized != denied {
			return errors.New("denied path is invalid")
		}
		profile.DeniedPaths[index] = normalized
	}
	limits := profile.Limits
	for _, setting := range []struct {
		name    string
		value   *int
		maximum int
	}{
		{"maximum_open_prs", limits.MaximumOpenPRs, 100},
		{"maximum_pr_files", limits.MaximumPRFiles, 1000},
		{"maximum_pr_patch_bytes", limits.MaximumPRPatchBytes, 1 << 20},
	} {
		if setting.value != nil && (*setting.value < 1 || *setting.value > setting.maximum) {
			return fmt.Errorf("%s must be between 1 and %d when supplied", setting.name, setting.maximum)
		}
	}
	if limits.TimeoutMilliseconds < 1000 || limits.TimeoutMilliseconds > 60_000 ||
		limits.MaximumFiles < 1 || limits.MaximumFiles > 128 ||
		limits.MaximumCandidateBytes < 1024 || limits.MaximumCandidateBytes > 4<<20 ||
		limits.MaximumTitleBytes < 10 || limits.MaximumTitleBytes > 256 ||
		limits.MaximumBodyBytes < 100 || limits.MaximumBodyBytes > 64<<10 ||
		limits.RecordTTLSeconds < 24*60*60 || limits.RecordTTLSeconds > 180*24*60*60 ||
		limits.ClaimTTLSeconds < 30 || limits.ClaimTTLSeconds > 600 {
		return errors.New("publication limits are outside supported bounds")
	}
	if len(profile.Limitations) == 0 || len(profile.Limitations) > 16 {
		return errors.New("publication limitations are invalid")
	}
	return nil
}

func (profile publicationProfile) allows(caller string) bool {
	_, ok := profile.callerSet[caller]
	return ok
}

func (profile publicationProfile) denies(candidate string) bool {
	base := path.Base(candidate)
	if base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || base == "id_rsa" || base == "id_ed25519" {
		return true
	}
	for _, denied := range profile.DeniedPaths {
		if candidate == denied || strings.HasPrefix(candidate, denied+"/") {
			return true
		}
	}
	return false
}

func (profile publicationProfile) timeout() time.Duration {
	return time.Duration(profile.Limits.TimeoutMilliseconds) * time.Millisecond
}

func (profile publicationProfile) recordTTL() time.Duration {
	return time.Duration(profile.Limits.RecordTTLSeconds) * time.Second
}

func (profile publicationProfile) claimTTL() time.Duration {
	return time.Duration(profile.Limits.ClaimTTLSeconds) * time.Second
}

func (store *publicationProfileStore) get(profileID string) (publicationProfile, bool) {
	profile, ok := store.profiles[profileID]
	return profile, ok
}

func normalizePublicationPath(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return "", errors.New("invalid publication path")
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return "", errors.New("invalid publication path")
		}
	}
	normalized := path.Clean(value)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || normalized != value {
		return "", errors.New("invalid publication path")
	}
	return normalized, nil
}
