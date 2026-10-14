package responsecontract

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestValidationDetailsPreserveEvidenceAndCause(t *testing.T) {
	cause := errors.New("untrusted upstream prose")
	err := Invalid("DECISION_JSON_INVALID", "decision", "One JSON object with status, summary and findings", cause)
	if !errors.Is(err, cause) || err.Error() != cause.Error() {
		t.Fatal("original cause lost")
	}
	for _, tc := range []struct {
		err   error
		code  string
		field string
	}{
		{err, "DECISION_JSON_INVALID", "decision"},
		{fmt.Errorf("wrapped: %w", &FieldError{Field: "summary", Actual: 10001, Limit: 10000}), "SUMMARY_TOO_LONG", "summary"},
		{&FieldError{Field: "findings", Actual: 1025, Limit: 1024}, "FINDING_TOO_LONG", "findings"},
		{&FieldError{Field: "blocking_findings", Actual: 1025, Limit: 1024}, "FINDING_TOO_LONG", "blocking_findings"},
	} {
		details := AddDetails(map[string]string{"execution_id": "child", "failed_operations": "retained", "candidate_id": "candidate"}, tc.err)
		if details["validation_code"] != tc.code || details["field"] != tc.field || details["execution_id"] != "child" || details["failed_operations"] != "retained" || details["candidate_id"] != "candidate" {
			t.Fatalf("incorrect details: %v", details)
		}
		for _, value := range details {
			if strings.Contains(value, cause.Error()) {
				t.Fatal("raw cause promoted to contract guidance")
			}
		}
		if message := FailureMessage("fallback", tc.err); message == "fallback" || strings.Contains(message, cause.Error()) {
			t.Fatalf("incorrect public message: %q", message)
		}
	}
	if details := AddDetails(nil, cause); details != nil {
		t.Fatalf("untyped error classified without evidence: %v", details)
	}
	if message := FailureMessage("fallback", cause); message != "fallback" {
		t.Fatalf("raw error replaced the public fallback: %q", message)
	}
}
