package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTextArtifactExactPage(t *testing.T) {
	for _, content := range []string{"test failed: café\n", string([]byte{0xff, 0x00}), string([]byte{0xc3}), "*literal*"} {
		page := readArtifactResponse{ArtifactID: "art", SHA256: "whole-artifact", ByteStart: 3, ByteEnd: 3 + int64(len(content)), TotalBytes: 200, Truncated: true, ContentBase64: base64.StdEncoding.EncodeToString([]byte(content))}
		got, err := textArtifactPage(page)
		if err != nil {
			t.Fatal(err)
		}
		if got.ByteStart != page.ByteStart || got.ByteEnd != page.ByteEnd || got.TotalBytes != page.TotalBytes || got.SHA256 != page.SHA256 || !got.Truncated {
			t.Fatal("page identity changed")
		}
		var restored []byte
		if got.ContentText != nil {
			if got.ContentBase64 != nil || got.Encoding != "utf-8" {
				t.Fatal("ambiguous text page")
			}
			restored = []byte(*got.ContentText)
		} else {
			if got.ContentBase64 == nil || got.Encoding != "base64" {
				t.Fatal("missing binary fallback")
			}
			restored, err = base64.StdEncoding.DecodeString(*got.ContentBase64)
			if err != nil {
				t.Fatal(err)
			}
		}
		if string(restored) != content {
			t.Fatal("bytes changed")
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "content_text") && strings.Contains(string(encoded), "content_base64") {
			t.Fatal("both encodings emitted")
		}
	}
}

type jobObservationTransport func(*http.Request) (*http.Response, error)

func (f jobObservationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func observationFixture(t *testing.T) (*workspaceOperations, jobRecord) {
	t.Helper()
	command := commandProfile{ID: "check"}
	encoded, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	profile := workspaceProfile{WorkspaceProfileID: "profile", RunnerImage: "image", profileSHA256: strings.Repeat("b", 64), commandByID: map[string]commandProfile{"check": command}}
	job := jobRecord{JobID: "job_" + strings.Repeat("a", 64), KubernetesJob: "test-job", Status: "pending", ProfileID: "profile", ProfileSHA256: profile.profileSHA256, RunnerImage: profile.RunnerImage, CommandID: "check", CommandSHA256: fmt.Sprintf("%x", sha256.Sum256(encoded)), ExpiresAt: time.Now().Add(time.Hour)}
	job.WorkspaceID = "ws_" + strings.Repeat("d", 64)
	store := &workspaceStore{client: newVerificationRedis(t)}
	if err := store.putMutable(context.Background(), "job", job.JobID, job, time.Hour); err != nil {
		t.Fatal(err)
	}
	runner := &kubernetesRunner{baseURL: "http://example.invalid", namespace: "test", httpClient: &http.Client{Transport: jobObservationTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":{"active":1}}`)), Header: make(http.Header)}, nil
	})}}
	return &workspaceOperations{store: store, profiles: &workspaceProfileStore{profiles: map[string]workspaceProfile{"profile": profile}}, runner: runner, now: time.Now}, job
}

func TestJobWaitReturnsPendingAndHonorsParent(t *testing.T) {
	operations, job := observationFixture(t)
	started := time.Now()
	got, err := operations.getJobWithWait(context.Background(), job.JobID, 15*time.Millisecond, time.Millisecond)
	if err != nil || got.Status != "running" {
		t.Fatalf("wait changed pending state: %s %v", got.Status, err)
	}
	if time.Since(started) < 15*time.Millisecond {
		t.Fatal("returned before observation window")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := operations.getJobWithWait(ctx, job.JobID, time.Second, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation lost: %v", err)
	}
}

func TestFinishedJobRefreshAndSubmissionRecovery(t *testing.T) {
	operations, job := observationFixture(t)
	job.Status = "succeeded"
	job.VerificationID = "verify_" + strings.Repeat("c", 64)
	if err := operations.store.putMutable(context.Background(), "job", job.JobID, job, time.Hour); err != nil {
		t.Fatal(err)
	}
	// Submission may recover its jobs before the aggregate exists.
	if _, err := operations.observeJob(context.Background(), job.JobID, false); err != nil {
		t.Fatal(err)
	}
	// Public status reads must propagate unavailable verification, not silently skip it.
	if _, err := operations.getJob(context.Background(), job.JobID); !errors.Is(err, errWorkspaceRecordNotFound) {
		t.Fatalf("refresh error lost: %v", err)
	}
}
