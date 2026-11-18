package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerificationScriptCanBeExtractedButNotChanged(t *testing.T) {
	store, err := loadWorkspaceProfiles("../../profiles/retail-order-workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := store.get("retail-order-go")
	content := "#!/usr/bin/env bash\nexit 0\n"
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gz)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "setup.sh", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, err := extractSnapshot(archive.Bytes(), destination, profile); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "setup.sh"))
	if err != nil || string(data) != content {
		t.Fatalf("verification script absent from snapshot: %v", err)
	}
	for _, file := range []candidateFile{
		{Path: "setup.sh", BaseSHA256: strings.Repeat("a", 64), ContentText: &content},
		{Path: "setup.sh", BaseSHA256: strings.Repeat("a", 64), Delete: true},
	} {
		if _, _, err := canonicalCandidateFiles(profile, []candidateFile{file}, nil); err == nil {
			t.Fatal("candidate could replace or delete its verification script")
		}
	}
	if _, _, err := canonicalCandidateFiles(profile, []candidateFile{{Path: "application.go", BaseSHA256: "absent", ContentText: &content}}, nil); err != nil {
		t.Fatalf("unrelated source edits rejected: %v", err)
	}
}

func TestProtectedPathsAreValidated(t *testing.T) {
	store, err := loadWorkspaceProfiles("../../profiles/retail-order-workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := store.get("retail-order-go")
	for _, paths := range [][]string{{"../setup.sh"}, {"/setup.sh"}, {"setup.sh", "setup.sh"}} {
		copy := profile
		copy.ProtectedPaths = paths
		if err := validateWorkspaceProfile(&copy); err == nil {
			t.Fatalf("invalid protected paths accepted: %v", paths)
		}
	}
	profile.ProtectedPaths = []string{"checks"}
	if !profile.protects("checks/gate.sh") || profile.protects("checks-other/gate.sh") {
		t.Fatal("directory protection has incorrect boundaries")
	}
}
