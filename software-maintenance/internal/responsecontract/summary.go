package responsecontract

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultSummaryBytes bounds final decision prose, independently of AI tokens.
const DefaultSummaryBytes = 10_000

// ValidSummaryText validates human-readable decision prose, not identifiers or
// receipt fields. Ordinary line breaks and tabs are accepted without rewriting
// the text. The caller owns the byte limit and any existing outer trimming.
func ValidSummaryText(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) && char != '\n' && char != '\r' && char != '\t' {
			return false
		}
	}
	return true
}

// ResolveSummaryLimit defaults only absent configuration. Explicit invalid
// values fail startup, using the same bounds as the coordinator's summary limit.
func ResolveSummaryLimit(lookup func(string) (string, bool), key string) (int, error) {
	value, present := lookup(key)
	if !present {
		return DefaultSummaryBytes, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 65536 {
		return 0, errors.New(key + " must be an integer between 1 and 65536 UTF-8 bytes")
	}
	return limit, nil
}
