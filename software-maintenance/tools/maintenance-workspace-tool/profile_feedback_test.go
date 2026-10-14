package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3/core"
)

func TestProfileFeedbackIsCallerScoped(t *testing.T) {
	tool := &maintenanceWorkspaceTool{BaseTool: core.NewTool(workspaceToolName), profiles: &workspaceProfileStore{profiles: map[string]workspaceProfile{}}}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("runner-%02d", i)
		tool.profiles.profiles[id] = workspaceProfile{WorkspaceProfileID: id, callerSet: map[string]struct{}{"repair": {}}}
	}
	tool.profiles.profiles["private-runner"] = workspaceProfile{WorkspaceProfileID: "private-runner", callerSet: map[string]struct{}{"other": {}}}
	for _, tc := range []struct {
		caller, profile string
		status          int
		wantHints       bool
	}{
		{"repair", "service-name", http.StatusNotFound, true},
		{"repair", "bad/profile", http.StatusBadRequest, true},
		{"repair", "private-runner", http.StatusForbidden, false},
		{"unknown", "service-name", http.StatusNotFound, false},
	} {
		recorder := httptest.NewRecorder()
		_, ok := tool.authorizeProfile(recorder, httptest.NewRequest(http.MethodPost, "/create", nil), "create_workspace", time.Now(), tc.caller, tc.profile)
		if ok || recorder.Code != tc.status {
			t.Fatalf("unexpected authorization: %v, %d", ok, recorder.Code)
		}
		var response core.ToolResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Error == nil || response.Error.Retryable {
			t.Fatalf("invalid error contract: %s, %v", recorder.Body.String(), err)
		}
		message := response.Error.Message
		if strings.Contains(message, "runner-00") != tc.wantHints || strings.Contains(message, "private-runner") || strings.Contains(message, "runner-08") {
			t.Fatalf("incorrect hint scope: %s", message)
		}
		if tc.wantHints && (!strings.Contains(message, tc.profile) || !strings.Contains(message, "workspace_profile_id") || !strings.Contains(message, "hints_truncated=true")) {
			t.Fatalf("missing actionable evidence: %s", message)
		}
	}
	message := tool.profileCorrection("repair", strings.Repeat("雪", 300), "Invalid")
	if !strings.Contains(message, "value_truncated=true") || len(message) > 1200 {
		t.Fatal("rejected value was not bounded")
	}
}

func TestProfileFeedbackAuthenticationPrecedesHints(t *testing.T) {
	auth, err := newWorkspaceAuthorizer(map[string]string{"repair": strings.Repeat("x", 40)})
	if err != nil {
		t.Fatal(err)
	}
	tool := &maintenanceWorkspaceTool{BaseTool: core.NewTool(workspaceToolName), auth: auth}
	recorder := httptest.NewRecorder()
	tool.handleCreateWorkspace(recorder, httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(`{"profile_id":"service-name"}`)))
	if recorder.Code != http.StatusUnauthorized || strings.Contains(recorder.Body.String(), "Caller-permitted") {
		t.Fatalf("unauthenticated hints: %s", recorder.Body.String())
	}
}

func TestWorkspaceErrorClassificationsAreBounded(t *testing.T) {
	for code, expected := range map[string]string{"PROFILE_NOT_FOUND": "profile_input", "CALLER_FORBIDDEN": "authorization", "WORKSPACE_DEPENDENCY_UNAVAILABLE": "dependency_or_state", "OPERATION_TIMEOUT": "timeout", "arbitrary-external-code": "input_or_policy"} {
		if got := workspaceErrorType(code); got != expected {
			t.Fatalf("%s: %s", code, got)
		}
	}
}
