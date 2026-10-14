package main

import (
	"encoding/json"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
)

type repairLimits struct {
	MaximumRequestBytes   int64           `json:"maximum_request_bytes"`
	MaximumObjectiveBytes int             `json:"maximum_objective_bytes"`
	MaximumSummaryBytes   int             `json:"-"`
	MaximumFindings       int             `json:"maximum_findings"`
	FindingBytesJSON      json.RawMessage `json:"maximum_finding_bytes,omitempty"`
	MaximumFindingBytes   int             `json:"-"`
	MaximumReferenceBytes int             `json:"maximum_reference_bytes"`
}

func (limits repairLimits) summaryLimit() int {
	if limits.MaximumSummaryBytes > 0 {
		return limits.MaximumSummaryBytes
	}
	return responsecontract.DefaultSummaryBytes
}

func (limits repairLimits) findingLimit() int {
	if limits.MaximumFindingBytes > 0 {
		return limits.MaximumFindingBytes
	}
	return limits.MaximumReferenceBytes
}

type repairProfile struct {
	SchemaVersion                  int          `json:"schema_version"`
	RepairProfileID                string       `json:"repair_profile_id"`
	RepairReady                    bool         `json:"repair_ready"`
	ServiceProfileID               string       `json:"service_profile_id"`
	WorkspaceProfileID             string       `json:"workspace_profile_id"`
	ExpectedServiceProfileVersion  string       `json:"expected_service_profile_version"`
	ExpectedSourceProfileSHA256    string       `json:"expected_source_profile_sha256"`
	ExpectedWorkspaceProfileSHA256 string       `json:"expected_workspace_profile_sha256"`
	RepositoryID                   string       `json:"repository_id"`
	AllowedCallers                 []string     `json:"allowed_callers"`
	Limits                         repairLimits `json:"limits"`
	Limitations                    []string     `json:"limitations"`

	callerSet     map[string]struct{}
	profileSHA256 string
}

type repairRequest struct {
	ProfileID     string `json:"profile_id"`
	CaseID        string `json:"case_id"`
	ObservationID string `json:"observation_id"`
	SnapshotID    string `json:"snapshot_id"`
	Objective     string `json:"objective"`
}

type repairWireRequest struct {
	Data          *repairRequest `json:"data,omitempty"`
	ProfileID     string         `json:"profile_id,omitempty"`
	CaseID        string         `json:"case_id,omitempty"`
	ObservationID string         `json:"observation_id,omitempty"`
	SnapshotID    string         `json:"snapshot_id,omitempty"`
	Objective     string         `json:"objective,omitempty"`
}

func (request repairWireRequest) normalized() (repairRequest, bool) {
	direct := repairRequest{ProfileID: request.ProfileID, CaseID: request.CaseID, ObservationID: request.ObservationID, SnapshotID: request.SnapshotID, Objective: request.Objective}
	hasDirect := direct.ProfileID != "" || direct.CaseID != "" || direct.ObservationID != "" || direct.SnapshotID != "" || direct.Objective != ""
	if request.Data != nil && hasDirect {
		return repairRequest{}, false
	}
	if request.Data != nil {
		return *request.Data, true
	}
	return direct, true
}

type repairDecision struct {
	Status   string   `json:"status"`
	Findings []string `json:"findings"`
	Summary  string   `json:"summary"`
}

type contractReference struct {
	Kind     string `json:"kind"`
	Revision string `json:"revision"`
	Path     string `json:"path"`
}

type serviceProfileResponse struct {
	ProfileID          string              `json:"profile_id"`
	ProfileVersion     string              `json:"profile_version"`
	AssessmentReady    bool                `json:"assessment_ready"`
	ContractReferences []contractReference `json:"contract_references"`
}

type workspaceReference struct {
	SchemaVersion       int       `json:"schema_version"`
	WorkspaceID         string    `json:"workspace_id"`
	ProfileID           string    `json:"profile_id"`
	ProfileSHA256       string    `json:"profile_sha256"`
	SourceProfileSHA256 string    `json:"source_profile_sha256"`
	CaseID              string    `json:"case_id"`
	RepositoryID        string    `json:"repository_id"`
	SnapshotID          string    `json:"snapshot_id"`
	RevisionSHA         string    `json:"revision_sha"`
	TreeSHA             string    `json:"tree_sha"`
	ArchiveSHA256       string    `json:"archive_sha256"`
	VerificationReady   bool      `json:"verification_ready"`
	CreatedAt           time.Time `json:"created_at"`
	ExpiresAt           time.Time `json:"expires_at"`
	Limitations         []string  `json:"limitations"`
}

type candidateReference struct {
	SchemaVersion  int       `json:"schema_version"`
	CandidateID    string    `json:"candidate_id"`
	TestArtifactID string    `json:"test_artifact_id"`
	WorkspaceID    string    `json:"workspace_id"`
	ProfileID      string    `json:"profile_id"`
	ProfileSHA256  string    `json:"profile_sha256"`
	CaseID         string    `json:"case_id"`
	RepositoryID   string    `json:"repository_id"`
	SnapshotID     string    `json:"snapshot_id"`
	RevisionSHA    string    `json:"revision_sha"`
	ExpectedTest   string    `json:"expected_test"`
	ChangeCount    int       `json:"change_count"`
	TestCount      int       `json:"test_count"`
	CandidateBytes int64     `json:"candidate_bytes"`
	ProposedBy     string    `json:"proposed_by"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type verificationReference struct {
	SchemaVersion        int       `json:"schema_version"`
	VerificationID       string    `json:"verification_id"`
	CandidateID          string    `json:"candidate_id"`
	ProfileID            string    `json:"profile_id"`
	ProfileSHA256        string    `json:"profile_sha256"`
	ReproductionJobID    string    `json:"reproduction_job_id"`
	CandidateJobIDs      []string  `json:"candidate_job_ids"`
	Status               string    `json:"status"`
	ExpectedTestObserved bool      `json:"expected_test_observed"`
	SuitePassed          bool      `json:"suite_passed"`
	PublicationEligible  bool      `json:"publication_eligible"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type repairResponse struct {
	FailedOperations    map[string]json.RawMessage `json:"failed_operations,omitempty"`
	ExecutionSuccess    bool                       `json:"execution_success"`
	SchemaVersion       int                        `json:"schema_version"`
	Status              string                     `json:"status"`
	RepairProfileSHA256 string                     `json:"repair_profile_sha256"`
	CaseID              string                     `json:"case_id"`
	ObservationID       string                     `json:"observation_id"`
	SnapshotID          string                     `json:"snapshot_id"`
	Workspace           *workspaceReference        `json:"workspace,omitempty"`
	Candidate           *candidateReference        `json:"candidate,omitempty"`
	Verification        *verificationReference     `json:"verification,omitempty"`
	WorkspaceID         string                     `json:"workspace_id,omitempty"`
	CandidateID         string                     `json:"candidate_id,omitempty"`
	TestArtifactID      string                     `json:"test_artifact_id,omitempty"`
	VerificationID      string                     `json:"verification_id,omitempty"`
	ContractReferences  []string                   `json:"contract_references"`
	Findings            []string                   `json:"findings"`
	Summary             string                     `json:"summary"`
	ExecutionID         string                     `json:"execution_id"`
	ToolsUsed           []string                   `json:"tools_used"`
	Confidence          float64                    `json:"confidence"`
	Limitations         []string                   `json:"limitations"`
}
