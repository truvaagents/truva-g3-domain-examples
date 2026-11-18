package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewBundleMeasuresStagedRegressionInputs(t *testing.T) {
	op, _, prior, _ := verificationReadFixture(t)
	_, workspace, profile, err := op.loadCandidate(t.Context(), prior.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	profile.stageCallerSet = map[string]struct{}{"repair": {}}
	profile.Limits.WorkspaceTTLSeconds = 3600
	op.profiles.profiles[profile.WorkspaceProfileID] = profile
	source := "package service\n"
	testFile := "package service\nfunc TestBoundary() {}\n"
	candidate, err := op.stageCandidate(t.Context(), "repair", stageCandidateRequest{
		WorkspaceID: workspace.WorkspaceID, CaseID: workspace.CaseID, ExpectedTest: "TestBoundary",
		Changes: []candidateFile{{Path: "service.go", BaseSHA256: "absent", ContentText: &source}},
		Tests:   []candidateFile{{Path: "service_test.go", BaseSHA256: "absent", ContentText: &testFile}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := publicationBundleResponse{Workspace: workspace, Candidate: candidate}
	review, err := op.buildReviewBundleResponse(bundle)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(testFile))
	want := hex.EncodeToString(digest[:])
	preview := review.CandidateFilePreviews[1]
	if review.CandidateFilePreviews[0].TargetSHA256 != nil || !preview.Test || preview.ContentSHA256 != want ||
		preview.TargetSHA256["reproduction"] != want || preview.TargetSHA256["candidate"] != want {
		t.Fatalf("unexpected review previews: %+v", review.CandidateFilePreviews)
	}
	for _, target := range []string{"reproduction", "candidate"} {
		t.Run(target, func(t *testing.T) {
			path := filepath.Join(op.root, candidate.targetSubPath(target), preview.Path)
			if err := os.WriteFile(path, []byte("X"+testFile[1:]), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := op.buildReviewBundleResponse(bundle)
			var mismatch *workspacePolicyFailure
			if !errors.As(err, &mismatch) || mismatch.code != "REGRESSION_INPUT_MISMATCH" {
				t.Fatalf("changed test input: %v", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if _, err := op.buildReviewBundleResponse(bundle); err == nil {
				t.Fatal("missing regression input accepted")
			}
			if err := os.WriteFile(path, []byte(testFile), 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}
