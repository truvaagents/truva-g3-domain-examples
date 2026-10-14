package responsecontract

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestFindingLimitAndSafeDetails(t *testing.T) {
	for _, legacy := range []int{256, 1024} {
		got, err := ResolveFindingLimit(nil, legacy)
		if err != nil || got != legacy {
			t.Fatalf("legacy: %d, %v", got, err)
		}
	}
	for _, input := range []string{`null`, `0`, `-1`, `1.5`, `"1024"`, `4097`, `{}`, `true`} {
		if _, err := ResolveFindingLimit(json.RawMessage(input), 256); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	got, err := ResolveFindingLimit(json.RawMessage(`1024`), 256)
	if err != nil || got != 1024 {
		t.Fatalf("override: %d, %v", got, err)
	}
	details := AddDetails(map[string]string{"failed_operations": "preserved"}, fmt.Errorf("outer: %w", &FieldError{Field: "findings", Actual: 1025, Limit: 1024}))
	if details["limit_bytes"] != "1024" || details["actual_bytes"] != "1025" || details["failed_operations"] != "preserved" {
		t.Fatalf("details: %v", details)
	}
}
