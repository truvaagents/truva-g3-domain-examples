package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/truvaagents/truva-g3/core"
)

func TestGitHubFailureEvidenceReachesToolResponse(t *testing.T) {
	for _, status := range []int{403, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			profile, _ := remediationFixture()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-GitHub-Request-Id", "ABCD:1234:5678")
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "provider failure"})
			}))
			defer server.Close()
			client, err := newGitHubPublicationClient(server.URL, strings.Repeat("t", 40))
			if err != nil {
				t.Fatal(err)
			}
			client.httpClient = server.Client()
			_, err = client.request(t.Context(), profile, http.MethodPost, "/repos/owner/repository/pulls", nil, map[string]string{"title": "fix"}, nil)
			var failure *publicationFailure
			if !errors.As(err, &failure) || failure.kind != "github_rejected" || failure.retryable != (status != 403) {
				t.Fatalf("classification changed: %v", err)
			}
			tool := &maintenancePublicationTool{BaseTool: core.NewTool(publicationToolName)}
			writer := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/publish_draft_pr", nil)
			tool.failOperation(writer, request, "publish_draft_pr", "profile", time.Now(), err)
			wantStatus := 503
			if status == 403 {
				wantStatus = 422
			}
			if writer.Code != wantStatus {
				t.Fatalf("tool status = %d", writer.Code)
			}
			var result struct {
				Success bool           `json:"success"`
				Error   core.ToolError `json:"error"`
			}
			if err := json.Unmarshal(writer.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Success || result.Error.Details["github_request_id"] != "ABCD:1234:5678" || result.Error.Details["upstream_status"] != strconv.Itoa(status) || result.Error.Details["upstream_operation"] != "create_pull_request" || result.Error.Details["upstream_message"] != "provider failure" || !strings.Contains(result.Error.Message, "publication is not confirmed") {
				t.Fatalf("missing provider evidence: %s", writer.Body.String())
			}
		})
	}
}

func TestGitHubFailureMessageBoundaries(t *testing.T) {
	client := &githubPublicationClient{token: "secret-credential-value", errorMessageMaxBytes: 31}
	for _, scenario := range []struct{ name, body, id string }{
		{"secrets", `{"message":"secret-credential-value Authorization: Bearer xyz456"}`, "ABCD:123"},
		{"unicode", `{"message":"` + strings.Repeat("界", 20) + `"}`, "ABCD:123"},
		{"html", `<html>secret-credential-value</html>`, "bad value"},
		{"null", `{"message":null}`, "ABCD:123"},
		{"object", `{"message":{"secret":"value"}}`, "ABCD:123"},
		{"controls", "{\"message\":\"first\\nsecond\\u0000third\"}", "secret-credential-value"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			response := &http.Response{StatusCode: 500, Header: make(http.Header)}
			response.Header.Set("X-GitHub-Request-Id", scenario.id)
			evidence := client.failureEvidence(http.MethodPost, "/repos/owner/repository/pulls", response, []byte(scenario.body))
			encoded, err := json.Marshal(evidence.details())
			if err != nil {
				t.Fatal(err)
			}
			if len(evidence.message) > 31 || !utf8.ValidString(evidence.message) || strings.Contains(string(encoded), client.token) || strings.Contains(string(encoded), "xyz456") || strings.Contains(evidence.message, "\n") || strings.Contains(evidence.message, "\x00") {
				t.Fatalf("unsafe diagnostic: %s", encoded)
			}
			if scenario.name == "unicode" && !evidence.shortened {
				t.Fatal("missing shortening marker")
			}
			if (scenario.name == "html" || scenario.name == "object" || scenario.name == "null") && evidence.message != "" {
				t.Fatal("invalid message retained")
			}
		})
	}
}

func TestGitHubErrorMessageLimit(t *testing.T) {
	const key = "MAINTENANCE_GITHUB_ERROR_MESSAGE_MAX_BYTES"
	t.Setenv(key, "512")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	if value, err := githubErrorMessageLimitFromEnvironment(); err != nil || value != 512 {
		t.Fatalf("default: %d %v", value, err)
	}
	for _, raw := range []string{"", "0", "-1", "abc", "16385"} {
		t.Setenv(key, raw)
		if _, err := githubErrorMessageLimitFromEnvironment(); err == nil {
			t.Fatalf("accepted invalid limit %q", raw)
		}
	}
	t.Setenv(key, "1024")
	if value, err := githubErrorMessageLimitFromEnvironment(); err != nil || value != 1024 {
		t.Fatalf("override: %d %v", value, err)
	}
}

func TestGitHubPRFailurePreservedAfterReconciliation(t *testing.T) {
	for _, status := range []int{500, 409, 422} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			profile, _ := remediationFixture()
			gets, posts := 0, 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					gets++
					_, _ = w.Write([]byte("[]"))
					return
				}
				posts++
				w.Header().Set("X-GitHub-Request-Id", "CREATE:123")
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": http.StatusText(status)})
			}))
			defer server.Close()
			client, err := newGitHubPublicationClient(server.URL, strings.Repeat("t", 40))
			if err != nil {
				t.Fatal(err)
			}
			client.httpClient = server.Client()
			_, err = client.ensureDraftPullRequest(t.Context(), profile, "branch", "base", "commit", "title", "body")
			var failure *publicationFailure
			wantKind := "github_pr_ambiguous"
			if status == 500 {
				wantKind = "github_rejected"
			}
			if !errors.As(err, &failure) || failure.kind != wantKind || !failure.retryable || failure.github == nil || failure.github.requestID != "CREATE:123" || failure.github.status != status || gets != 2 || posts != 1 {
				t.Fatalf("lost create evidence or changed retries: %v GET=%d POST=%d", err, gets, posts)
			}
		})
	}
}
