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
	sourceIDPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	repositoryPartPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
	gitSHAPattern         = regexp.MustCompile(`^[a-f0-9]{40}$`)
	snapshotIDPattern     = regexp.MustCompile(`^src_[a-f0-9]{64}$`)
	sha256Pattern         = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type sourceProfileStore struct {
	profiles map[string]sourceProfile
}

func loadSourceProfileStore(filename string) (*sourceProfileStore, error) {
	if strings.TrimSpace(filename) == "" {
		return nil, errors.New("MAINTENANCE_SOURCE_PROFILES_FILE is required")
	}
	// #nosec G304,G703 -- this is an operator-selected startup configuration
	// path. Requests cannot select it, and its contents are strictly decoded.
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read source profile: %w", err)
	}
	var profile sourceProfile
	if err := decodeSourceStrictBytes(data, &profile); err != nil {
		return nil, fmt.Errorf("decode source profile: %w", err)
	}
	if err := validateSourceProfile(&profile); err != nil {
		return nil, fmt.Errorf("validate source profile: %w", err)
	}
	digest := sha256.Sum256(data)
	profile.profileSHA256 = hex.EncodeToString(digest[:])
	return &sourceProfileStore{profiles: map[string]sourceProfile{profile.RepositoryID: profile}}, nil
}

func validateSourceProfile(profile *sourceProfile) error {
	if profile.SchemaVersion != 1 || !sourceIDPattern.MatchString(profile.RepositoryID) {
		return errors.New("source profile identity is invalid")
	}
	if !repositoryPartPattern.MatchString(profile.Owner) || !repositoryPartPattern.MatchString(profile.Repository) {
		return errors.New("repository owner or name is invalid")
	}
	if err := validateNamedRef(profile.DefaultRef); err != nil {
		return errors.New("default_ref is invalid")
	}
	if len(profile.AllowedNamedRefs) == 0 || len(profile.AllowedNamedRefs) > 16 {
		return errors.New("allowed_named_refs must be bounded and non-empty")
	}
	profile.namedRefSet = make(map[string]struct{}, len(profile.AllowedNamedRefs))
	for _, ref := range profile.AllowedNamedRefs {
		if err := validateNamedRef(ref); err != nil {
			return errors.New("allowed named ref is invalid")
		}
		if _, exists := profile.namedRefSet[ref]; exists {
			return errors.New("allowed named refs must be unique")
		}
		profile.namedRefSet[ref] = struct{}{}
	}
	if _, ok := profile.namedRefSet[profile.DefaultRef]; !ok {
		return errors.New("default_ref must be allowlisted")
	}
	if len(profile.DeniedPaths) == 0 || len(profile.DeniedPaths) > 64 {
		return errors.New("denied_paths must be bounded and non-empty")
	}
	for index, denied := range profile.DeniedPaths {
		normalized, err := normalizeSourcePath(denied, false)
		if err != nil || normalized != denied {
			return errors.New("denied path is invalid")
		}
		profile.DeniedPaths[index] = normalized
	}
	if len(profile.AllowedCallers) == 0 || len(profile.AllowedCallers) > 16 {
		return errors.New("allowed_callers must be bounded and non-empty")
	}
	profile.callerSet = make(map[string]struct{}, len(profile.AllowedCallers))
	for _, caller := range profile.AllowedCallers {
		if !sourceIDPattern.MatchString(caller) {
			return errors.New("allowed caller is invalid")
		}
		if _, exists := profile.callerSet[caller]; exists {
			return errors.New("allowed callers must be unique")
		}
		profile.callerSet[caller] = struct{}{}
	}
	limits := profile.Limits
	if limits.TimeoutMilliseconds < 100 || limits.TimeoutMilliseconds > 30_000 ||
		limits.MaximumAPIResponseBytes < 1024 || limits.MaximumAPIResponseBytes > 8<<20 ||
		limits.MaximumFileBytes < 1024 || limits.MaximumFileBytes > 2<<20 ||
		limits.MaximumReadBytes < 256 || limits.MaximumReadBytes > limits.MaximumFileBytes ||
		limits.MaximumTreeEntries < 1 || limits.MaximumTreeEntries > 20_000 ||
		limits.MaximumTreeResults < 1 || limits.MaximumTreeResults > 500 ||
		limits.MaximumSearchFiles < 1 || limits.MaximumSearchFiles > 256 ||
		limits.MaximumSearchBytes < limits.MaximumFileBytes || limits.MaximumSearchBytes > 16<<20 ||
		limits.MaximumSearchResults < 1 || limits.MaximumSearchResults > 200 ||
		limits.MaximumSnapshotFiles < 1 || limits.MaximumSnapshotFiles > limits.MaximumTreeEntries ||
		limits.MaximumSnapshotBytes < limits.MaximumFileBytes || limits.MaximumSnapshotBytes > 64<<20 ||
		limits.MaximumSnapshotDownloadBytes < 1024 || limits.MaximumSnapshotDownloadBytes > 1<<20 ||
		limits.SnapshotTTLSeconds < 300 || limits.SnapshotTTLSeconds > 7*24*60*60 {
		return errors.New("source profile limits are outside supported bounds")
	}
	if len(profile.Limitations) == 0 || len(profile.Limitations) > 16 {
		return errors.New("limitations must be bounded and non-empty")
	}
	return nil
}

func (p sourceProfile) timeout() time.Duration {
	return time.Duration(p.Limits.TimeoutMilliseconds) * time.Millisecond
}

func (p sourceProfile) repositoryName() string {
	return p.Owner + "/" + p.Repository
}

func (p sourceProfile) allows(caller string) bool {
	_, ok := p.callerSet[caller]
	return ok
}

func (p sourceProfile) allowsRevision(revision string) bool {
	if gitSHAPattern.MatchString(revision) {
		return true
	}
	_, ok := p.namedRefSet[revision]
	return ok
}

func (p sourceProfile) denies(candidate string) bool {
	base := path.Base(candidate)
	if base == ".env" || strings.HasPrefix(base, ".env.") || base == "id_rsa" || base == "id_ed25519" || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") {
		return true
	}
	for _, denied := range p.DeniedPaths {
		if candidate == denied || strings.HasPrefix(candidate, denied+"/") {
			return true
		}
	}
	return false
}

func (s *sourceProfileStore) get(repositoryID string) (sourceProfile, bool) {
	profile, ok := s.profiles[repositoryID]
	return profile, ok
}

func validateNamedRef(ref string) error {
	if ref == "" || len(ref) > 128 || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.ContainsAny(ref, "\\~^:?*[") {
		return errors.New("invalid named ref")
	}
	for _, char := range ref {
		if char <= 0x20 || char == 0x7f {
			return errors.New("invalid named ref")
		}
	}
	return nil
}

func normalizeSourcePath(value string, allowEmpty bool) (string, error) {
	if value == "" && allowEmpty {
		return "", nil
	}
	if value == "" || value != strings.TrimSpace(value) || strings.Contains(value, "\\") || strings.ContainsRune(value, '\x00') || strings.HasPrefix(value, "/") {
		return "", errors.New("invalid source path")
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return "", errors.New("invalid source path")
		}
	}
	normalized := path.Clean(value)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || normalized != value {
		return "", errors.New("invalid source path")
	}
	return normalized, nil
}
