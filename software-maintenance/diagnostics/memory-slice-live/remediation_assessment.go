package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/orchestration"
)

// Operator assessment, not a production approval or a model self-assessment.
// Each true field attests that the reviewer inspected the exact captured final
// answer and compared its claims with both attributable capability results.
type remediationAnswerAssessment struct {
	RequestID                    string    `json:"request_id"`
	ResponseSHA256               string    `json:"response_sha256"`
	Reviewer                     string    `json:"reviewer"`
	ReviewedAt                   time.Time `json:"reviewed_at"`
	PRStatesMatchObservations    bool      `json:"pr_states_match_observations"`
	OriginalAttributionCorrect   bool      `json:"original_attribution_correct"`
	NoCurrentPublicationClaim    bool      `json:"no_current_publication_claim"`
	NoUnsupportedApplicability   bool      `json:"no_unsupported_applicability_claim"`
	NoUnsupportedResolution      bool      `json:"no_unsupported_resolution_claim"`
	SummaryAndFindingsConsistent bool      `json:"summary_and_findings_consistent"`
	EvidenceNote                 string    `json:"evidence_note"`
}

type remediationAssessmentCapture struct {
	RequestID      string                              `json:"request_id"`
	TraceID        string                              `json:"trace_id"`
	ResponseSHA256 string                              `json:"response_sha256"`
	PaidCalls      int                                 `json:"paid_calls"`
	ModelMode      string                              `json:"model_mode"`
	Response       *orchestration.OrchestratorResponse `json:"response"`
	Execution      *orchestration.ExecutionResult      `json:"execution"`
}

func validateRemediationAssessment(capture remediationAssessmentCapture, assessment remediationAnswerAssessment, now time.Time) error {
	if capture.ModelMode != "paid OpenRouter" || capture.PaidCalls < 1 || capture.PaidCalls > 4 || capture.Response == nil || capture.Execution == nil || !capture.Execution.Success || len(capture.Execution.Steps) != 2 {
		return errors.New("assessment requires a successful paid-model read capture; fixtures do not establish model judgment")
	}
	for _, step := range capture.Execution.Steps {
		if !step.Success || step.Capability != "get_remediation_state" {
			return errors.New("assessment capture did not complete both authorized reads")
		}
	}
	if strings.TrimSpace(capture.Response.Response) == "" {
		return errors.New("assessment capture has no final model answer")
	}
	responseDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(capture.Response.Response)))
	if capture.RequestID == "" || capture.Response.RequestID != capture.RequestID || assessment.RequestID != capture.RequestID || capture.ResponseSHA256 != responseDigest || assessment.ResponseSHA256 != responseDigest {
		return errors.New("assessment does not identify the exact captured model response and request")
	}
	if strings.TrimSpace(assessment.Reviewer) == "" || len(assessment.Reviewer) > 128 || assessment.ReviewedAt.IsZero() || assessment.ReviewedAt.After(now.Add(5*time.Minute)) || strings.TrimSpace(assessment.EvidenceNote) == "" || len(assessment.EvidenceNote) > 4096 {
		return errors.New("assessment needs a bounded reviewer, review time and evidence note")
	}
	if !assessment.PRStatesMatchObservations || !assessment.OriginalAttributionCorrect || !assessment.NoCurrentPublicationClaim || !assessment.NoUnsupportedApplicability || !assessment.NoUnsupportedResolution || !assessment.SummaryAndFindingsConsistent {
		return errors.New("model-answer acceptance is incomplete or rejected; retain the read evidence without claiming model judgment passed")
	}
	return nil
}

func verifyRecordedRemediationAssessment(capturePath, assessmentPath string) error {
	captureData, err := readBoundedAssessmentFile(capturePath, 4<<20)
	if err != nil {
		return err
	}
	var record struct {
		Diagnostic string                       `json:"diagnostic"`
		Data       remediationAssessmentCapture `json:"data"`
	}
	if err := json.Unmarshal(captureData, &record); err != nil || record.Diagnostic != "remediation_reads_verified" {
		return errors.New("capture must be one saved remediation_reads_verified record")
	}
	reviewData, err := readBoundedAssessmentFile(assessmentPath, 16<<10)
	if err != nil {
		return err
	}
	var assessment remediationAnswerAssessment
	decoder := json.NewDecoder(bytes.NewReader(reviewData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&assessment); err != nil {
		return fmt.Errorf("invalid assessment: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("assessment must contain exactly one JSON object")
	}
	if err := validateRemediationAssessment(record.Data, assessment, time.Now()); err != nil {
		return err
	}
	emit("remediation_model_assessment_verified", map[string]interface{}{"request_id": assessment.RequestID, "trace_id": record.Data.TraceID, "response_sha256": assessment.ResponseSHA256, "model_judgment_verified": true, "assessment_kind": "recorded_operator_review", "assessment": assessment, "paid_calls_in_this_command": 0})
	return nil
}

func readBoundedAssessmentFile(filename string, maximum int64) ([]byte, error) {
	// #nosec G304,G703 -- operator-selected local evidence file, not an agent capability.
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, errors.New("assessment evidence file is unreadable or exceeds its protocol bound")
	}
	return data, nil
}
