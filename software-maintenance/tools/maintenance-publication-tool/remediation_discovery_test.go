package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Exercises real request shapes and the no-write boundary, including malformed
// successful provider responses. No production credentials or AI calls.
func TestExistingPRDiscoveryAndPublicationGuard(t *testing.T) {
	for _, scenario := range []string{"matching", "unrelated", "incomplete", "null_listing", "head_changed", "missing_patch", "bounded_patch", "same_reservation", "reservation_wrong_branch", "reservation_wrong_head", "reservation_wrong_repository", "reservation_missing_commit"} {
		t.Run(scenario, func(t *testing.T) {
			profile, pull := remediationFixture()
			profile.BaseRef = "main"
			profile.Limits.MaximumBodyBytes = 1024
			pull.Title = "Existing repair"
			pull.Body = "Evidence for an existing change"
			reservation := publicationRecord{}
			if strings.HasPrefix(scenario, "reservation_") || scenario == "same_reservation" {
				pull.Head.Repo.FullName = profile.Owner + "/" + profile.Repository
				reservation = publicationRecord{BaseRef: profile.BaseRef, BaseSHA: pull.Base.SHA, Branch: pull.Head.Ref, CommitSHA: pull.Head.SHA}
				switch scenario {
				case "reservation_wrong_branch":
					reservation.Branch = "other-publication"
				case "reservation_wrong_head":
					reservation.CommitSHA = strings.Repeat("a", 40)
				case "reservation_wrong_repository":
					pull.Head.Repo.FullName = "fork/service"
				case "reservation_missing_commit":
					reservation.CommitSHA = ""
				}
			}
			count := 1
			pull.ChangedFiles = &count
			patch := "@@ -1 +1 @@\n-old\n+new\n"
			file := githubPullFile{Path: "checkout.go", Status: "modified", SHA: "ec23be681b1a1f4ab56f1ee127acf5d6669af8ab", Patch: &patch}
			if scenario == "unrelated" {
				file.SHA = strings.Repeat("b", 40)
			}
			if scenario == "missing_patch" {
				file.Patch = nil
			}
			if scenario == "bounded_patch" {
				limit := 3
				profile.Limits.MaximumPRPatchBytes = &limit
				patch = "€€€"
			}
			if scenario == "incomplete" {
				count = 2
			}
			reads, writes := 0, 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/pulls"):
					if r.URL.Query().Get("head") != "" {
						t.Error("discovery was limited to a case branch")
					}
					if scenario == "null_listing" {
						_, _ = w.Write([]byte("null"))
						return
					}
					_ = json.NewEncoder(w).Encode([]githubRemediationPR{pull})
				case strings.HasSuffix(r.URL.Path, "/files"):
					_ = json.NewEncoder(w).Encode([]githubPullFile{file})
				default:
					reads++
					copy := pull
					if scenario == "head_changed" && reads%2 == 0 {
						copy.Head.SHA = strings.Repeat("a", 40)
					}
					_ = json.NewEncoder(w).Encode(copy)
				}
			}))
			defer server.Close()
			origin, _ := url.Parse(server.URL)
			operations := &publicationOperations{github: &githubPublicationClient{baseURL: origin, httpClient: server.Client()}, now: func() time.Time { return time.Now().UTC() }}
			listing, err := operations.listOpenRemediations(t.Context(), profile)
			if scenario == "null_listing" {
				if err == nil {
					t.Fatal("null falsely established empty coverage")
				}
				return
			}
			if err != nil || !listing.Complete || len(listing.PullRequests) != 1 {
				t.Fatalf("discovery: %+v %v", listing, err)
			}
			changes, err := operations.remediationChanges(t.Context(), profile, remediationChangesRequest{PullRequestNumber: pull.Number, ExpectedHeadSHA: pull.Head.SHA})
			if scenario == "head_changed" {
				if err == nil {
					t.Fatal("changed head accepted")
				}
				return
			}
			if err != nil || changes.FilesComplete != (scenario != "incomplete") {
				t.Fatalf("changes: %+v %v", changes, err)
			}
			if scenario == "missing_patch" && changes.Files[0].PatchComplete {
				t.Fatal("absent patch reported complete")
			}
			if scenario == "bounded_patch" && (changes.Files[0].PatchComplete || *changes.Files[0].Patch != "€") {
				t.Fatal("UTF-8 patch budget not preserved")
			}
			candidate := candidateRecord{RevisionSHA: pull.Base.SHA, ChangeCount: 1, Files: []workspaceFile{{Path: "checkout.go", ContentBase64: base64.StdEncoding.EncodeToString([]byte("package main\n\nfunc checkout(receipt *Receipt) ledger {\n\treturn ledger{Total: receipt.UnitPrice*receipt.Quantity - receipt.Discount, Writes: 1}\n}\n"))}}}
			err = operations.guardDuplicateProductionChange(context.Background(), profile, candidate, reservation)
			var failure *remediationFailure
			if scenario == "unrelated" || scenario == "same_reservation" {
				if err != nil {
					t.Fatalf("unrelated production bytes blocked: %v", err)
				}
			} else if !errors.As(err, &failure) || failure.code != "EXISTING_REMEDIATION" || failure.retryable {
				t.Fatalf("duplicate guard: %v", err)
			}
			if writes != 0 {
				t.Fatalf("read/guard issued %d writes", writes)
			}
		})
	}
}

func TestRemediationReadLimitDefaultsAndValidation(t *testing.T) {
	if readLimit(nil, 50) != 50 {
		t.Fatal("default lost")
	}
	profiles, err := loadPublicationProfiles("../../profiles/retail-order-publication.json")
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := profiles.get("retail-order-publication")
	if !ok {
		t.Fatal("valid profile fixture missing")
	}
	zero := 0
	profile.Limits.MaximumOpenPRs = &zero
	if validatePublicationProfile(&profile) == nil {
		t.Fatal("explicit invalid limit accepted")
	}
}
