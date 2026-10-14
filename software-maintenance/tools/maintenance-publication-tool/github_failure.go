package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/truvaagents/truva-g3/core"
)

const defaultGitHubErrorMessageMaxBytes = 512

// This is a derived diagnostic, not the provider body or an outcome receipt.
// Keep request payloads, URLs, headers and unstructured bodies out of it.
type githubFailureEvidence struct {
	operation string
	status    int
	requestID string
	message   string
	shortened bool
}

func githubErrorMessageLimitFromEnvironment() (int, error) {
	raw, present := os.LookupEnv("MAINTENANCE_GITHUB_ERROR_MESSAGE_MAX_BYTES")
	if !present {
		return defaultGitHubErrorMessageMaxBytes, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 16384 {
		return 0, errors.New("MAINTENANCE_GITHUB_ERROR_MESSAGE_MAX_BYTES must be an integer from 1 to 16384")
	}
	return value, nil
}

func (client *githubPublicationClient) failureEvidence(method, endpoint string, response *http.Response, body []byte) *githubFailureEvidence {
	evidence := &githubFailureEvidence{operation: githubOperation(method, endpoint), status: response.StatusCode}
	if id := response.Header.Get("X-GitHub-Request-Id"); validGitHubRequestID(id) && (client.token == "" || !strings.Contains(id, client.token)) {
		evidence.requestID = id
	}
	var envelope struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return evidence // HTML, malformed JSON and non-string messages are not copied.
	}
	message := envelope.Message
	if client.token != "" {
		message = strings.ReplaceAll(message, client.token, "[REDACTED]")
	}
	message = core.RedactSensitiveText(message)
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, message)
	message = strings.Join(strings.Fields(message), " ")
	limit := client.errorMessageMaxBytes
	if limit <= 0 {
		limit = defaultGitHubErrorMessageMaxBytes
	}
	if len(message) > limit {
		message = message[:limit]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
		evidence.shortened = true
	}
	evidence.message = message
	return evidence
}

func validGitHubRequestID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == ':' || r == '-' {
			continue
		}
		return false
	}
	return true
}

// Enumerated operation names avoid exposing repository names, refs or queries.
func githubOperation(method, endpoint string) string {
	parts := strings.Split(endpoint, "/")
	if len(parts) < 5 || parts[1] != "repos" {
		return "repository_request"
	}
	resource := strings.Join(parts[4:], "/")
	if method == http.MethodPost {
		switch resource {
		case "pulls":
			return "create_pull_request"
		case "git/blobs":
			return "create_blob"
		case "git/trees":
			return "create_tree"
		case "git/commits":
			return "create_commit"
		case "git/refs":
			return "create_branch"
		}
	}
	if method == http.MethodGet {
		switch {
		case resource == "pulls":
			return "list_pull_requests"
		case strings.HasPrefix(resource, "pulls/"):
			return "read_pull_request"
		case strings.HasPrefix(resource, "git/ref/"):
			return "read_branch"
		case strings.HasPrefix(resource, "commits/"):
			return "read_commit"
		}
	}
	return "repository_request"
}

func (evidence *githubFailureEvidence) details() map[string]string {
	details := map[string]string{
		"provider": "github", "upstream_operation": evidence.operation,
		"upstream_status": strconv.Itoa(evidence.status),
	}
	if evidence.requestID != "" {
		details["github_request_id"] = evidence.requestID
	}
	if evidence.message != "" {
		details["upstream_message"] = evidence.message
	}
	if evidence.shortened {
		details["upstream_message_shortened"] = "true"
	}
	return details
}

func (evidence *githubFailureEvidence) summary() string {
	return fmt.Sprintf("GitHub %s returned HTTP %d; publication is not confirmed. Details contain provider diagnostics, not a publication receipt.", evidence.operation, evidence.status)
}
