package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/caseorigin"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
	"github.com/truvaagents/truva-g3/core"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestAssessmentWriteIntentBinding(t *testing.T) {
	facts := lifecycle.Facts{ComparedFactsDigest: "fixture-facts"}
	input := assessmentWriteInput{ExpectedOriginExecutionID: "fixture-execution", ExpectedIssueID: lifecycle.IssueID(facts), ExpectedFactsDigest: facts.ComparedFactsDigest}
	if err := validateAssessmentWriteIntent(input, "fixture-execution", facts); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*assessmentWriteInput){
		func(v *assessmentWriteInput) { v.ExpectedOriginExecutionID = "different" },
		func(v *assessmentWriteInput) { v.ExpectedIssueID = "different" },
		func(v *assessmentWriteInput) { v.ExpectedFactsDigest = "different" },
		func(v *assessmentWriteInput) { v.ExpectedFactsDigest = "" },
	} {
		changed := input
		mutate(&changed)
		if !errors.Is(validateAssessmentWriteIntent(changed, "fixture-execution", facts), errAssessmentIntent) {
			t.Fatal("changed write intent accepted")
		}
	}
	tool := &assessmentTool{}
	w := httptest.NewRecorder()
	tool.assessmentReply(w, httptest.NewRequest(http.MethodPost, "/", nil), "record_assessment", time.Now(), nil, errAssessmentIntent)
	var response core.ToolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 409 || response.Error == nil || response.Error.Code != "ASSESSMENT_INTENT_CHANGED" || response.Error.Retryable {
		t.Fatal("intent drift did not fail closed")
	}
}

func TestAssessmentArrayProjectionPreservesRecord(t *testing.T) {
	original := lifecycle.Assessment{AssessmentID: "opaque-original-id", Disposition: "benign"}
	before, _ := json.Marshal(original)
	view := assessmentResponse(original)
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(original)
	if string(fields["remediations"]) != "[]" || string(before) != string(after) || view.AssessmentID != original.AssessmentID {
		t.Fatal("array projection changed stored bytes/identity or returned null")
	}
	original.Remediations = []lifecycle.RemediationReference{{PublicationID: "publication"}}
	before, _ = json.Marshal(original)
	after, _ = json.Marshal(assessmentResponse(original))
	if string(before) != string(after) {
		t.Fatal("nonempty receipt references changed")
	}
}

type reviewLogger struct {
	core.Logger
	completion map[string]interface{}
}

func TestAssessmentWriterIsInternal(t *testing.T) {
	tool := &assessmentTool{BaseTool: core.NewTool(assessmentToolName)}
	tool.registerAssessmentCapabilities()
	seen := 0
	for _, capability := range tool.GetCapabilities() {
		if capability.Internal != (capability.Name == "record_assessment") {
			t.Fatalf("incorrect planner visibility for %s", capability.Name)
		}
		seen++
	}
	if seen != 3 {
		t.Fatal("assessment capability inventory changed")
	}
}

func (l *reviewLogger) InfoWithContext(context.Context, string, map[string]interface{}) {}
func (l *reviewLogger) WarnWithContext(_ context.Context, _ string, fields map[string]interface{}) {
	l.completion = fields
}

func TestAssessmentErrorTelemetry(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	ctx, span := provider.Tracer("review").Start(t.Context(), "assessment")
	logger := &reviewLogger{}
	tool := &assessmentTool{BaseTool: core.NewTool(assessmentToolName)}
	tool.Logger = logger
	request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	request.Header.Set("X-TruvaG3-Request-ID", "review-request")
	w := httptest.NewRecorder()
	tool.observeAssessment("record_assessment", func(w http.ResponseWriter, r *http.Request) {
		tool.assessmentReply(w, r, "record_assessment", time.Now(), nil, caseorigin.ErrTooLarge)
	})(w, request)
	span.End()
	if w.Code != 502 || logger.completion["error_code"] != "ASSESSMENT_ORIGIN_TOO_LARGE" || logger.completion["error_type"] != "response_limit" || logger.completion["retryable"] != false {
		t.Fatalf("incorrect error log: %+v", logger.completion)
	}
	events := recorder.Ended()[0].Events()
	var received, completed bool
	for _, event := range events {
		if event.Name == "maintenance.assessment.request_received" {
			received = len(event.Attributes) > 0 && string(event.Attributes[0].Key) == "request_id" && event.Attributes[0].Value.AsString() == "review-request"
		}
		if event.Name == "maintenance.assessment.operation" {
			for _, a := range event.Attributes {
				if string(a.Key) == "error_code" && a.Value.AsString() == "ASSESSMENT_ORIGIN_TOO_LARGE" {
					completed = true
				}
			}
		}
	}
	if !received || !completed {
		t.Fatal("request identity or structured failure missing from trace events")
	}
}
