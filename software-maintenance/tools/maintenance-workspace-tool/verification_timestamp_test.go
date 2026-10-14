package main

import (
	"reflect"
	"testing"
	"time"
)

func TestVerificationReadDoesNotInvalidateReview(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	verification := verificationRecord{Status: "pending", CreatedAt: created, UpdatedAt: created}
	completed := created.Add(time.Minute)
	if !updateVerificationOutcome(&verification, true, true, true, true, completed) {
		t.Fatal("completion must update the outcome")
	}
	reviewed := completed.Add(time.Minute)
	approvedEvidence := verification
	for i := 1; i <= 3; i++ {
		if updateVerificationOutcome(&verification, true, true, true, true, reviewed.Add(time.Duration(i)*time.Minute)) {
			t.Fatal("unchanged review/publication reads must not rewrite verification")
		}
		if !reflect.DeepEqual(approvedEvidence, verification) || reviewed.Before(verification.UpdatedAt) {
			t.Fatal("reading unchanged evidence invalidated the review")
		}
	}
	changed := reviewed.Add(4 * time.Minute)
	if !updateVerificationOutcome(&verification, true, false, true, true, changed) ||
		verification.Status != "failed" || verification.PublicationEligible ||
		!reviewed.Before(verification.UpdatedAt) {
		t.Fatal("changed evidence must invalidate approval and publication eligibility")
	}
}

func TestVerificationOutcomeTransitions(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		terminal, passed, reproduced, ready bool
		status                              string
		suite, eligible                     bool
	}{
		{"running", false, true, false, true, "running", false, false},
		{"passed", true, true, true, true, "succeeded", true, true},
		{"failed-suite", true, false, true, true, "failed", false, false},
		{"no-reproduction", true, true, false, true, "failed", true, false},
		{"not-ready", true, true, true, false, "succeeded", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			verification := verificationRecord{Status: "pending", CreatedAt: created, UpdatedAt: created}
			now := created.Add(time.Minute)
			if !updateVerificationOutcome(&verification, tc.terminal, tc.passed, tc.reproduced, tc.ready, now) {
				t.Fatal("initial transition not recorded")
			}
			if verification.Status != tc.status || verification.SuitePassed != tc.suite ||
				verification.ExpectedTestObserved != tc.reproduced || verification.PublicationEligible != tc.eligible ||
				!verification.UpdatedAt.Equal(now) || !verification.CreatedAt.Equal(created) {
				t.Fatalf("unexpected transition: %+v", verification)
			}
			if updateVerificationOutcome(&verification, tc.terminal, tc.passed, tc.reproduced, tc.ready, now.Add(time.Minute)) || !verification.UpdatedAt.Equal(now) {
				t.Fatal("identical outcome changed the timestamp")
			}
		})
	}
}
