package maintenanceprofile

import (
	"encoding/json"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
)

const DefaultSummaryBytes = 10000

type Limits struct {
	MaximumRequestBytes      int64           `json:"maximum_request_bytes"`
	MaximumObjectiveBytes    int             `json:"maximum_objective_bytes"`
	MaximumObservationRefs   int             `json:"maximum_observation_refs"`
	MaximumFindings          int             `json:"maximum_findings"`
	FindingBytesJSON         json.RawMessage `json:"maximum_finding_bytes,omitempty"`
	MaximumFindingBytes      int             `json:"-"`
	MaximumSummaryBytes      int             `json:"-"`
	MaximumCollectionCalls   int             `json:"maximum_collection_calls"`
	MaximumAdmissionAttempts int             `json:"maximum_admission_attempts"`
}

func (limits Limits) SummaryLimit() int {
	if limits.MaximumSummaryBytes > 0 {
		return limits.MaximumSummaryBytes
	}
	return DefaultSummaryBytes
}

func (limits Limits) FindingLimit() int {
	if limits.MaximumFindingBytes > 0 {
		return limits.MaximumFindingBytes
	}
	return 1024
}

type Collection struct {
	Enabled                 bool   `json:"enabled"`
	PrivateRedisDeployment  string `json:"private_redis_deployment"`
	ScheduleID              string `json:"schedule_id"`
	CronExpression          string `json:"cron_expression"`
	WindowSeconds           int    `json:"window_seconds"`
	OverlapSeconds          int    `json:"overlap_seconds"`
	ItemLimit               int    `json:"item_limit"`
	SuccessfulSampleModulus uint64 `json:"successful_sample_modulus"`
}

type Profile struct {
	SchemaVersion                 int               `json:"schema_version"`
	CoordinatorProfileID          string            `json:"coordinator_profile_id"`
	InvestigationReady            bool              `json:"investigation_ready"`
	ServiceProfileID              string            `json:"service_profile_id"`
	RepositoryID                  string            `json:"repository_id"`
	ExpectedServiceProfileVersion string            `json:"expected_service_profile_version"`
	RepairProfileID               string            `json:"repair_profile_id"`
	ExpectedRepairProfileSHA256   string            `json:"expected_repair_profile_sha256"`
	ReviewProfileID               string            `json:"review_profile_id"`
	ExpectedReviewProfileSHA256   string            `json:"expected_review_profile_sha256"`
	PublicationProfileID          string            `json:"publication_profile_id"`
	ExpectedPublicationSHA256     string            `json:"expected_publication_profile_sha256"`
	AllowedCallers                []string          `json:"allowed_callers"`
	AssessmentAllowedCallers      []string          `json:"assessment_allowed_callers,omitempty"`
	AssessmentPolicy              *lifecycle.Policy `json:"assessment_policy,omitempty"`
	Collection                    Collection        `json:"collection"`
	Limits                        Limits            `json:"limits"`
	Limitations                   []string          `json:"limitations"`

	CallerSet     map[string]struct{} `json:"-"`
	ProfileSHA256 string              `json:"-"`
}
