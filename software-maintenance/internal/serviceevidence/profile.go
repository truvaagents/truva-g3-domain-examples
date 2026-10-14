package serviceevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	profileIDPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	identifierPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	gitRevisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
)

type upstreamEndpoints struct {
	Catalog         string `json:"catalog"`
	Observations    string `json:"observations"`
	Evidence        string `json:"evidence"`
	BusinessOutcome string `json:"business_outcome"`
	BuildInfo       string `json:"build_info"`
}

type upstreamProfile struct {
	BaseURL   string            `json:"base_url"`
	Endpoints upstreamEndpoints `json:"endpoints"`
}

type profileLimits struct {
	TimeoutMilliseconds  int64 `json:"timeout_milliseconds"`
	MaximumResponseBytes int64 `json:"maximum_response_bytes"`
	MaximumRequestBytes  int64 `json:"maximum_request_bytes"`
	MaximumWindowSeconds int   `json:"maximum_window_seconds"`
	RetentionSeconds     int   `json:"retention_seconds"`
	MaximumItems         int   `json:"maximum_items"`
}

type serviceProfile struct {
	SchemaVersion      int                 `json:"schema_version"`
	ProfileID          string              `json:"profile_id"`
	AssessmentReady    bool                `json:"assessment_ready"`
	ServiceName        string              `json:"service_name"`
	Repository         repositoryReference `json:"repository"`
	ContractReferences []contractReference `json:"contract_references"`
	Upstream           upstreamProfile     `json:"upstream"`
	Limits             profileLimits       `json:"limits"`
	AllowedCallers     []string            `json:"allowed_callers"`
	SupportedEvidence  []string            `json:"supported_evidence"`
	Limitations        []string            `json:"limitations"`

	version       string
	parsedBaseURL *url.URL
	callerSet     map[string]struct{}
}

func (p serviceProfile) timeout() time.Duration {
	return time.Duration(p.Limits.TimeoutMilliseconds) * time.Millisecond
}

func (p serviceProfile) allows(caller string) bool {
	_, ok := p.callerSet[caller]
	return ok
}

func (p serviceProfile) public() serviceProfileResponse {
	return serviceProfileResponse{
		ProfileID:          p.ProfileID,
		ProfileVersion:     p.version,
		AssessmentReady:    p.AssessmentReady,
		ServiceName:        p.ServiceName,
		Repository:         p.Repository,
		ContractReferences: append([]contractReference{}, p.ContractReferences...),
		SupportedEvidence:  append([]string{}, p.SupportedEvidence...),
		ObservationConstraints: observationConstraints{
			MaximumWindowSeconds: p.Limits.MaximumWindowSeconds,
			RetentionSeconds:     p.Limits.RetentionSeconds,
			MaximumItems:         p.Limits.MaximumItems,
			CompleteStream:       false,
		},
		Limitations: append([]string{}, p.Limitations...),
	}
}

type profileStore struct {
	profiles map[string]serviceProfile
}

func loadProfileStore(filename string) (*profileStore, error) {
	if strings.TrimSpace(filename) == "" {
		return nil, errors.New("MAINTENANCE_EVIDENCE_PROFILES_FILE is required")
	}
	// #nosec G304,G703 -- the operator selects this startup-only profile path; the
	// file is parsed with a strict schema and cannot be selected by a request.
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	var profile serviceProfile
	if err := decodeStrictBytes(data, &profile); err != nil {
		return nil, fmt.Errorf("decode profile: %w", err)
	}
	if err := validateProfile(&profile); err != nil {
		return nil, fmt.Errorf("validate profile: %w", err)
	}
	canonical, err := json.Marshal(profile)
	if err != nil {
		return nil, fmt.Errorf("canonicalize profile: %w", err)
	}
	digest := sha256.Sum256(canonical)
	profile.version = "sha256:" + hex.EncodeToString(digest[:])
	return &profileStore{profiles: map[string]serviceProfile{profile.ProfileID: profile}}, nil
}

