package serviceevidence

import (
	"errors"
	"net/http"

	"github.com/truvaagents/truva-g3/core"
)

// ReadFailure preserves the adapter's classification without exposing response
// bodies or requiring callers to parse error strings. Codes are package-owned.
type ReadFailure struct {
	Status    int
	Code      string
	Message   string
	Category  core.ErrorCategory
	Retryable bool
}

// ClassifyReadFailure recognizes errors returned by the external service reader.
// Unrelated store/application errors remain the caller's responsibility.
func ClassifyReadFailure(err error) (ReadFailure, bool) {
	if errors.Is(err, errReleaseChanged) {
		return ReadFailure{http.StatusConflict, "RELEASE_CHANGED", "Serving revision changed while configuration was collected", core.CategoryServiceError, true}, true
	}
	var failure *upstreamFailure
	if !errors.As(err, &failure) {
		return ReadFailure{}, false
	}
	switch failure.kind {
	case failureNotFound:
		return ReadFailure{http.StatusNotFound, "EVIDENCE_NOT_FOUND", "Requested evidence was not found", core.CategoryNotFound, false}, true
	case failureTimeout:
		return ReadFailure{http.StatusGatewayTimeout, "EVIDENCE_SOURCE_TIMEOUT", "Evidence source timed out", core.CategoryServiceError, true}, true
	case failureUnavailable, failureRateLimited, failureConflict:
		return ReadFailure{http.StatusServiceUnavailable, "EVIDENCE_SOURCE_UNAVAILABLE", "Evidence source is temporarily unavailable", core.CategoryServiceError, true}, true
	default:
		return ReadFailure{http.StatusBadGateway, "INVALID_UPSTREAM_RESPONSE", "Evidence source returned an invalid response", core.CategoryServiceError, false}, true
	}
}
