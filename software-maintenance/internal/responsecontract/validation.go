package responsecontract

import "errors"

// ValidationError describes a server-owned result contract failure. Expected
// and Reason contain only contract guidance and observed conditions chosen by
// the application, not model prose or upstream response text. Cause remains
// available to correlated logs and errors.As.
type ValidationError struct {
	Code     string
	Field    string
	Expected string
	Reason   string
	Cause    error
}

func (err *ValidationError) Error() string {
	if err.Cause != nil {
		return err.Cause.Error()
	}
	return err.Code + ": invalid " + err.Field
}

func (err *ValidationError) Unwrap() error { return err.Cause }

// Invalid adds public contract facts without replacing the original cause.
func Invalid(code, field, expected string, cause error) error {
	return &ValidationError{Code: code, Field: field, Expected: expected, Cause: cause}
}

// FailureMessage keeps the actionable reason in the top-level error too, rather
// than only in details alongside potentially large failed-operation evidence.
// Unknown errors retain the existing fallback; raw causes are not public guidance.
func FailureMessage(fallback string, err error) string {
	var fieldErr *FieldError
	if errors.As(err, &fieldErr) {
		return fieldErr.Error()
	}
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		message := validationErr.Code + ": invalid " + validationErr.Field
		if validationErr.Reason != "" {
			message += "; reason " + validationErr.Reason
		}
		if validationErr.Expected != "" {
			message += "; expected " + validationErr.Expected
		}
		return message
	}
	return fallback
}
