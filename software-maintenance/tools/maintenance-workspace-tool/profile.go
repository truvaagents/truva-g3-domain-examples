package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

var (
	workspaceNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	caseIDPattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	snapshotIDPattern     = regexp.MustCompile(`^src_[a-f0-9]{64}$`)
	workspaceIDPattern    = regexp.MustCompile(`^ws_[a-f0-9]{64}$`)
	candidateIDPattern    = regexp.MustCompile(`^cand_[a-f0-9]{64}$`)
	testArtifactIDPattern = regexp.MustCompile(`^test_[a-f0-9]{64}$`)
	jobIDPattern          = regexp.MustCompile(`^job_[a-f0-9]{64}$`)
	artifactIDPattern     = regexp.MustCompile(`^art_[a-f0-9]{64}$`)
	verificationIDPattern = regexp.MustCompile(`^verify_[a-f0-9]{64}$`)
	sha256Pattern         = regexp.MustCompile(`^[a-f0-9]{64}$`)
	gitSHAPattern         = regexp.MustCompile(`^[a-f0-9]{40}$`)
	goTestPattern         = regexp.MustCompile(`^Test[A-Za-z0-9_/]{1,127}$`)
	runnerDigestPattern   = regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)
)

type workspaceProfileStore struct {
	profiles map[string]workspaceProfile
}

func loadWorkspaceProfiles(filename string) (*workspaceProfileStore, error) {
	if strings.TrimSpace(filename) == "" {
		return nil, errors.New("MAINTENANCE_WORKSPACE_PROFILES_FILE is required")
	}
	// #nosec G304,G703 -- operator-selected startup configuration, never a request path.
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read workspace profile: %w", err)
	}
	var profile workspaceProfile
	if err := decodeWorkspaceStrictBytes(data, &profile); err != nil {
		return nil, fmt.Errorf("decode workspace profile: %w", err)
	}
	if err := validateWorkspaceProfile(&profile); err != nil {
		return nil, fmt.Errorf("validate workspace profile: %w", err)
	}
	digest := sha256.Sum256(data)
	profile.profileSHA256 = hex.EncodeToString(digest[:])
	return &workspaceProfileStore{profiles: map[string]workspaceProfile{profile.WorkspaceProfileID: profile}}, nil
}

