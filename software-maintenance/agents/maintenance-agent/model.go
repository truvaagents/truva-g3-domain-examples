package main

import (
	"encoding/json"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/agentfailure"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
)

const (
	maintenanceAgentName  = "maintenance-agent"
	investigationTaskType = "maintenance.investigate"
	collectionFlow        = "collect-observations"
	defaultSummaryBytes   = 10000
)

type investigationInput struct {
	ProfileID       string   `json:"profile_id"`
	CaseID          string   `json:"case_id"`
	Objective       string   `json:"objective"`
	ObservationRefs []string `json:"observation_refs"`
}

type observationEvent struct {
	ProfileID     string `json:"profile_id"`
	ObservationID string `json:"observation_id"`
}

type investigationDecision struct {
	Status      string                        `json:"status"`
	Findings    []string                      `json:"findings"`
	Summary     string                        `json:"summary"`
	Assessment  *assessmentDecisionReference  `json:"assessment,omitempty"`
	Remediation *remediationDecisionReference `json:"remediation,omitempty"`
	NextCheck   *reconsiderationCondition     `json:"next_check,omitempty"`
}

type assessmentDecisionReference struct {
	AssessmentID  string `json:"assessment_id"`
	ObservationID string `json:"observation_id"`
	CheckID       string `json:"check_id"`
}

type remediationDecisionReference struct {
	PublicationID string `json:"publication_id"`
	OriginCaseID  string `json:"origin_case_id"`
}

// A stored condition is an operator-visible deadline, not scheduled work.
// Automatic reconsideration is disabled until its delivery contract is reviewed.
type reconsiderationCondition struct {
	Condition string    `json:"condition"`
	NotBefore time.Time `json:"not_before"`
}

type investigationResult struct {
	FailedOperations          map[string]json.RawMessage     `json:"failed_operations,omitempty"`
	ExecutionSuccess          bool                           `json:"execution_success"`
	ModelDecision             investigationDecision          `json:"model_decision"`
	SchemaVersion             int                            `json:"schema_version"`
	Status                    string                         `json:"status"`
	CoordinatorProfileSHA256  string                         `json:"coordinator_profile_sha256"`
	CaseID                    string                         `json:"case_id"`
	ObservationRefs           []string                       `json:"observation_refs"`
	Findings                  []string                       `json:"findings"`
	Summary                   string                         `json:"summary"`
	ExecutionID               string                         `json:"execution_id"`
	ToolsUsed                 []string                       `json:"tools_used"`
	Confidence                float64                        `json:"confidence"`
	RepairExecutionID         string                         `json:"repair_execution_id,omitempty"`
	ReviewID                  string                         `json:"review_id,omitempty"`
	PublicationID             string                         `json:"publication_id,omitempty"`
	PullRequestURL            string                         `json:"pull_request_url,omitempty"`
	Limitations               []string                       `json:"limitations"`
	LinkedAssessment          *lifecycle.AssessmentReference `json:"linked_assessment,omitempty"`
	LinkedAssessmentCheckedAt *time.Time                     `json:"linked_assessment_checked_at,omitempty"`
	LinkedRemediation         *linkedRemediationObservation  `json:"linked_remediation,omitempty"`
	NextCheck                 *reconsiderationCondition      `json:"next_check,omitempty"`
}

type caseRecord struct {
	AssessmentRecording      []assessmentRecording            `json:"assessment_recording,omitempty"`
	SchemaVersion            int                              `json:"schema_version"`
	CaseID                   string                           `json:"case_id"`
	CoordinatorProfileID     string                           `json:"coordinator_profile_id"`
	CoordinatorProfileSHA256 string                           `json:"coordinator_profile_sha256"`
	Objective                string                           `json:"objective"`
	ObservationRefs          []string                         `json:"observation_refs"`
	Source                   string                           `json:"source"`
	CurrentTaskID            string                           `json:"current_task_id"`
	AdmissionAttempts        int                              `json:"admission_attempts"`
	OccurrenceCount          int                              `json:"occurrence_count"`
	Status                   string                           `json:"status"`
	Disposition              string                           `json:"disposition,omitempty"`
	ExecutionID              string                           `json:"execution_id,omitempty"`
	Summary                  string                           `json:"summary,omitempty"`
	Result                   *investigationResult             `json:"result,omitempty"`
	LastErrorCode            string                           `json:"last_error_code,omitempty"`
	BoundaryFailure          *agentfailure.BoundaryFailure    `json:"boundary_failure,omitempty"`
	ValidationEvidence       *resultValidationEvidence        `json:"last_result_validation_failure,omitempty"`
	PublicationEffects       map[string]casePublicationEffect `json:"publication_effects,omitempty"`
	CreatedAt                time.Time                        `json:"created_at"`
	UpdatedAt                time.Time                        `json:"updated_at"`
	LastSeenAt               time.Time                        `json:"last_seen_at"`
}

