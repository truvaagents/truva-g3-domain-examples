package main

import "time"

type publicationLimits struct {
	TimeoutMilliseconds   int64 `json:"timeout_milliseconds"`
	MaximumFiles          int   `json:"maximum_files"`
	MaximumCandidateBytes int64 `json:"maximum_candidate_bytes"`
	MaximumTitleBytes     int   `json:"maximum_title_bytes"`
	MaximumBodyBytes      int   `json:"maximum_body_bytes"`
	RecordTTLSeconds      int64 `json:"record_ttl_seconds"`
	ClaimTTLSeconds       int64 `json:"claim_ttl_seconds"`
	MaximumOpenPRs        *int  `json:"maximum_open_prs,omitempty"`
	MaximumPRFiles        *int  `json:"maximum_pr_files,omitempty"`
	MaximumPRPatchBytes   *int  `json:"maximum_pr_patch_bytes,omitempty"`
}

type publicationProfile struct {
	SchemaVersion                  int               `json:"schema_version"`
	PublicationProfileID           string            `json:"publication_profile_id"`
	PublicationReady               bool              `json:"publication_ready"`
	RepositoryID                   string            `json:"repository_id"`
	WorkspaceProfileID             string            `json:"workspace_profile_id"`
	ExpectedSourceProfileSHA256    string            `json:"expected_source_profile_sha256"`
	ExpectedWorkspaceProfileSHA256 string            `json:"expected_workspace_profile_sha256"`
	ExpectedReviewProfileSHA256    string            `json:"expected_review_profile_sha256"`
	Owner                          string            `json:"owner"`
	Repository                     string            `json:"repository"`
	BaseRef                        string            `json:"base_ref"`
	BranchPrefix                   string            `json:"branch_prefix"`
	CommitAuthorName               string            `json:"commit_author_name"`
	CommitAuthorEmail              string            `json:"commit_author_email"`
	AllowedCallers                 []string          `json:"allowed_callers"`
	DeniedPaths                    []string          `json:"denied_paths"`
	Limits                         publicationLimits `json:"limits"`
	Limitations                    []string          `json:"limitations"`

	callerSet     map[string]struct{}
	profileSHA256 string
}

type publishRequest struct {
	ProfileID      string `json:"profile_id"`
	CaseID         string `json:"case_id"`
	CandidateID    string `json:"candidate_id"`
	VerificationID string `json:"verification_id"`
	ReviewID       string `json:"review_id"`
	Title          string `json:"title"`
	Body           string `json:"body"`
}

type getPublicationRequest struct {
	ProfileID     string `json:"profile_id"`
	CaseID        string `json:"case_id"`
	PublicationID string `json:"publication_id"`
}

type publicationRecord struct {
	SchemaVersion          int       `json:"schema_version"`
	PublicationID          string    `json:"publication_id"`
	ProfileID              string    `json:"profile_id"`
	ProfileSHA256          string    `json:"profile_sha256"`
	CaseID                 string    `json:"case_id"`
	RepositoryID           string    `json:"repository_id"`
	RepositoryOwner        string    `json:"repository_owner"`
	RepositoryName         string    `json:"repository_name"`
	WorkspaceID            string    `json:"workspace_id"`
	WorkspaceProfileID     string    `json:"workspace_profile_id"`
	WorkspaceProfileSHA256 string    `json:"workspace_profile_sha256"`
	ReviewProfileSHA256    string    `json:"review_profile_sha256"`
	SourceProfileSHA256    string    `json:"source_profile_sha256"`
	SnapshotID             string    `json:"snapshot_id"`
	CandidateID            string    `json:"candidate_id"`
	TestArtifactID         string    `json:"test_artifact_id"`
	VerificationID         string    `json:"verification_id"`
	ReviewID               string    `json:"review_id"`
	TitleSHA256            string    `json:"title_sha256"`
	BodySHA256             string    `json:"body_sha256"`
	BaseRef                string    `json:"base_ref"`
	BaseSHA                string    `json:"base_sha"`
	BaseTreeSHA            string    `json:"base_tree_sha"`
	CandidateTreeSHA       string    `json:"candidate_tree_sha,omitempty"`
	CommitSHA              string    `json:"commit_sha,omitempty"`
	Branch                 string    `json:"branch"`
	PullRequestID          int64     `json:"pull_request_id,omitempty"`
	PullRequestNumber      int       `json:"pull_request_number,omitempty"`
	PullRequestURL         string    `json:"pull_request_url,omitempty"`
	Draft                  bool      `json:"draft"`
	Status                 string    `json:"status"`
	FailureCode            string    `json:"failure_code,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	ExpiresAt              time.Time `json:"expires_at"`
	Limitations            []string  `json:"limitations"`
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

type workspaceBundle struct {
	Workspace    workspaceRecord    `json:"workspace"`
	Candidate    candidateRecord    `json:"candidate"`
	Verification verificationRecord `json:"verification"`
	Jobs         []jobRecord        `json:"jobs"`
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

type githubCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	} `json:"commit"`
}

type githubObject struct {
	SHA string `json:"sha"`
}

type githubTreeEntry struct {
	Path string  `json:"path"`
	Mode string  `json:"mode"`
	Type string  `json:"type"`
	SHA  *string `json:"sha"`
}

type githubPullRequest struct {
	ID      int64  `json:"id"`
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	State   string `json:"state"`
	Base    struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
	Head struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}
