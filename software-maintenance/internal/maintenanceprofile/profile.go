package maintenanceprofile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/strictjson"
)

var (
	coordinatorNamePattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	coordinatorNamespacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	coordinatorSHA256Pattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	coordinatorVersionPattern   = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

func Load(filename string) (Profile, error) {
	if strings.TrimSpace(filename) == "" {
		return Profile{}, errors.New("MAINTENANCE_AGENT_PROFILE_FILE is required")
	}
	// #nosec G304,G703 -- operator-selected startup configuration, never request-controlled.
	data, err := os.ReadFile(filename)
	if err != nil {
		return Profile{}, fmt.Errorf("read coordinator profile: %w", err)
	}
	var profile Profile
	if err := strictjson.Decode(data, &profile); err != nil {
		return Profile{}, fmt.Errorf("decode coordinator profile: %w", err)
	}
	if err := Validate(&profile); err != nil {
		return Profile{}, fmt.Errorf("validate coordinator profile: %w", err)
	}
	limit, err := SummaryLimit(os.LookupEnv)
	if err != nil {
		return Profile{}, err
	}
	profile.Limits.MaximumSummaryBytes = limit
	// Effective result policy participates in case reuse and schedule identity.
	// Same resolved setting has the same identity whether defaulted or explicit.
	digest := sha256.Sum256(append(data, []byte("\x00maximum_summary_bytes="+strconv.Itoa(limit))...))
	profile.ProfileSHA256 = hex.EncodeToString(digest[:])
	return profile, nil
}

func SummaryLimit(lookup func(string) (string, bool)) (int, error) {
	const key = "MAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES"
	value, present := lookup(key)
	if !present {
		return DefaultSummaryBytes, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 65536 {
		return 0, errors.New(key + " must be an integer between 1 and 65536 UTF-8 bytes")
	}
	return limit, nil
}

func Validate(profile *Profile) error {
	if err := lifecycle.ValidatePolicy(profile.AssessmentPolicy); err != nil {
		return err
	}
	if len(profile.AssessmentAllowedCallers) > 8 {
		return errors.New("assessment caller policy exceeds its supported bound")
	}
	seenAssessmentCallers := make(map[string]bool)
	for _, caller := range profile.AssessmentAllowedCallers {
		if caller != "maintenance-operator" && caller != "maintenance-agent" || seenAssessmentCallers[caller] {
			return errors.New("assessment caller must be a unique configured operator or coordinator identity")
		}
		seenAssessmentCallers[caller] = true
	}
	if profile.SchemaVersion != 1 || !coordinatorNamePattern.MatchString(profile.CoordinatorProfileID) ||
		!coordinatorNamePattern.MatchString(profile.ServiceProfileID) ||
		!coordinatorNamePattern.MatchString(profile.RepositoryID) ||
		!coordinatorVersionPattern.MatchString(profile.ExpectedServiceProfileVersion) ||
		!coordinatorNamePattern.MatchString(profile.RepairProfileID) ||
		!coordinatorSHA256Pattern.MatchString(profile.ExpectedRepairProfileSHA256) ||
		!coordinatorNamePattern.MatchString(profile.ReviewProfileID) ||
		!coordinatorSHA256Pattern.MatchString(profile.ExpectedReviewProfileSHA256) ||
		!coordinatorNamePattern.MatchString(profile.PublicationProfileID) ||
		!coordinatorSHA256Pattern.MatchString(profile.ExpectedPublicationSHA256) {
		return errors.New("coordinator profile identity is invalid")
	}
	if len(profile.AllowedCallers) == 0 || len(profile.AllowedCallers) > 8 {
		return errors.New("coordinator allowed caller policy is invalid")
	}
	profile.CallerSet = make(map[string]struct{}, len(profile.AllowedCallers))
	for _, caller := range profile.AllowedCallers {
		if !coordinatorNamePattern.MatchString(caller) {
			return errors.New("coordinator allowed caller is invalid")
		}
		if _, exists := profile.CallerSet[caller]; exists {
			return errors.New("coordinator allowed callers must be unique")
		}
		profile.CallerSet[caller] = struct{}{}
	}
	collection := profile.Collection
	if !coordinatorNamespacePattern.MatchString(collection.PrivateRedisDeployment) ||
		!coordinatorNamePattern.MatchString(collection.ScheduleID) || collection.CronExpression != "* * * * *" ||
		collection.WindowSeconds < 60 || collection.WindowSeconds > 600 ||
		collection.OverlapSeconds < 0 || collection.OverlapSeconds > 120 ||
		collection.WindowSeconds+collection.OverlapSeconds > 600 ||
		collection.ItemLimit < 1 || collection.ItemLimit > 50 ||
		collection.SuccessfulSampleModulus < 1 || collection.SuccessfulSampleModulus > 10_000 {
		return errors.New("collection policy is outside supported bounds")
	}
	limits := profile.Limits
	findingLimit, err := responsecontract.ResolveFindingLimit(limits.FindingBytesJSON, 1024)
	if err != nil {
		return err
	}
	profile.Limits.MaximumFindingBytes = findingLimit
	if limits.MaximumRequestBytes < 1024 || limits.MaximumRequestBytes > 128<<10 ||
		limits.MaximumObjectiveBytes < 128 || limits.MaximumObjectiveBytes > 32<<10 ||
		limits.MaximumObservationRefs < 1 || limits.MaximumObservationRefs > 128 ||
		limits.MaximumFindings < 1 || limits.MaximumFindings > 64 ||
		limits.MaximumCollectionCalls < 1 || limits.MaximumCollectionCalls > 32 ||
		limits.MaximumAdmissionAttempts < 1 || limits.MaximumAdmissionAttempts > 8 {
		return errors.New("coordinator limits are outside supported bounds")
	}
	if len(profile.Limitations) == 0 || len(profile.Limitations) > 16 {
		return errors.New("coordinator limitations are invalid")
	}
	for _, limitation := range profile.Limitations {
		if limitation == "" || len(limitation) > 1024 || !ValidText(limitation) {
			return errors.New("coordinator profile contains an invalid limitation")
		}
	}
	return nil
}

func (profile Profile) AllowsAssessment(caller string) bool {
	for _, allowed := range profile.AssessmentAllowedCallers {
		if allowed == caller {
			return true
		}
	}
	return false
}

func (profile Profile) Allows(caller string) bool {
	_, ok := profile.CallerSet[caller]
	return ok
}

func ValidText(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}
