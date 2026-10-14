package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
	"github.com/truvaagents/truva-g3/orchestration"
)

func TestRepairSummaryProseAndReceiptBoundary(t *testing.T) {
	profile := repairProfile{Limits: repairLimits{MaximumFindings: 32}}
	for _, tc := range []struct {
		summary string
		valid   bool
	}{
		{"Evidence:\n\tChecks passed.\r\nCandidate ready.", true},
		{"Evidence\x00invalid", false}, {"Evidence\x1binvalid", false},
		{"Evidence\x7finvalid", false}, {"Evidence\u0085invalid", false},
	} {
		decision := repairDecision{Status: "inconclusive", Summary: tc.summary}
		err := validateRepairDecision(profile, &decision)
		if (err == nil) != tc.valid || decision.Summary != tc.summary {
			t.Fatalf("summary shape %q: %v", tc.summary, err)
		}
	}
	decision := repairDecision{Status: "inconclusive", Summary: "Valid\nprose", Findings: []string{"Invalid\nfinding"}}
	if validateRepairDecision(profile, &decision) == nil || validRepairText("Invalid\nidentifier") {
		t.Fatal("prose allowance relaxed a non-summary contract")
	}
	agent := &maintenanceRepairAgent{}
	_, err := agent.buildRepairResponse(profile, repairRequest{}, &orchestration.OrchestratorResponse{
		RequestID: "diagnostic", Response: `{"status":"candidate_submitted","summary":"Valid\nprose","findings":[]}`,
	}, &orchestration.ExecutionResult{Success: true})
	var validation *responsecontract.ValidationError
	if !errors.As(err, &validation) || validation.Code != "TRUSTED_EVIDENCE_MISSING" {
		t.Fatalf("positive claim accepted without receipts: %v", err)
	}
}

// Optional no-AI replay of the original private evidence. Fixtures remain outside
// this repo. Receipts are evaluated at the recorded synthesis completion time,
// not presented as current live receipts. No stored execution is modified.
func TestArchivedRepairSummaryReplay(t *testing.T) {
	directory := os.Getenv("MAINTENANCE_SUMMARY_REPLAY_DIR")
	if directory == "" {
		t.Skip("set the preserved evidence directory for the no-AI replay")
	}
	store, err := loadRepairProfiles("../../profiles/retail-order-repair.json")
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := store.get("retail-order-repair")
	if !ok {
		t.Fatal("repair profile missing")
	}
	for _, tc := range []struct {
		id, hash, executionHash string
		bytes, lf               int
	}{
		{"1791989060730837246", "27dd33efc4d0806db492dd16326a1eb06922e96f4f33d4ccbbee0c690148b637", "97d04f9718e715431f2765df21ee5bb7c50ab3b42713987705b3bda3cb9c42c0", 5673, 10},
		{"1791991446538072879", "1d8d2983ccdb45feded2aeb28970147c6826598880216d52aad37b21e0f725b9", "a26a99fbf08cc854b41e8d506c9dcd78dba8a1de44b87c13e604506aab7945a9", 5155, 8},
	} {
		t.Run(tc.id, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(directory, "debug-maintenance-repair-"+tc.id+".json"))
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != tc.hash {
				t.Fatalf("debug fixture missing or changed: %v", err)
			}
			var debug struct {
				Interactions []struct {
					Type       string    `json:"type"`
					Response   string    `json:"response"`
					Timestamp  time.Time `json:"timestamp"`
					DurationMS int64     `json:"duration_ms"`
				} `json:"interactions"`
			}
			if err := json.Unmarshal(data, &debug); err != nil {
				t.Fatal(err)
			}
			var raw string
			var at time.Time
			for _, item := range debug.Interactions {
				if item.Type == "synthesis" {
					raw = item.Response
					at = item.Timestamp.Add(time.Duration(item.DurationMS) * time.Millisecond)
				}
			}
			var decision repairDecision
			if err := decodeRepairPartialBytes([]byte(raw), &decision); err != nil {
				t.Fatal(err)
			}
			if len(decision.Summary) != tc.bytes || strings.Count(decision.Summary, "\n") != tc.lf {
				t.Fatal("not the observed multiline response")
			}
			data, err = os.ReadFile(filepath.Join(directory, "execution-maintenance-repair-"+tc.id+".json"))
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != tc.executionHash {
				t.Fatalf("execution fixture missing or changed: %v", err)
			}
			var record struct {
				RequestID       string                         `json:"request_id"`
				OriginalRequest string                         `json:"original_request"`
				Result          *orchestration.ExecutionResult `json:"result"`
			}
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if record.Result == nil || !record.Result.Success {
				t.Fatal("saved execution was not successful")
			}
			// Bind receipt validation to the independently recorded caller inputs,
			// rather than deriving the expected case/snapshot from the receipt.
			_, payload, found := strings.Cut(record.OriginalRequest, "<repair_request>")
			payload, closed := strings.CutSuffix(payload, "</repair_request>")
			if !found || !closed {
				t.Fatal("saved repair request envelope missing")
			}
			var request repairRequest
			if err := decodeRepairPartialBytes([]byte(payload), &request); err != nil {
				t.Fatal(err)
			}
			if !caseIDPattern.MatchString(request.CaseID) || !observationIDPattern.MatchString(request.ObservationID) || !snapshotIDPattern.MatchString(request.SnapshotID) || request.Objective == "" {
				t.Fatal("saved caller inputs incomplete")
			}
			request.ProfileID = profile.RepairProfileID
			agent := &maintenanceRepairAgent{now: func() time.Time { return at }}
			response := &orchestration.OrchestratorResponse{RequestID: record.RequestID, Response: raw}
			result, err := agent.buildRepairResponse(profile, request, response, record.Result)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != "candidate_submitted" || result.Summary != decision.Summary || result.Candidate == nil || result.Verification == nil {
				t.Fatal("prose or authoritative evidence not preserved")
			}
			request.CaseID = "case-other"
			if _, err := agent.buildRepairResponse(profile, request, response, record.Result); err == nil {
				t.Fatal("cross-case receipts accepted")
			}
			t.Logf("accepted saved envelope: summary_bytes=%d line_feeds=%d; cross-case receipts rejected", tc.bytes, tc.lf)
		})
	}
}

