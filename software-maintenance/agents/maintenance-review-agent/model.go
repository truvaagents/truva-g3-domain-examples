package main

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
)

type reviewLimits struct {
	MaximumRequestBytes   int64           `json:"maximum_request_bytes"`
	MaximumObjectiveBytes int             `json:"maximum_objective_bytes"`
	MaximumSummaryBytes   int             `json:"-"`
	MaximumCandidateFiles int             `json:"maximum_candidate_files"`
	MaximumCandidateBytes int64           `json:"maximum_candidate_bytes"`
	MaximumFindings       int             `json:"maximum_findings"`
	FindingBytesJSON      json.RawMessage `json:"maximum_finding_bytes,omitempty"`
	MaximumFindingBytes   int             `json:"-"`
	MaximumReferenceBytes int             `json:"maximum_reference_bytes"`
	ReceiptTTLSeconds     int64           `json:"receipt_ttl_seconds"`
}

func (limits reviewLimits) summaryLimit() int {
	if limits.MaximumSummaryBytes > 0 {
		return limits.MaximumSummaryBytes
	}
	return responsecontract.DefaultSummaryBytes
}

func (limits reviewLimits) findingLimit() int {
	if limits.MaximumFindingBytes > 0 {
		return limits.MaximumFindingBytes
	}
	return limits.MaximumReferenceBytes
}

type reviewProfile struct {
	SchemaVersion                  int          `json:"schema_version"`
	ReviewProfileID                string       `json:"review_profile_id"`
	ReviewReady                    bool         `json:"review_ready"`
	ServiceProfileID               string       `json:"service_profile_id"`
	WorkspaceProfileID             string       `json:"workspace_profile_id"`
	ExpectedServiceProfileVersion  string       `json:"expected_service_profile_version"`
	ExpectedSourceProfileSHA256    string       `json:"expected_source_profile_sha256"`
	ExpectedWorkspaceProfileSHA256 string       `json:"expected_workspace_profile_sha256"`
	RepositoryID                   string       `json:"repository_id"`
	AllowedCallers                 []string     `json:"allowed_callers"`
	Limits                         reviewLimits `json:"limits"`
	Limitations                    []string     `json:"limitations"`

	callerSet     map[string]struct{}
	profileSHA256 string
}

type reviewRequest struct {
	ProfileID      string `json:"profile_id"`
	CaseID         string `json:"case_id"`
	CandidateID    string `json:"candidate_id"`
	VerificationID string `json:"verification_id"`
	Objective      string `json:"objective"`
}

type reviewWireRequest struct {
	Data           *reviewRequest `json:"data,omitempty"`
	ProfileID      string         `json:"profile_id,omitempty"`
	CaseID         string         `json:"case_id,omitempty"`
	CandidateID    string         `json:"candidate_id,omitempty"`
	VerificationID string         `json:"verification_id,omitempty"`
	Objective      string         `json:"objective,omitempty"`
}

func (request reviewWireRequest) normalized() (reviewRequest, error) {
	direct := reviewRequest{
		ProfileID: request.ProfileID, CaseID: request.CaseID, CandidateID: request.CandidateID,
		VerificationID: request.VerificationID, Objective: request.Objective,
	}
	hasDirect := direct.ProfileID != "" || direct.CaseID != "" || direct.CandidateID != "" || direct.VerificationID != "" || direct.Objective != ""
	if request.Data != nil && hasDirect {
		return reviewRequest{}, errors.New("wrapped and direct review fields cannot be combined")
	}
	if request.Data != nil {
		return *request.Data, nil
	}
	return direct, nil
}

type getReviewRequest struct {
	CaseID   string `json:"case_id"`
	ReviewID string `json:"review_id"`
}

type reviewDecision struct {
	Status           string   `json:"status"`
	BlockingFindings []string `json:"blocking_findings"`
	Summary          string   `json:"summary"`
}