func validateProfile(profile *serviceProfile) error {
	if profile.SchemaVersion != 1 {
		return errors.New("schema_version must be 1")
	}
	if !profileIDPattern.MatchString(profile.ProfileID) {
		return errors.New("profile_id is invalid")
	}
	if !profileIDPattern.MatchString(profile.ServiceName) {
		return errors.New("service_name is invalid")
	}
	if strings.TrimSpace(profile.Repository.Name) == "" || !profileIDPattern.MatchString(profile.Repository.DefaultBranch) {
		return errors.New("repository identity is invalid")
	}
	if profile.AssessmentReady && len(profile.ContractReferences) == 0 {
		return errors.New("assessment-ready profiles require a pinned contract reference")
	}
	for _, ref := range profile.ContractReferences {
		if strings.TrimSpace(ref.Kind) == "" || !gitRevisionPattern.MatchString(ref.Revision) {
			return errors.New("contract reference kind or revision is invalid")
		}
		if ref.Path == "" || path.IsAbs(ref.Path) || path.Clean(ref.Path) != ref.Path || strings.HasPrefix(ref.Path, "..") {
			return errors.New("contract reference path is invalid")
		}
	}

	baseURL, err := url.Parse(profile.Upstream.BaseURL)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" || (baseURL.Path != "" && baseURL.Path != "/") {
		return errors.New("upstream base_url is invalid")
	}
	profile.parsedBaseURL = baseURL

	endpoints := map[string]string{
		"catalog":          profile.Upstream.Endpoints.Catalog,
		"observations":     profile.Upstream.Endpoints.Observations,
		"evidence":         profile.Upstream.Endpoints.Evidence,
		"business_outcome": profile.Upstream.Endpoints.BusinessOutcome,
		"build_info":       profile.Upstream.Endpoints.BuildInfo,
	}
	for name, endpoint := range endpoints {
		if err := validateEndpoint(name, endpoint); err != nil {
			return err
		}
	}
	if strings.Count(profile.Upstream.Endpoints.Evidence, "{id}") != 1 || strings.Count(profile.Upstream.Endpoints.BusinessOutcome, "{id}") != 1 {
		return errors.New("identifier endpoints must contain one {id} placeholder")
	}
	if strings.Contains(profile.Upstream.Endpoints.Catalog, "{id}") || strings.Contains(profile.Upstream.Endpoints.Observations, "{id}") || strings.Contains(profile.Upstream.Endpoints.BuildInfo, "{id}") {
		return errors.New("static endpoints cannot contain an identifier placeholder")
	}

	limits := profile.Limits
	if limits.TimeoutMilliseconds < 100 || limits.TimeoutMilliseconds > 30_000 ||
		limits.MaximumResponseBytes < 1024 || limits.MaximumResponseBytes > 1<<20 ||
		limits.MaximumRequestBytes < 1024 || limits.MaximumRequestBytes > 64<<10 ||
		limits.MaximumWindowSeconds < 1 || limits.MaximumWindowSeconds > 600 ||
		limits.RetentionSeconds < limits.MaximumWindowSeconds || limits.RetentionSeconds > 24*60*60 ||
		limits.MaximumItems < 1 || limits.MaximumItems > 50 {
		return errors.New("profile limits are outside supported bounds")
	}
	if len(profile.AllowedCallers) == 0 || len(profile.AllowedCallers) > 16 {
		return errors.New("allowed_callers must contain between 1 and 16 identities")
	}
	profile.callerSet = make(map[string]struct{}, len(profile.AllowedCallers))
	for _, caller := range profile.AllowedCallers {
		if !profileIDPattern.MatchString(caller) {
			return errors.New("allowed caller identity is invalid")
		}
		if _, exists := profile.callerSet[caller]; exists {
			return errors.New("allowed caller identities must be unique")
		}
		profile.callerSet[caller] = struct{}{}
	}
	if len(profile.SupportedEvidence) == 0 || len(profile.SupportedEvidence) > 16 || len(profile.Limitations) == 0 || len(profile.Limitations) > 16 {
		return errors.New("supported evidence and limitations must be bounded and non-empty")
	}
	if hasDuplicateStrings(profile.SupportedEvidence) {
		return errors.New("supported evidence entries must be unique")
	}
	return nil
}

func validateEndpoint(name, endpoint string) error {
	if endpoint == "" || !strings.HasPrefix(endpoint, "/") || strings.ContainsAny(endpoint, "?#") {
		return fmt.Errorf("%s endpoint is invalid", name)
	}
	withoutID := strings.ReplaceAll(endpoint, "{id}", "id")
	if path.Clean(withoutID) != withoutID || strings.Contains(withoutID, "//") {
		return fmt.Errorf("%s endpoint is unsafe", name)
	}
	return nil
}

func hasDuplicateStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func (s *profileStore) get(profileID string) (serviceProfile, bool) {
	profile, ok := s.profiles[profileID]
	return profile, ok
}

func (s *profileStore) maximumRequestBytes() int64 {
	values := make([]int64, 0, len(s.profiles))
	for _, profile := range s.profiles {
		values = append(values, profile.Limits.MaximumRequestBytes)
	}
	if len(values) == 0 {
		return 16 << 10
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[len(values)-1]
}
