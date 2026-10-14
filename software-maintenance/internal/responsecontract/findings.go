// Package responsecontract owns the example's human-readable result contract.
package responsecontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// ResolveFindingLimit preserves the legacy limit only when the field is absent.
// The 4 KiB ceiling and 64-entry ceiling bound worst-case JSON escaping to
// 1.5 MiB of finding strings. Approved review receipts contain zero blocking
// findings, so this does not enlarge the publication client's receipt budget.
func ResolveFindingLimit(raw json.RawMessage, legacy int) (int, error) {
	if len(raw) == 0 {
		return legacy, nil
	}
	var value int
	if string(raw) == "null" || json.Unmarshal(raw, &value) != nil || value < 1 || value > 4096 {
		return 0, errors.New("maximum_finding_bytes must be an integer between 1 and 4096")
	}
	return value, nil
}

// FieldError exposes validation facts, not model-provided prose.
type FieldError struct {
	Field  string
	Actual int
	Limit  int
}

func (err *FieldError) Error() string {
	return fmt.Sprintf("%s exceeds its UTF-8 byte limit: %d > %d", err.Field, err.Actual, err.Limit)
}

// AddDetails preserves the public error code and existing evidence.
func AddDetails(details map[string]string, err error) map[string]string {
	var fieldErr *FieldError
	var validationErr *ValidationError
	if !errors.As(err, &fieldErr) && !errors.As(err, &validationErr) {
		return details
	}
	if details == nil {
		details = make(map[string]string)
	}
	if fieldErr != nil {
		details["validation_code"] = "FINDING_TOO_LONG"
		if fieldErr.Field == "summary" {
			details["validation_code"] = "SUMMARY_TOO_LONG"
		}
		details["field"] = fieldErr.Field
		details["actual_bytes"] = strconv.Itoa(fieldErr.Actual)
		details["limit_bytes"] = strconv.Itoa(fieldErr.Limit)
	} else {
		details["validation_code"] = validationErr.Code
		details["field"] = validationErr.Field
		if validationErr.Reason != "" {
			details["reason"] = validationErr.Reason
		}
		if validationErr.Expected != "" {
			details["expected"] = validationErr.Expected
		}
	}
	return details
}