func TestRepairSummaryByteLimit(t *testing.T) {
	for _, tc := range []struct {
		summary string
		limit   int
		valid   bool
	}{
		{strings.Repeat("x", 4183), 0, true},
		{strings.Repeat("x", 10000), 0, true},
		{strings.Repeat("x", 10001), 0, false},
		{strings.Repeat("é", 5000), 0, true},
		{strings.Repeat("é", 5001), 0, false},
		{strings.Repeat("x", 4097), 4096, false},
		{"", 0, false}, {string([]byte{0xff}), 0, false},
	} {
		decision := repairDecision{Status: "inconclusive", Summary: tc.summary}
		err := validateRepairDecision(repairProfile{Limits: repairLimits{MaximumSummaryBytes: tc.limit}}, &decision)
		if (err == nil) != tc.valid {
			t.Fatalf("bytes=%d override=%d: %v", len(tc.summary), tc.limit, err)
		}
	}
}

func TestRepairSummarySettingAppliedAtStartup(t *testing.T) {
	const key = "MAINTENANCE_REPAIR_MAXIMUM_SUMMARY_BYTES"
	filename := os.Getenv("MAINTENANCE_REPAIR_PROFILES_FILE")
	if filename == "" {
		filename = "../../profiles/retail-order-repair.json"
	}
	initial, err := loadRepairProfiles(filename)
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := initial.get("retail-order-repair")
	if !ok {
		t.Fatal("profile missing")
	}
	if _, present := os.LookupEnv(key); !present && profile.Limits.summaryLimit() != 10000 {
		t.Fatal("absent setting did not default to 10000")
	}
	t.Logf("effective_summary_bytes_before_test=%d", profile.Limits.summaryLimit())
	for _, tc := range []struct {
		value string
		want  int
	}{{"10000", 10000}, {"4096", 4096}, {"65536", 65536}, {"0", 0}, {"", 0}} {
		t.Setenv(key, tc.value)
		store, err := loadRepairProfiles(filename)
		if tc.want == 0 {
			if err == nil {
				t.Fatalf("accepted invalid configuration %q", tc.value)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		profile, ok := store.get("retail-order-repair")
		if !ok || profile.Limits.summaryLimit() != tc.want {
			t.Fatalf("override %q: resolved limit %d", tc.value, profile.Limits.summaryLimit())
		}
		// Exercise validation with the same resolved profile used by the prompt.
		decision := repairDecision{Status: "inconclusive", Summary: strings.Repeat("x", profile.Limits.summaryLimit()+1)}
		if err := validateRepairDecision(profile, &decision); err == nil {
			t.Fatalf("override %q not enforced", tc.value)
		}
	}
}
