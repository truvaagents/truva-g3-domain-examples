package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRawToolWireContract(t *testing.T) {
	for _, body := range []string{`{"profile_id":"p","observation_id":"o"}`} {
		var input assessmentLookupInput
		if err := decodeAssessmentRequest(httptest.NewRequest("POST", "/", strings.NewReader(body)), 1024, &input); err != nil || input.ProfileID != "p" {
			t.Fatalf("raw tool parameters rejected: %v", err)
		}
	}
	for _, body := range []string{`{"data":{"profile_id":"p"}}`, `{"profile_id":"p","profile_id":"q"}`, `null`, `{} {}`} {
		var input assessmentLookupInput
		err := decodeAssessmentRequest(httptest.NewRequest("POST", "/", strings.NewReader(body)), 1024, &input)
		if err == nil {
			t.Fatalf("invalid tool body accepted: %s", body)
		}
	}
}
