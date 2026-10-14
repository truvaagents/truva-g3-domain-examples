package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestIncomingCandidateContentPresence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"omitted", `{}`, false},
		{"null-text", `{"content_text":null}`, false},
		{"null-base64", `{"content_base64":null}`, false},
		{"dual-empty", `{"content_text":"","content_base64":""}`, false},
		{"empty-text", `{"content_text":""}`, true},
		{"empty-base64", `{"content_base64":""}`, true},
		{"delete", `{"delete":true}`, true},
		{"delete-with-content", `{"delete":true,"content_text":""}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var file candidateFile
			err := decodeWorkspaceStrictBytes([]byte(tc.body), &file)
			if err == nil {
				err = validateCandidateInputContent(file)
			}
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestMissingCandidateContentFailsBeforeStorage(t *testing.T) {
	// Nil dependencies deliberately prove invalid input cannot reach storage or writes.
	operations := &workspaceOperations{}
	for _, tests := range []bool{false, true} {
		request := stageCandidateRequest{}
		file := candidateFile{Path: "existing.go", BaseSHA256: strings.Repeat("a", 64)}
		if tests {
			request.Tests = []candidateFile{file}
		} else {
			request.Changes = []candidateFile{file}
		}
		if _, err := operations.stageCandidate(context.Background(), "caller", request); err == nil {
			t.Fatal("omitted content reached staging")
		}
	}
}

func TestCandidateTextCanonicalizesToExistingBytes(t *testing.T) {
	text := "package sample\r\n// café\n"
	profile := workspaceProfile{Limits: workspaceLimits{MaximumFileBytes: 1024, MaximumCandidateBytes: 2048}}
	a, bytesA, err := canonicalCandidateFiles(profile, []candidateFile{{Path: "main.go", BaseSHA256: "absent", ContentText: &text}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, bytesB, err := canonicalCandidateFiles(profile, []candidateFile{{Path: "main.go", BaseSHA256: "absent", ContentBase64: base64.StdEncoding.EncodeToString([]byte(text))}}, nil)
	if err != nil || bytesA != bytesB || !reflect.DeepEqual(a, b) {
		t.Fatalf("canonical mismatch: %v %v %v", a, b, err)
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip []candidateFile
	if err := decodeWorkspaceStrictBytes(encoded, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if roundtrip[0].ContentText != nil {
		t.Fatal("text leaked into canonical storage")
	}
	// Exercise the stored-record validation path, not just JSON decoding.
	baseline := t.TempDir()
	a, err = resolveCandidateFileModes(baseline, a)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeWorkspaceStrictBytes(encoded, &roundtrip); err != nil {
		t.Fatal(err)
	}
	stored, storedBytes, err := canonicalStoredCandidateFiles(profile, baseline, roundtrip, nil)
	if err != nil || storedBytes != bytesA || !reflect.DeepEqual(stored, a) {
		t.Fatalf("stored roundtrip: %v %v", stored, err)
	}
	wantID, wantTest := candidateIdentity("workspace", "TestExample", a, 1)
	gotID, gotTest := candidateIdentity("workspace", "TestExample", stored, 1)
	if gotID != wantID || gotTest != wantTest {
		t.Fatal("storage changed identity")
	}
}

func TestCandidateRejectsAmbiguousEncodings(t *testing.T) {
	profile := workspaceProfile{Limits: workspaceLimits{MaximumFileBytes: 1024, MaximumCandidateBytes: 2048}}
	for _, body := range []string{`{"path":"main.go","base_sha256":"absent","content_text":"","content_base64":""}`, `{"path":"main.go","base_sha256":"absent","delete":true,"content_text":""}`} {
		var file candidateFile
		if err := decodeWorkspaceStrictBytes([]byte(body), &file); err != nil {
			t.Fatal(err)
		}
		if _, _, err := canonicalCandidateFiles(profile, []candidateFile{file}, nil); err == nil {
			t.Fatal("ambiguous content accepted")
		}
	}
}

func TestIntentionalEmptyCandidateSurvivesStoredEncoding(t *testing.T) {
	profile := workspaceProfile{Limits: workspaceLimits{MaximumFileBytes: 1024, MaximumCandidateBytes: 2048}}
	for _, encoding := range []string{"content_text", "content_base64"} {
		var file candidateFile
		if err := decodeWorkspaceStrictBytes([]byte(`{"path":"empty.go","base_sha256":"absent","`+encoding+`":""}`), &file); err != nil {
			t.Fatal(err)
		}
		if err := validateCandidateInputContent(file); err != nil {
			t.Fatal(err)
		}
		files, count, err := canonicalCandidateFiles(profile, []candidateFile{file}, nil)
		if err != nil || count != 0 {
			t.Fatalf("canonical: %d %v", count, err)
		}
		baseline := t.TempDir()
		files, err = resolveCandidateFileModes(baseline, files)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(files)
		if err != nil {
			t.Fatal(err)
		}
		var reloaded []candidateFile
		if err := decodeWorkspaceStrictBytes(body, &reloaded); err != nil {
			t.Fatal(err)
		}
		stored, _, err := canonicalStoredCandidateFiles(profile, baseline, reloaded, nil)
		if err != nil || !reflect.DeepEqual(files, stored) {
			t.Fatalf("empty storage roundtrip: %v", err)
		}
	}
}

func TestPendingJobSchemaRequiresOnlyPresentFields(t *testing.T) {
	body, err := json.Marshal(jobRecord{Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range jobOutputSummary().RequiredFields {
		if _, ok := fields[field.Name]; !ok {
			t.Fatalf("required field %s absent in running receipt", field.Name)
		}
	}
}
