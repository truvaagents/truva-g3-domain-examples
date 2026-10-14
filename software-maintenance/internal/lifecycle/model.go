package lifecycle

import (
	"encoding/json"
	"time"
)

type Scope struct {
	CoordinatorProfileID string `json:"coordinator_profile_id"`
	ServiceProfileID     string `json:"service_profile_id"`
	RepositoryID         string `json:"repository_id"`
	EnvironmentID        string `json:"environment_id"`
}

type Rule struct {
	CheckID         string   `json:"check_id"`
	Operation       string   `json:"operation"`
	SignaturePaths  []string `json:"signature_paths"`
	ComparisonPaths []string `json:"comparison_paths"`
	// Empty strings are known values only on explicitly selected owner paths.
	AllowEmptyStringPaths []string `json:"allow_empty_string_paths,omitempty"`
}

type Policy struct {
	SchemaVersion int    `json:"schema_version"`
	Revision      string `json:"revision"`
	EnvironmentID string `json:"environment_id"`
	Rules         []Rule `json:"rules"`
}

type EvidenceReference struct {
	Capability     string    `json:"capability"`
	ProfileID      string    `json:"profile_id"`
	ProfileVersion string    `json:"profile_version"`
	PayloadSHA256  string    `json:"payload_sha256"`
	CollectedAt    time.Time `json:"collected_at"`
}

type Facts struct {
	Scope               Scope                      `json:"scope"`
	PolicySHA256        string                     `json:"policy_sha256"`
	CheckID             string                     `json:"check_id"`
	Operation           string                     `json:"operation"`
	ObservationID       string                     `json:"observation_id"`
	ObservedAt          time.Time                  `json:"observed_at"`
	Signature           map[string]json.RawMessage `json:"signature"`
	Comparison          map[string]json.RawMessage `json:"comparison"`
	ComparedFactsDigest string                     `json:"compared_facts_digest"`
	Evidence            []EvidenceReference        `json:"evidence"`
}

type RemediationReference struct {
	PublicationID            string `json:"publication_id"`
	OriginCaseID             string `json:"origin_case_id"`
	OriginExecutionID        string `json:"origin_execution_id"`
	PublicationProfileID     string `json:"publication_profile_id,omitempty"`
	PublicationProfileSHA256 string `json:"publication_profile_sha256,omitempty"`
	PullRequestURL           string `json:"pull_request_url"`
}

type Assessment struct {
	SchemaVersion     int                    `json:"schema_version"`
	AssessmentID      string                 `json:"assessment_id"`
	IssueID           string                 `json:"issue_id"`
	Version           uint64                 `json:"version"`
	OriginCaseID      string                 `json:"origin_case_id"`
	OriginExecutionID string                 `json:"origin_execution_id"`
	Disposition       string                 `json:"disposition"`
	Summary           string                 `json:"summary"`
	Facts             Facts                  `json:"facts"`
	Remediations      []RemediationReference `json:"remediations"`
	AssessedAt        time.Time              `json:"assessed_at"`
	RevalidateAfter   time.Time              `json:"revalidate_after"`
	SupersedesID      string                 `json:"supersedes_id,omitempty"`
}

type Issue struct {
	SchemaVersion       int                        `json:"schema_version"`
	IssueID             string                     `json:"issue_id"`
	Scope               Scope                      `json:"scope"`
	PolicySHA256        string                     `json:"policy_sha256"`
	CheckID             string                     `json:"check_id"`
	Signature           map[string]json.RawMessage `json:"signature"`
	Version             uint64                     `json:"version"`
	CurrentAssessmentID string                     `json:"current_assessment_id"`
	UpdatedAt           time.Time                  `json:"updated_at"`
}

type Match struct {
	Assessment          AssessmentReference `json:"assessment"`
	Eligible            bool                `json:"eligible"`
	Reason              string              `json:"reason"`
	ComparedFactsDigest string              `json:"compared_facts_digest,omitempty"`
}

// Lookup is a derived locator view. Full, exact facts and receipts are read
// through get_assessment rather than duplicated across every history candidate.
type AssessmentReference struct {
	AssessmentID      string    `json:"assessment_id"`
	IssueID           string    `json:"issue_id"`
	Version           uint64    `json:"version"`
	OriginCaseID      string    `json:"origin_case_id"`
	OriginExecutionID string    `json:"origin_execution_id"`
	Disposition       string    `json:"disposition"`
	Summary           string    `json:"summary"`
	RevalidateAfter   time.Time `json:"revalidate_after"`
}

type Lookup struct {
	CurrentIssueID           string    `json:"current_issue_id,omitempty"`
	CurrentIssueVersion      *uint64   `json:"current_issue_version,omitempty"`
	CurrentFactsDigest       string    `json:"current_facts_digest,omitempty"`
	CoordinatorProfileID     string    `json:"coordinator_profile_id"`
	CoordinatorProfileSHA256 string    `json:"coordinator_profile_sha256"`
	ObservationID            string    `json:"observation_id,omitempty"`
	CheckID                  string    `json:"check_id,omitempty"`
	PolicyConfigured         bool      `json:"policy_configured"`
	Candidates               []Match   `json:"candidates"`
	Truncated                bool      `json:"truncated"`
	CheckedAt                time.Time `json:"checked_at"`
	Limitations              []string  `json:"limitations"`
}
