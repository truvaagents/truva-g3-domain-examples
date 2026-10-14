package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

type commandProfile struct {
	ID   string   `json:"id"`
	Argv []string `json:"argv"`
}

type workspaceLimits struct {
	WorkspaceTTLSeconds      int64 `json:"workspace_ttl_seconds"`
	MaximumSnapshotBytes     int64 `json:"maximum_snapshot_bytes"`
	MaximumSnapshotFiles     int   `json:"maximum_snapshot_files"`
	MaximumCandidateFiles    int   `json:"maximum_candidate_files"`
	MaximumCandidateBytes    int64 `json:"maximum_candidate_bytes"`
	MaximumFileBytes         int64 `json:"maximum_file_bytes"`
	MaximumJobSeconds        int64 `json:"maximum_job_seconds"`
	MaximumJobLogBytes       int64 `json:"maximum_job_log_bytes"`
	MaximumArtifactReadBytes int64 `json:"maximum_artifact_read_bytes"`
}

type workspaceProfile struct {
	SchemaVersion          int              `json:"schema_version"`
	WorkspaceProfileID     string           `json:"workspace_profile_id"`
	RepositoryID           string           `json:"repository_id"`
	VerificationReady      bool             `json:"verification_ready"`
	RunnerImage            string           `json:"runner_image"`
	WorkspacePVC           string           `json:"workspace_pvc"`
	AllowedCallers         []string         `json:"allowed_callers"`
	CandidateStageCallers  []string         `json:"candidate_stage_callers"`
	DeniedPaths            []string         `json:"denied_paths"`
	ExperimentCommands     []commandProfile `json:"experiment_commands"`
	ReproductionCommandID  string           `json:"reproduction_command_id"`
	VerificationCommandIDs []string         `json:"verification_command_ids"`
	Limits                 workspaceLimits  `json:"limits"`
	Limitations            []string         `json:"limitations"`

	callerSet      map[string]struct{}
	stageCallerSet map[string]struct{}
	commandByID    map[string]commandProfile
	profileSHA256  string
}

type createWorkspaceRequest struct {
	ProfileID  string `json:"profile_id"`
	CaseID     string `json:"case_id"`
	SnapshotID string `json:"snapshot_id"`
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

type candidateFile struct {
	Path                 string  `json:"path"`
	BaseSHA256           string  `json:"base_sha256"`
	Mode                 string  `json:"mode"`
	ContentBase64        string  `json:"content_base64,omitempty"`
	ContentText          *string `json:"content_text,omitempty"`
	contentBase64Present bool
	Delete               bool `json:"delete,omitempty"`
}

// UnmarshalJSON retains encoding presence so even empty dual inputs are rejected.
// Canonical stored candidates still contain only base64, keeping identity stable.
func (file *candidateFile) UnmarshalJSON(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("candidate JSON must contain valid UTF-8")
	}
	type plain candidateFile
	var value plain
	if err := decodeWorkspaceStrictBytes(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"content_base64", "content_text"} {
		if raw, ok := fields[key]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("candidate content encoding cannot be null")
		}
	}
	_, value.contentBase64Present = fields["content_base64"]
	*file = candidateFile(value)
	return nil
}

type workspacePolicyFailure struct {
	code    string
	message string
}

func (failure *workspacePolicyFailure) Error() string { return failure.message }

func policyFailure(code, message string) error {
	return &workspacePolicyFailure{code: code, message: message}
}

func validateCandidateInputContent(file candidateFile) error {
	hasBase64 := file.contentBase64Present || file.ContentBase64 != ""
	hasText := file.ContentText != nil
	if file.Delete {
		if hasBase64 || hasText {
			return errors.New("deleted candidate files cannot have content")
		}
		return nil
	}
	if hasBase64 == hasText {
		return errors.New("candidate file requires exactly one content encoding")
	}
	return nil
}

type stageCandidateRequest struct {
	WorkspaceID  string          `json:"workspace_id"`
	CaseID       string          `json:"case_id"`
	ExpectedTest string          `json:"expected_test"`
	Changes      []candidateFile `json:"changes"`
	Tests        []candidateFile `json:"tests"`
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
	Files          []candidateFile `json:"files"`
}