func validateWorkspaceProfile(profile *workspaceProfile) error {
	if profile.SchemaVersion != 1 || !workspaceNamePattern.MatchString(profile.WorkspaceProfileID) || !workspaceNamePattern.MatchString(profile.RepositoryID) {
		return errors.New("workspace profile identity is invalid")
	}
	if profile.RunnerImage == "" || len(profile.RunnerImage) > 255 || strings.ContainsAny(profile.RunnerImage, " \t\r\n") || !workspaceNamePattern.MatchString(profile.WorkspacePVC) {
		return errors.New("runner image or workspace PVC is invalid")
	}
	if profile.VerificationReady && !runnerDigestPattern.MatchString(profile.RunnerImage) {
		return errors.New("a ready workspace profile requires a digest-pinned runner image")
	}
	if len(profile.AllowedCallers) == 0 || len(profile.AllowedCallers) > 16 || len(profile.CandidateStageCallers) == 0 {
		return errors.New("workspace caller policy is invalid")
	}
	profile.callerSet = make(map[string]struct{}, len(profile.AllowedCallers))
	for _, caller := range profile.AllowedCallers {
		if !workspaceNamePattern.MatchString(caller) {
			return errors.New("allowed caller is invalid")
		}
		profile.callerSet[caller] = struct{}{}
	}
	profile.stageCallerSet = make(map[string]struct{}, len(profile.CandidateStageCallers))
	for _, caller := range profile.CandidateStageCallers {
		if _, ok := profile.callerSet[caller]; !ok {
			return errors.New("candidate stage caller must be generally allowed")
		}
		profile.stageCallerSet[caller] = struct{}{}
	}
	if len(profile.DeniedPaths) == 0 || len(profile.DeniedPaths) > 64 {
		return errors.New("denied path policy is invalid")
	}
	for index, denied := range profile.DeniedPaths {
		normalized, err := normalizeWorkspacePath(denied)
		if err != nil || normalized != denied {
			return errors.New("denied path is invalid")
		}
		profile.DeniedPaths[index] = normalized
	}
	if len(profile.ExperimentCommands) == 0 || len(profile.ExperimentCommands) > 16 {
		return errors.New("experiment commands are invalid")
	}
	profile.commandByID = make(map[string]commandProfile, len(profile.ExperimentCommands))
	for _, command := range profile.ExperimentCommands {
		if !workspaceNamePattern.MatchString(command.ID) || len(command.Argv) == 0 || len(command.Argv) > 16 {
			return errors.New("command profile is invalid")
		}
		for _, argument := range command.Argv {
			if argument == "" || len(argument) > 256 || strings.ContainsRune(argument, 0) {
				return errors.New("command argument is invalid")
			}
		}
		if _, exists := profile.commandByID[command.ID]; exists {
			return errors.New("command identifiers must be unique")
		}
		profile.commandByID[command.ID] = command
	}
	if _, ok := profile.commandByID[profile.ReproductionCommandID]; !ok || len(profile.VerificationCommandIDs) == 0 || len(profile.VerificationCommandIDs) > 8 {
		return errors.New("verification command policy is invalid")
	}
	for _, id := range profile.VerificationCommandIDs {
		if _, ok := profile.commandByID[id]; !ok {
			return errors.New("verification command is not allowlisted")
		}
	}
	limits := profile.Limits
	if limits.WorkspaceTTLSeconds < 3600 || limits.WorkspaceTTLSeconds > 30*24*60*60 ||
		limits.MaximumSnapshotBytes < 1024 || limits.MaximumSnapshotBytes > 64<<20 ||
		limits.MaximumSnapshotFiles < 1 || limits.MaximumSnapshotFiles > 20_000 ||
		limits.MaximumCandidateFiles < 1 || limits.MaximumCandidateFiles > 128 ||
		limits.MaximumCandidateBytes < 1024 || limits.MaximumCandidateBytes > 4<<20 ||
		limits.MaximumFileBytes < 256 || limits.MaximumFileBytes > limits.MaximumCandidateBytes ||
		limits.MaximumJobSeconds < 10 || limits.MaximumJobSeconds > 1800 ||
		limits.MaximumJobLogBytes < 1024 || limits.MaximumJobLogBytes > 4<<20 ||
		limits.MaximumArtifactReadBytes < 256 || limits.MaximumArtifactReadBytes > 1<<20 {
		return errors.New("workspace limits are outside supported bounds")
	}
	if len(profile.Limitations) == 0 || len(profile.Limitations) > 16 {
		return errors.New("workspace limitations are invalid")
	}
	return nil
}

func (profile workspaceProfile) allows(caller string) bool {
	_, ok := profile.callerSet[caller]
	return ok
}

func (profile workspaceProfile) allowsStage(caller string) bool {
	_, ok := profile.stageCallerSet[caller]
	return ok
}

func (profile workspaceProfile) denies(candidate string) bool {
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

func (profile workspaceProfile) ttl() time.Duration {
	return time.Duration(profile.Limits.WorkspaceTTLSeconds) * time.Second
}

func (store *workspaceProfileStore) get(profileID string) (workspaceProfile, bool) {
	profile, ok := store.profiles[profileID]
	return profile, ok
}

func normalizeWorkspacePath(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return "", errors.New("invalid workspace path")
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return "", errors.New("invalid workspace path")
		}
	}
	normalized := path.Clean(value)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || normalized != value {
		return "", errors.New("invalid workspace path")
	}
	return normalized, nil
}
