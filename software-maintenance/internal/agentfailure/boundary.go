package agentfailure

import (
	"context"
	"encoding/json"
	"errors"
	"unicode"
	"unicode/utf8"

	"github.com/truvaagents/truva-g3/orchestration"
)

const GuidanceBudgetCode = "AGENT_GUIDANCE_BUDGET_EXCEEDED"
const GuidanceBudgetMessage = "Required agent guidance exceeded its configured admission budget"

// BoundaryFailure is application-approved diagnostic evidence. It establishes
// an orchestration failure independently of whether any tool step failed.
type BoundaryFailure struct {
	Code               string `json:"code"`
	ExecutionRequestID string `json:"execution_request_id,omitempty"`
	Operation          string `json:"operation,omitempty"`
	SkillRef           string `json:"skill_ref,omitempty"`
	ReconsiderWhen     string `json:"reconsider_when"`
}

func GuidanceBoundary(ctx context.Context, err error) *BoundaryFailure {
	if !errors.Is(err, orchestration.ErrSkillLimitExceeded) {
		return nil
	}
	failure := &BoundaryFailure{
		Code:               GuidanceBudgetCode,
		ExecutionRequestID: orchestration.GetRequestID(ctx),
		ReconsiderWhen:     "Effective guidance budget or content changes and required guidance passes admission",
	}
	var domain *orchestration.SkillDomainError
	if errors.As(err, &domain) {
		// Only these attributable, bounded fields are approved for disclosure.
		// Unknown stage/token/limit facts are omitted, rather than inferred.
		failure.Operation = diagnosticField(domain.Operation)
		if domain.Ref.Namespace != "" || domain.Ref.Name != "" {
			failure.SkillRef = diagnosticField(domain.Ref.String())
		}
	}
	failure.ExecutionRequestID = diagnosticField(failure.ExecutionRequestID)
	return failure
}

func diagnosticField(value string) string {
	if len(value) > 512 || !utf8.ValidString(value) {
		return ""
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return ""
		}
	}
	return value
}

// AddBoundaryDetails keeps failed dispatch evidence and artifact references.
func AddBoundaryDetails(details map[string]string, failure *BoundaryFailure) map[string]string {
	if failure == nil {
		return details
	}
	if details == nil {
		details = make(map[string]string)
	}
	encoded, _ := json.Marshal(failure) // Only string fields; cannot fail.
	details["boundary_failure"] = string(encoded)
	return details
}