type candidateResponse struct {
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

type runExperimentRequest struct {
	WorkspaceID    string `json:"workspace_id"`
	CaseID         string `json:"case_id"`
	CandidateID    string `json:"candidate_id,omitempty"`
	Target         string `json:"target"`
	CommandID      string `json:"command_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type runVerificationRequest struct {
	CandidateID string `json:"candidate_id"`
	CaseID      string `json:"case_id"`
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

type getJobRequest struct {
	JobID       string `json:"job_id"`
	CaseID      string `json:"case_id"`
	WaitSeconds int    `json:"wait_seconds,omitempty"`
}

type readArtifactRequest struct {
	ArtifactID string `json:"artifact_id"`
	CaseID     string `json:"case_id"`
	ByteStart  int64  `json:"byte_start"`
	ByteLimit  int64  `json:"byte_limit"`
	Format     string `json:"format,omitempty"`
}

type artifactRecord struct {
	SchemaVersion int       `json:"schema_version"`
	ArtifactID    string    `json:"artifact_id"`
	JobID         string    `json:"job_id"`
	ProfileID     string    `json:"profile_id"`
	CaseID        string    `json:"case_id"`
	SHA256        string    `json:"sha256"`
	Bytes         int64     `json:"bytes"`
	MediaType     string    `json:"media_type"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type readArtifactResponse struct {
	ArtifactID    string `json:"artifact_id"`
	SHA256        string `json:"sha256"`
	MediaType     string `json:"media_type"`
	ByteStart     int64  `json:"byte_start"`
	ByteEnd       int64  `json:"byte_end"`
	TotalBytes    int64  `json:"total_bytes"`
	ContentBase64 string `json:"content_base64"`
	Truncated     bool   `json:"truncated"`
}

// Explicit text-mode response. The legacy/base64 DTO is unchanged.
type textArtifactResponse struct {
	ArtifactID    string  `json:"artifact_id"`
	SHA256        string  `json:"sha256"`
	MediaType     string  `json:"media_type"`
	ByteStart     int64   `json:"byte_start"`
	ByteEnd       int64   `json:"byte_end"`
	TotalBytes    int64   `json:"total_bytes"`
	ContentText   *string `json:"content_text,omitempty"`
	ContentBase64 *string `json:"content_base64,omitempty"`
	Encoding      string  `json:"encoding"`
	Truncated     bool    `json:"truncated"`
}

type publicationBundleRequest struct {
	CaseID         string `json:"case_id"`
	CandidateID    string `json:"candidate_id"`
	VerificationID string `json:"verification_id"`
}

type publicationBundleResponse struct {
	Workspace    workspaceRecord    `json:"workspace"`
	Candidate    candidateRecord    `json:"candidate"`
	Verification verificationRecord `json:"verification"`
	Jobs         []jobRecord        `json:"jobs"`
}

type reviewCandidateFilePreview struct {
	Path          string `json:"path"`
	BaseSHA256    string `json:"base_sha256"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Operation     string `json:"operation"`
	Test          bool   `json:"test"`
	TextAvailable bool   `json:"text_available"`
	ContentText   string `json:"content_text,omitempty"`
	Truncated     bool   `json:"truncated"`
}

type reviewBundleResponse struct {
	Workspace             workspaceRecord              `json:"workspace"`
	Candidate             candidateRecord              `json:"candidate"`
	Verification          verificationRecord           `json:"verification"`
	Jobs                  []jobRecord                  `json:"jobs"`
	CandidateFilePreviews []reviewCandidateFilePreview `json:"candidate_file_previews"`
}

type sourceSnapshotChunk struct {
	SnapshotID          string `json:"snapshot_id"`
	RepositoryID        string `json:"repository_id"`
	SourceProfileSHA256 string `json:"source_profile_sha256"`
	AssessmentReady     bool   `json:"assessment_ready"`
	RevisionSHA         string `json:"revision_sha"`
	TreeSHA             string `json:"tree_sha"`
	ArchiveSHA256       string `json:"archive_sha256"`
	Format              string `json:"format"`
	ByteStart           int64  `json:"byte_start"`
	ByteEnd             int64  `json:"byte_end"`
	TotalBytes          int64  `json:"total_bytes"`
	ContentBase64       string `json:"content_base64"`
	Truncated           bool   `json:"truncated"`
}