type reviewReceipt struct {
	SchemaVersion          int       `json:"schema_version"`
	ReviewID               string    `json:"review_id"`
	CaseID                 string    `json:"case_id"`
	CandidateID            string    `json:"candidate_id"`
	TestArtifactID         string    `json:"test_artifact_id"`
	VerificationID         string    `json:"verification_id"`
	WorkspaceProfileSHA256 string    `json:"workspace_profile_sha256"`
	ReviewProfileSHA256    string    `json:"review_profile_sha256"`
	Status                 string    `json:"status"`
	ContractReferences     []string  `json:"contract_references"`
	EvidenceReferences     []string  `json:"evidence_references"`
	BlockingFindings       []string  `json:"blocking_findings"`
	ReviewerExecutionID    string    `json:"reviewer_execution_id"`
	CreatedAt              time.Time `json:"created_at"`
	ExpiresAt              time.Time `json:"expires_at"`
}

type reviewResponse struct {
	FailedOperations    map[string]json.RawMessage `json:"failed_operations,omitempty"`
	ExecutionSuccess    bool                       `json:"execution_success"`
	ExecutionID         string                     `json:"execution_id"`
	BlockingFindings    []string                   `json:"blocking_findings"`
	Review              *reviewReceipt             `json:"review,omitempty"`
	ReviewID            string                     `json:"review_id,omitempty"`
	CaseID              string                     `json:"case_id"`
	CandidateID         string                     `json:"candidate_id"`
	VerificationID      string                     `json:"verification_id"`
	ReviewProfileSHA256 string                     `json:"review_profile_sha256"`
	Status              string                     `json:"status"`
	Summary             string                     `json:"summary"`
	ToolsUsed           []string                   `json:"tools_used"`
	Confidence          float64                    `json:"confidence"`
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
	Limitations        []string            `json:"limitations"`
}

type workspaceFile struct {
	Path          string `json:"path"`
	BaseSHA256    string `json:"base_sha256"`
	Mode          string `json:"mode"`
	ContentBase64 string `json:"content_base64,omitempty"`
	Delete        bool   `json:"delete,omitempty"`
}

type workspaceRecord struct {
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

type candidateRecord struct {
	SchemaVersion  int             `json:"schema_version"`
	CandidateID    string          `json:"candidate_id"`
	TestArtifactID string          `json:"test_artifact_id"`
	WorkspaceID    string          `json:"workspace_id"`
	ProfileID      string          `json:"profile_id"`
	ProfileSHA256  string          `json:"profile_sha256"`
	CaseID         string          `json:"case_id"`
	RepositoryID   string          `json:"repository_id"`
	SnapshotID     string          `json:"snapshot_id"`
	RevisionSHA    string          `json:"revision_sha"`
	ExpectedTest   string          `json:"expected_test"`
	ChangeCount    int             `json:"change_count"`
	TestCount      int             `json:"test_count"`
	CandidateBytes int64           `json:"candidate_bytes"`
	ProposedBy     string          `json:"proposed_by"`
	CreatedAt      time.Time       `json:"created_at"`
	ExpiresAt      time.Time       `json:"expires_at"`
	Files          []workspaceFile `json:"files"`
}

type verificationRecord struct {
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

type jobRecord struct {
	SchemaVersion     int       `json:"schema_version"`
	JobID             string    `json:"job_id"`
	KubernetesJob     string    `json:"kubernetes_job"`
	WorkspaceID       string    `json:"workspace_id"`
	CandidateID       string    `json:"candidate_id,omitempty"`
	VerificationID    string    `json:"verification_id,omitempty"`
	ProfileID         string    `json:"profile_id"`
	ProfileSHA256     string    `json:"profile_sha256"`
	CaseID            string    `json:"case_id"`
	Target            string    `json:"target"`
	CommandID         string    `json:"command_id"`
	CommandSHA256     string    `json:"command_sha256"`
	RunnerImage       string    `json:"runner_image"`
	Status            string    `json:"status"`
	ExitCode          *int32    `json:"exit_code,omitempty"`
	ArtifactID        string    `json:"artifact_id,omitempty"`
	ArtifactSHA256    string    `json:"artifact_sha256,omitempty"`
	ArtifactBytes     int64     `json:"artifact_bytes,omitempty"`
	ArtifactTruncated bool      `json:"artifact_truncated"`
	TimedOut          bool      `json:"timed_out"`
	CreatedAt         time.Time `json:"created_at"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	CompletedAt       time.Time `json:"completed_at,omitempty"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type reviewBundle struct {
	Workspace    workspaceRecord    `json:"workspace"`
	Candidate    candidateRecord    `json:"candidate"`
	Verification verificationRecord `json:"verification"`
	Jobs         []jobRecord        `json:"jobs"`
}