// A retained effect proves only what was observed at publication time. It is
// not current PR state, a model decision, or transferable publication authority.
type casePublicationEffect struct {
	Publication          publicationStepResponse `json:"publication"`
	OriginExecutionID    string                  `json:"origin_execution_id"`
	ObservedExecutionIDs []string                `json:"observed_execution_ids"`
	RecordedAt           time.Time               `json:"recorded_at"`
}

// Records attributable effects even when final model prose fails validation.
// This is not an accepted model decision or a successful case result.
type resultValidationEvidence struct {
	ExecutionID string                   `json:"execution_id,omitempty"`
	Error       string                   `json:"error"`
	Publication *publicationStepResponse `json:"publication,omitempty"`
}

type admissionResult struct {
	CaseID string `json:"case_id"`
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Reused bool   `json:"reused"`
}

type observationSummary struct {
	ObservationID string    `json:"observation_id"`
	Operation     string    `json:"operation"`
	ObservedAt    time.Time `json:"observed_at"`
	HTTPStatus    int       `json:"http_status"`
	ErrorCode     string    `json:"error_code,omitempty"`
}

type observationWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type observationCoverage struct {
	Source           string `json:"source"`
	Complete         bool   `json:"complete"`
	RetentionSeconds int    `json:"retention_seconds"`
}

type observationsResponse struct {
	ProfileID      string               `json:"profile_id"`
	ProfileVersion string               `json:"profile_version"`
	Window         observationWindow    `json:"window"`
	Observations   []observationSummary `json:"observations"`
	ReturnedCount  int                  `json:"returned_count"`
	Truncated      bool                 `json:"truncated"`
	Coverage       observationCoverage  `json:"coverage"`
	CollectedAt    time.Time            `json:"collected_at"`
}

type serviceProfileResponse struct {
	ProfileID          string `json:"profile_id"`
	ProfileVersion     string `json:"profile_version"`
	AssessmentReady    bool   `json:"assessment_ready"`
	ContractReferences []struct {
		Kind     string `json:"kind"`
		Revision string `json:"revision"`
		Path     string `json:"path"`
	} `json:"contract_references"`
	ObservationConstraints struct {
		MaximumWindowSeconds int  `json:"maximum_window_seconds"`
		RetentionSeconds     int  `json:"retention_seconds"`
		MaximumItems         int  `json:"maximum_items"`
		CompleteStream       bool `json:"complete_stream"`
	} `json:"observation_constraints"`
	Limitations []string `json:"limitations"`
}

type collectionResult struct {
	Deferred          map[string]string `json:"deferred_observations,omitempty"`
	SampledOut        int               `json:"sampled_out"`
	RunID             string            `json:"admission_run_id,omitempty"`
	SchemaVersion     int               `json:"schema_version"`
	ProfileID         string            `json:"profile_id"`
	Window            observationWindow `json:"window"`
	Requests          int               `json:"requests"`
	Observed          int               `json:"observed"`
	Selected          int               `json:"selected"`
	Admitted          int               `json:"admitted"`
	Reused            int               `json:"reused"`
	CoverageComplete  bool              `json:"coverage_complete"`
	WatermarkAdvanced bool              `json:"watermark_advanced"`
	NextWatermark     time.Time         `json:"next_watermark,omitempty"`
	AssessmentReady   bool              `json:"assessment_ready"`
	Limitations       []string          `json:"limitations"`
}

type repairStepResponse struct {
	Status              string `json:"status"`
	RepairProfileSHA256 string `json:"repair_profile_sha256"`
	CaseID              string `json:"case_id"`
	ExecutionID         string `json:"execution_id"`
	Candidate           *struct {
		CandidateID string `json:"candidate_id"`
	} `json:"candidate,omitempty"`
}

type reviewStepResponse struct {
	Review struct {
		VerificationID      string    `json:"verification_id"`
		CreatedAt           time.Time `json:"created_at"`
		ExpiresAt           time.Time `json:"expires_at"`
		ReviewID            string    `json:"review_id"`
		CaseID              string    `json:"case_id"`
		CandidateID         string    `json:"candidate_id"`
		ReviewProfileSHA256 string    `json:"review_profile_sha256"`
		Status              string    `json:"status"`
	} `json:"review"`
}

type publicationStepResponse struct {
	PublicationID  string `json:"publication_id"`
	ProfileID      string `json:"profile_id"`
	ProfileSHA256  string `json:"profile_sha256"`
	CaseID         string `json:"case_id"`
	Status         string `json:"status"`
	Draft          bool   `json:"draft"`
	PullRequestURL string `json:"pull_request_url"`
}
