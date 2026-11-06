package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/discoverytest"
	"github.com/truvaagents/truva-g3/core"
)

func TestWorkspaceCreationAdvertisesAuthorizedCommandsAndLimits(t *testing.T) {
	profiles, err := loadWorkspaceProfiles("../../profiles/retail-order-workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	var profile workspaceProfile
	for _, item := range profiles.profiles {
		profile = item
	}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	content := "package sample\n"
	if err := tarWriter.WriteHeader(&tar.Header{Name: "sample.go", Mode: 0600, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive.Bytes())
	archiveSHA := hex.EncodeToString(digest[:])
	snapshotID := "src_" + archiveSHA
	var sourceCalls atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sourceCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": sourceSnapshotChunk{
			SnapshotID: snapshotID, RepositoryID: profile.RepositoryID, SourceProfileSHA256: strings.Repeat("a", 64), AssessmentReady: true,
			RevisionSHA: strings.Repeat("b", 40), TreeSHA: strings.Repeat("c", 40), ArchiveSHA256: archiveSHA, Format: "application/gzip",
			ByteStart: 0, ByteEnd: int64(archive.Len()), TotalBytes: int64(archive.Len()), ContentBase64: base64.StdEncoding.EncodeToString(archive.Bytes()),
		}})
	}))
	defer source.Close()
	client, err := newSourceSnapshotClient(source.URL, strings.Repeat("s", 40))
	if err != nil {
		t.Fatal(err)
	}
	auth, err := newWorkspaceAuthorizer(map[string]string{"maintenance-repair-agent": strings.Repeat("r", 40), "outsider": strings.Repeat("o", 40)})
	if err != nil {
		t.Fatal(err)
	}
	operations := &workspaceOperations{root: t.TempDir(), profiles: profiles, store: &workspaceStore{client: newVerificationRedis(t)}, sourceClient: client, now: time.Now}
	tool := &maintenanceWorkspaceTool{BaseTool: core.NewTool(workspaceToolName), profiles: profiles, auth: auth, operations: operations}
	tool.registerCapabilities()
	payload, err := json.Marshal(createWorkspaceRequest{ProfileID: profile.WorkspaceProfileID, CaseID: "case-fixture", SnapshotID: snapshotID})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token  string
		status int
	}{{"", http.StatusUnauthorized}, {strings.Repeat("o", 40), http.StatusForbidden}, {strings.Repeat("r", 40), http.StatusOK}} {
		request := httptest.NewRequest(http.MethodPost, "/api/capabilities/create_workspace", bytes.NewReader(payload))
		request.Header.Set("Authorization", "Bearer "+tc.token)
		response := httptest.NewRecorder()
		tool.handleCreateWorkspace(response, request)
		if response.Code != tc.status {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if tc.status != http.StatusOK {
			if sourceCalls.Load() != 0 || strings.Contains(response.Body.String(), "allowed_command_ids") || strings.Contains(response.Body.String(), "maximum_candidate_bytes") {
				t.Fatal("unauthorized request accessed source or profile metadata")
			}
			continue
		}
		var envelope struct {
			Success bool                      `json:"success"`
			Data    workspaceCreationResponse `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if !envelope.Success || len(envelope.Data.AllowedCommandIDs) != len(profile.ExperimentCommands) || envelope.Data.Limits != profile.Limits {
			t.Fatalf("discovery metadata: %+v", envelope)
		}
		for i, command := range profile.ExperimentCommands {
			if envelope.Data.AllowedCommandIDs[i] != command.ID {
				t.Fatalf("command choice mismatch: %+v", envelope.Data.AllowedCommandIDs)
			}
		}
		for _, private := range []string{`"argv"`, `"runner_image"`, `"workspace_pvc"`, `"allowed_callers"`} {
			if strings.Contains(response.Body.String(), private) {
				t.Errorf("private configuration leaked: %s", private)
			}
		}
		// Strict decoding of the durable receipt must still succeed without the projection fields.
		var stored workspaceRecord
		if err := operations.store.get(t.Context(), "workspace", envelope.Data.WorkspaceID, &stored); err != nil {
			t.Fatal(err)
		}
		if stored.WorkspaceID != envelope.Data.WorkspaceID || stored.ProfileSHA256 != profile.profileSHA256 {
			t.Fatal("workspace identity changed")
		}
		discoverytest.Output(t, discoverytest.Capability(t, tool.GetCapabilities(), "create_workspace"), envelope.Data, "allowed_command_ids", "limits")
	}
	catalog := discoverytest.Catalog(t, tool.GetCapabilities())
	for _, expected := range []string{"allowed_command_ids:array", "limits:object", "create_workspace.allowed_command_ids", "baseline forbids candidate_id", "maximum_artifact_read_bytes"} {
		if !strings.Contains(catalog, expected) {
			t.Errorf("planner catalog lacks %q", expected)
		}
	}
}
