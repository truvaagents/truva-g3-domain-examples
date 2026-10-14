package main

import (
	"strings"
	"testing"
)

// These guard paid/external transmission rather than application behavior.
func TestReplayAdmissionBindsExactReviewedInput(t *testing.T) {
	input := `{"interactions":[{"prompt":"literal *code* 界"}]}`
	got, hash, err := admitReplayInput(strings.NewReader(input), "execution-123", "", false)
	if err != nil || string(got) != input || len(hash) != 64 {
		t.Fatalf("offline admission: %v", err)
	}
	if _, _, err := admitReplayInput(strings.NewReader(input), "execution-123", hash, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, source, approval string }{
		{input + " ", "execution-123", hash},
		{strings.ReplaceAll(input, "界", "different"), "execution-123", hash},
		{input, "execution-456", hash},
		{input, "", hash},
		{input, "execution-123", ""},
		{input, "https://source.invalid/?secret=example", hash},
	} {
		if _, _, err := admitReplayInput(strings.NewReader(tc.input), tc.source, tc.approval, true); err == nil {
			t.Fatal("changed or unreviewed input admitted")
		}
	}
}

func TestReplayAdmissionRejectsIncompleteDocument(t *testing.T) {
	for _, input := range []string{"", `{`, `{} {}`, `{} trailing`, `"` + strings.Repeat("x", 4<<20) + `"`} {
		if _, _, err := admitReplayInput(strings.NewReader(input), "fixture", "", false); err == nil {
			t.Fatal("invalid/oversized fixture admitted")
		}
	}
}
