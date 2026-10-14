package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type workspaceOperations struct {
	root         string
	profiles     *workspaceProfileStore
	store        *workspaceStore
	sourceClient *sourceSnapshotClient
	runner       *kubernetesRunner
	now          func() time.Time
}

func (operations *workspaceOperations) createWorkspace(ctx context.Context, profile workspaceProfile, caseID, snapshotID string) (workspaceRecord, error) {
	if !caseIDPattern.MatchString(caseID) || !snapshotIDPattern.MatchString(snapshotID) {
		return workspaceRecord{}, errors.New("case or snapshot identifier is invalid")
	}
	archive, identity, err := operations.sourceClient.download(ctx, snapshotID, profile.Limits.MaximumSnapshotBytes)
	if err != nil {
		if errors.Is(err, errSourceAssessmentNotReady) {
			return workspaceRecord{}, errors.New("source profile is not ready for workspace assessment")
		}
		return workspaceRecord{}, serviceFailure(err, true)
	}
	if identity.RepositoryID != profile.RepositoryID {
		return workspaceRecord{}, errors.New("source snapshot repository does not match the workspace profile")
	}
	digest := sha256.Sum256([]byte(profile.WorkspaceProfileID + "\x00" + profile.profileSHA256 + "\x00" + identity.SourceProfileSHA256 + "\x00" + caseID + "\x00" + snapshotID))
	workspaceID := "ws_" + hex.EncodeToString(digest[:])
	var existing workspaceRecord
	if err := operations.store.get(ctx, "workspace", workspaceID, &existing); err == nil {
		validated, _, validationErr := operations.loadWorkspace(ctx, workspaceID)
		return validated, validationErr
	} else if !errors.Is(err, errWorkspaceRecordNotFound) {
		return workspaceRecord{}, err
	}
	workspaceRoot := filepath.Join(operations.root, workspaceID)
	temporary, err := os.MkdirTemp(operations.root, ".workspace-")
	if err != nil {
		return workspaceRecord{}, errors.New("create temporary workspace")
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	baseline := filepath.Join(temporary, "baseline")
	if err := os.Mkdir(baseline, 0o750); err != nil {
		return workspaceRecord{}, errors.New("create baseline directory")
	}
	manifest, err := extractSnapshot(archive, baseline, profile)
	if err != nil {
		return workspaceRecord{}, err
	}
	metadataBytes, err := json.Marshal(manifest)
	if err != nil {
		return workspaceRecord{}, errors.New("encode extracted source manifest")
	}
	if err := os.WriteFile(filepath.Join(temporary, "source-manifest.json"), metadataBytes, 0o600); err != nil {
		return workspaceRecord{}, errors.New("write extracted source manifest")
	}
	if err := os.Rename(temporary, workspaceRoot); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return workspaceRecord{}, errors.New("publish workspace directory")
		}
	}
	createdAt := operations.now()
	record := workspaceRecord{
		SchemaVersion: 1, WorkspaceID: workspaceID, ProfileID: profile.WorkspaceProfileID,
		ProfileSHA256: profile.profileSHA256, SourceProfileSHA256: identity.SourceProfileSHA256,
		CaseID: caseID, RepositoryID: profile.RepositoryID, SnapshotID: snapshotID,
		RevisionSHA: identity.RevisionSHA, TreeSHA: identity.TreeSHA, ArchiveSHA256: identity.ArchiveSHA256,
		VerificationReady: profile.VerificationReady, CreatedAt: createdAt, ExpiresAt: createdAt.Add(profile.ttl()),
		Limitations: append([]string{}, profile.Limitations...),
	}
	if err := operations.store.putImmutable(ctx, "workspace", workspaceID, record, profile.ttl()); err != nil {
		return workspaceRecord{}, err
	}
	return record, nil
}

type extractedManifest struct {
	Files []manifestFile `json:"files"`
}

type manifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Mode   int64  `json:"mode"`
}

func extractSnapshot(archive []byte, destination string, profile workspaceProfile) (extractedManifest, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return extractedManifest{}, errors.New("source snapshot is not valid gzip")
	}
	defer func() { _ = gzipReader.Close() }()
	tarReader := tar.NewReader(gzipReader)
	manifest := extractedManifest{}
	seen := make(map[string]struct{})
	var total int64
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return extractedManifest{}, errors.New("read source snapshot archive")
		}
		name, pathErr := normalizeWorkspacePath(header.Name)
		if pathErr != nil || profile.denies(name) || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > profile.Limits.MaximumFileBytes {
			return extractedManifest{}, errors.New("source snapshot contains a disallowed entry")
		}
		if _, exists := seen[name]; exists || len(seen)+1 > profile.Limits.MaximumSnapshotFiles {
			return extractedManifest{}, errors.New("source snapshot file set is invalid")
		}
		seen[name] = struct{}{}
		total += header.Size
		if total > profile.Limits.MaximumSnapshotBytes {
			return extractedManifest{}, errors.New("source snapshot exceeds workspace limits")
		}
		target, err := safeWorkspacePath(destination, name)
		if err != nil {
			return extractedManifest{}, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return extractedManifest{}, errors.New("create source directory")
		}
		mode := fs.FileMode(0o640)
		if header.Mode&0o111 != 0 {
			mode = 0o750
		}
		// #nosec G304,G703 -- target is derived from a normalized archive path and containment-checked.
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return extractedManifest{}, errors.New("create source file")
		}
		hash := sha256.New()
		written, copyErr := io.CopyN(io.MultiWriter(file, hash), tarReader, header.Size)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			return extractedManifest{}, errors.New("extract source file")
		}
		manifest.Files = append(manifest.Files, manifestFile{Path: name, SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: header.Size, Mode: int64(mode.Perm())})
	}
	if len(manifest.Files) == 0 {
		return extractedManifest{}, errors.New("source snapshot contains no files")
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	return manifest, nil
}

func safeWorkspacePath(root, relative string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(relative))
	relativeToRoot, err := filepath.Rel(root, target)
	if err != nil || relativeToRoot == "." || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
		return "", errors.New("workspace path escapes its root")
	}
	return target, nil
}

func (operations *workspaceOperations) stageCandidate(ctx context.Context, caller string, request stageCandidateRequest) (candidateRecord, error) {
	for _, group := range [][]candidateFile{request.Changes, request.Tests} {
		for _, file := range group {
			if err := validateCandidateInputContent(file); err != nil {
				return candidateRecord{}, policyFailure("CANDIDATE_CONTENT_INVALID", err.Error())
			}
		}
	}
	workspace, profile, err := operations.loadWorkspace(ctx, request.WorkspaceID)
	if err != nil {
		return candidateRecord{}, err
	}
	if request.CaseID != workspace.CaseID {
		return candidateRecord{}, errors.New("case scope does not match the workspace")
	}
	if !profile.allowsStage(caller) {
		return candidateRecord{}, errStageForbidden
	}
	if !goTestPattern.MatchString(request.ExpectedTest) {
		return candidateRecord{}, policyFailure("EXPECTED_TEST_INVALID", "expected_test must be the exact Go test function name (Test...) with no sentence, command, package, or explanation")
	}
	if len(request.Changes) == 0 || len(request.Tests) == 0 || len(request.Changes)+len(request.Tests) > profile.Limits.MaximumCandidateFiles {
		return candidateRecord{}, policyFailure("CANDIDATE_SET_INVALID", "Candidate requires a valid expected Go test, at least one implementation change, at least one test file, and a file count within profile limits")
	}
	files, total, err := canonicalCandidateFiles(profile, request.Changes, request.Tests)
	if err != nil {
		return candidateRecord{}, err
	}
	workspaceRoot := filepath.Join(operations.root, workspace.WorkspaceID)
	baseline := filepath.Join(workspaceRoot, "baseline")
	files, err = resolveCandidateFileModes(baseline, files)
	if err != nil {
		return candidateRecord{}, err
	}
	candidateID, testArtifactID := candidateIdentity(workspace.WorkspaceID, request.ExpectedTest, files, len(request.Changes))
	var existing candidateRecord
	if err := operations.store.get(ctx, "candidate", candidateID, &existing); err == nil {
		validated, _, _, validationErr := operations.loadCandidate(ctx, candidateID)
		return validated, validationErr
	} else if !errors.Is(err, errWorkspaceRecordNotFound) {
		return candidateRecord{}, err
	}
	candidateTemporary, err := os.MkdirTemp(workspaceRoot, ".candidate-")
	if err != nil {
		return candidateRecord{}, errors.New("create candidate staging directory")
	}
	defer func() { _ = os.RemoveAll(candidateTemporary) }()
	// #nosec G302 -- sandbox Jobs use a separate non-root UID in the workspace pod's scoped fsGroup and require group traversal.
	if err := os.Chmod(candidateTemporary, 0o750); err != nil {
		return candidateRecord{}, errors.New("set candidate staging permissions")
	}
	reproductionTemporary, err := os.MkdirTemp(workspaceRoot, ".reproduction-")
	if err != nil {
		return candidateRecord{}, errors.New("create reproduction staging directory")
	}
	defer func() { _ = os.RemoveAll(reproductionTemporary) }()
	// #nosec G302 -- sandbox Jobs use a separate non-root UID in the workspace pod's scoped fsGroup and require group traversal.
	if err := os.Chmod(reproductionTemporary, 0o750); err != nil {
		return candidateRecord{}, errors.New("set reproduction staging permissions")
	}
	if err := copyRegularTree(baseline, candidateTemporary); err != nil {
		return candidateRecord{}, err
	}
	if err := copyRegularTree(baseline, reproductionTemporary); err != nil {
		return candidateRecord{}, err
	}
	for index, file := range files {
		isTest := index >= len(request.Changes)
		if err := applyCandidateFile(candidateTemporary, file); err != nil {
			return candidateRecord{}, err
		}
		if isTest {
			if err := applyCandidateFile(reproductionTemporary, file); err != nil {
				return candidateRecord{}, err
			}
		}
	}
	if err := os.Rename(candidateTemporary, filepath.Join(workspaceRoot, candidateID+"-candidate")); err != nil && !errors.Is(err, fs.ErrExist) {
		return candidateRecord{}, errors.New("publish candidate tree")
	}
	if err := os.Rename(reproductionTemporary, filepath.Join(workspaceRoot, candidateID+"-reproduction")); err != nil && !errors.Is(err, fs.ErrExist) {
		return candidateRecord{}, errors.New("publish reproduction tree")
	}
	createdAt := operations.now()
	record := candidateRecord{
		SchemaVersion: 1, CandidateID: candidateID, TestArtifactID: testArtifactID,
		WorkspaceID: workspace.WorkspaceID, ProfileID: workspace.ProfileID, ProfileSHA256: workspace.ProfileSHA256, CaseID: workspace.CaseID,
		RepositoryID: workspace.RepositoryID, SnapshotID: workspace.SnapshotID, RevisionSHA: workspace.RevisionSHA,
		ExpectedTest: request.ExpectedTest, ChangeCount: len(request.Changes), TestCount: len(request.Tests),
		CandidateBytes: total, ProposedBy: caller, CreatedAt: createdAt, ExpiresAt: createdAt.Add(profile.ttl()), Files: files,
	}
	if err := operations.store.putImmutable(ctx, "candidate", candidateID, record, profile.ttl()); err != nil {
		return candidateRecord{}, err
	}
	return record, nil
}

var errStageForbidden = errors.New("caller cannot stage candidates")

func canonicalCandidateFiles(profile workspaceProfile, changes, tests []candidateFile) ([]candidateFile, int64, error) {
	seen := make(map[string]struct{}, len(changes)+len(tests))
	result := make([]candidateFile, 0, len(changes)+len(tests))
	var total int64
	validate := func(file candidateFile, test bool) (candidateFile, error) {
		normalized, err := normalizeWorkspacePath(file.Path)
		if err != nil || profile.denies(normalized) || (test && !strings.HasSuffix(normalized, "_test.go")) || (!test && strings.HasSuffix(normalized, "_test.go")) || (test && file.Delete) {
			return candidateFile{}, policyFailure("CANDIDATE_PATH_REJECTED", "Candidate contains a disallowed path, file category, or operation")
		}
		if _, exists := seen[normalized]; exists {
			return candidateFile{}, policyFailure("CANDIDATE_PATH_DUPLICATED", "Candidate change and test paths must be unique")
		}
		seen[normalized] = struct{}{}
		if file.Mode != "" || file.BaseSHA256 != "absent" && !sha256Pattern.MatchString(file.BaseSHA256) {
			return candidateFile{}, policyFailure("BASE_IDENTITY_INVALID", `Each candidate file must omit mode and use either the exact existing-file content SHA-256 or the literal base_sha256 value "absent" for a new path`)
		}
		if file.ContentText != nil && (file.contentBase64Present || file.ContentBase64 != "") {
			return candidateFile{}, errors.New("candidate file must use only one content encoding")
		}
		if file.Delete && (file.ContentBase64 != "" || file.contentBase64Present || file.ContentText != nil) {
			return candidateFile{}, errors.New("deleted candidate files cannot have content")
		}
		if !file.Delete {
			content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
			if file.ContentText != nil {
				if !utf8.ValidString(*file.ContentText) {
					return candidateFile{}, errors.New("candidate text is not valid UTF-8")
				}
				content = []byte(*file.ContentText)
			}
			if err != nil || int64(len(content)) > profile.Limits.MaximumFileBytes || bytes.IndexByte(content, 0) >= 0 {
				return candidateFile{}, errors.New("candidate file content is invalid")
			}
			total += int64(len(content))
			if total > profile.Limits.MaximumCandidateBytes {
				return candidateFile{}, errors.New("candidate exceeds its byte limit")
			}
			file.ContentBase64 = base64.StdEncoding.EncodeToString(content)
			file.ContentText = nil
		}
		file.contentBase64Present = false
		file.Path = normalized
		return file, nil
	}
	for _, file := range changes {
		valid, err := validate(file, false)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, valid)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	changeCount := len(result)
	testFiles := make([]candidateFile, 0, len(tests))
	for _, file := range tests {
		valid, err := validate(file, true)
		if err != nil {
			return nil, 0, err
		}
		testFiles = append(testFiles, valid)
	}
	sort.Slice(testFiles, func(i, j int) bool { return testFiles[i].Path < testFiles[j].Path })
	result = append(result, testFiles...)
	if changeCount != len(changes) {
		return nil, 0, errors.New("candidate canonicalization failed")
	}
	return result, total, nil
}

func resolveCandidateFileModes(baseline string, files []candidateFile) ([]candidateFile, error) {
	resolved := append([]candidateFile(nil), files...)
	for index := range resolved {
		file := &resolved[index]
		target, err := safeWorkspacePath(baseline, file.Path)
		if err != nil {
			return nil, err
		}
		content, readErr := os.ReadFile(target) // #nosec G304,G703 -- normalized and containment-checked above.
		if file.BaseSHA256 == "absent" {
			if readErr == nil || !errors.Is(readErr, fs.ErrNotExist) || file.Delete {
				return nil, policyFailure("BASE_IDENTITY_MISMATCH", `The literal base_sha256 value "absent" may be used only when the path is absent from the immutable workspace snapshot`)
			}
			file.Mode = "100644"
			continue
		}
		if readErr != nil {
			return nil, policyFailure("BASE_IDENTITY_MISMATCH", "Candidate path does not exist in the immutable workspace snapshot; use the literal base_sha256 value absent only when creating a new path")
		}
		digest := sha256.Sum256(content)
		if hex.EncodeToString(digest[:]) != file.BaseSHA256 {
			return nil, policyFailure("BASE_IDENTITY_MISMATCH", "Candidate base_sha256 does not match this path's content_sha256 in the immutable workspace snapshot")
		}
		info, err := os.Stat(target) // #nosec G304,G703 -- normalized and containment-checked above.
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("candidate base path is not a regular file")
		}
		if file.Delete {
			file.Mode = ""
		} else if info.Mode()&0o111 != 0 {
			file.Mode = "100755"
		} else {
			file.Mode = "100644"
		}
	}
	return resolved, nil
}

func canonicalStoredCandidateFiles(profile workspaceProfile, baseline string, changes, tests []candidateFile) ([]candidateFile, int64, error) {
	stored := append(append([]candidateFile(nil), changes...), tests...)
	requestChanges := append([]candidateFile(nil), changes...)
	requestTests := append([]candidateFile(nil), tests...)
	for index := range requestChanges {
		requestChanges[index].Mode = ""
	}
	for index := range requestTests {
		requestTests[index].Mode = ""
	}
	canonical, total, err := canonicalCandidateFiles(profile, requestChanges, requestTests)
	if err != nil {
		return nil, 0, err
	}
	canonical, err = resolveCandidateFileModes(baseline, canonical)
	if err != nil || len(canonical) != len(stored) {
		return nil, 0, errors.New("candidate mode identity is invalid")
	}
	for index := range canonical {
		// Encoding presence is a wire-decoding fact, not part of canonical
		// file identity. Stored text must still be rejected by the comparison.
		stored[index].contentBase64Present = false
		if canonical[index] != stored[index] {
			return nil, 0, errors.New("candidate file identity is not canonical")
		}
	}
	return canonical, total, nil
}

func copyRegularTree(source, destination string) error {
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return errors.New("open baseline root")
	}
	defer func() { _ = sourceRoot.Close() }()
	destinationRoot, err := os.OpenRoot(destination)
	if err != nil {
		return errors.New("open destination root")
	}
	defer func() { _ = destinationRoot.Close() }()
	return fs.WalkDir(sourceRoot.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("walk baseline tree")
		}
		if relative == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return errors.New("inspect baseline entry")
		}
		if entry.IsDir() {
			return destinationRoot.Mkdir(relative, 0o750)
		}
		if !info.Mode().IsRegular() {
			return errors.New("baseline contains a non-regular entry")
		}
		input, err := sourceRoot.Open(relative)
		if err != nil {
			return errors.New("open baseline file")
		}
		mode := fs.FileMode(0o640)
		if info.Mode()&0o111 != 0 {
			mode = 0o750
		}
		output, err := destinationRoot.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			_ = input.Close()
			return errors.New("create copied source file")
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		outputCloseErr := output.Close()
		if copyErr != nil || inputCloseErr != nil || outputCloseErr != nil {
			return errors.New("copy baseline file")
		}
		return nil
	})
}

func applyCandidateFile(root string, file candidateFile) error {
	target, err := safeWorkspacePath(root, file.Path)
	if err != nil {
		return err
	}
	existing, readErr := os.ReadFile(target) // #nosec G304,G703 -- normalized and containment-checked above.
	mode := fs.FileMode(0o640)
	if !file.Delete && file.Mode != "100644" && file.Mode != "100755" {
		return errors.New("candidate file mode is invalid")
	}
	if file.Mode == "100755" {
		mode = 0o750
	}
	if file.BaseSHA256 == "absent" {
		if readErr == nil || !errors.Is(readErr, fs.ErrNotExist) {
			return errors.New("candidate expected an absent path")
		}
	} else {
		if readErr != nil {
			return errors.New("candidate base file is unavailable")
		}
		digest := sha256.Sum256(existing)
		if hex.EncodeToString(digest[:]) != file.BaseSHA256 {
			return errors.New("candidate base digest does not match")
		}
		info, err := os.Stat(target) // #nosec G304,G703 -- normalized and containment-checked above.
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("candidate base path is not a regular file")
		}
		expectedMode := "100644"
		if info.Mode()&0o111 != 0 {
			expectedMode = "100755"
		}
		if !file.Delete && file.Mode != expectedMode {
			return errors.New("candidate file mode does not match the baseline")
		}
	}
	if file.Delete {
		if err := os.Remove(target); err != nil {
			return errors.New("delete candidate file")
		}
		return nil
	}
	content, _ := base64.StdEncoding.DecodeString(file.ContentBase64)
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return errors.New("create candidate file directory")
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".candidate-file-")
	if err != nil {
		return errors.New("create temporary candidate file")
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return errors.New("set candidate file mode")
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return errors.New("write candidate file")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close candidate file")
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return errors.New("publish candidate file")
	}
	return nil
}

func (operations *workspaceOperations) loadWorkspace(ctx context.Context, workspaceID string) (workspaceRecord, workspaceProfile, error) {
	if !workspaceIDPattern.MatchString(workspaceID) {
		return workspaceRecord{}, workspaceProfile{}, policyFailure("WORKSPACE_ID_INVALID", "workspace_id must be the exact ws_-prefixed handle returned by create_workspace")
	}
	var record workspaceRecord
	if err := operations.store.get(ctx, "workspace", workspaceID, &record); err != nil {
		return workspaceRecord{}, workspaceProfile{}, err
	}
	profile, ok := operations.profiles.get(record.ProfileID)
	expectedDigest := sha256.Sum256([]byte(record.ProfileID + "\x00" + record.ProfileSHA256 + "\x00" + record.SourceProfileSHA256 + "\x00" + record.CaseID + "\x00" + record.SnapshotID))
	expectedWorkspaceID := "ws_" + hex.EncodeToString(expectedDigest[:])
	if !ok || record.SchemaVersion != 1 || record.WorkspaceID != workspaceID || record.WorkspaceID != expectedWorkspaceID || record.ProfileSHA256 != profile.profileSHA256 || !sha256Pattern.MatchString(record.SourceProfileSHA256) || !caseIDPattern.MatchString(record.CaseID) || record.RepositoryID != profile.RepositoryID || !snapshotIDPattern.MatchString(record.SnapshotID) || !gitSHAPattern.MatchString(record.RevisionSHA) || !gitSHAPattern.MatchString(record.TreeSHA) || !sha256Pattern.MatchString(record.ArchiveSHA256) || record.SnapshotID != "src_"+record.ArchiveSHA256 || record.ExpiresAt.Before(operations.now()) {
		return workspaceRecord{}, workspaceProfile{}, errors.New("workspace record is invalid or expired")
	}
	return record, profile, nil
}

func (operations *workspaceOperations) loadCandidate(ctx context.Context, candidateID string) (candidateRecord, workspaceRecord, workspaceProfile, error) {
	if !candidateIDPattern.MatchString(candidateID) {
		return candidateRecord{}, workspaceRecord{}, workspaceProfile{}, errors.New("candidate identifier is invalid")
	}
	var candidate candidateRecord
	if err := operations.store.get(ctx, "candidate", candidateID, &candidate); err != nil {
		return candidateRecord{}, workspaceRecord{}, workspaceProfile{}, err
	}
	workspace, profile, err := operations.loadWorkspace(ctx, candidate.WorkspaceID)
	if err != nil || candidate.SchemaVersion != 1 || candidate.CandidateID != candidateID || !testArtifactIDPattern.MatchString(candidate.TestArtifactID) || candidate.ProfileID != workspace.ProfileID || candidate.ProfileSHA256 != workspace.ProfileSHA256 || candidate.CaseID != workspace.CaseID || candidate.RepositoryID != workspace.RepositoryID || candidate.SnapshotID != workspace.SnapshotID || candidate.RevisionSHA != workspace.RevisionSHA || !goTestPattern.MatchString(candidate.ExpectedTest) || candidate.ChangeCount < 1 || candidate.TestCount < 1 || len(candidate.Files) != candidate.ChangeCount+candidate.TestCount || candidate.ExpiresAt.Before(operations.now()) {
		return candidateRecord{}, workspaceRecord{}, workspaceProfile{}, errors.New("candidate record is invalid or expired")
	}
	baseline := filepath.Join(operations.root, workspace.WorkspaceID, "baseline")
	canonical, total, canonicalErr := canonicalStoredCandidateFiles(profile, baseline, candidate.Files[:candidate.ChangeCount], candidate.Files[candidate.ChangeCount:])
	if canonicalErr != nil {
		return candidateRecord{}, workspaceRecord{}, workspaceProfile{}, errors.New("candidate record failed content identity validation")
	}
	expectedCandidateID, expectedTestArtifactID := candidateIdentity(workspace.WorkspaceID, candidate.ExpectedTest, canonical, candidate.ChangeCount)
	if expectedCandidateID != candidate.CandidateID || expectedTestArtifactID != candidate.TestArtifactID || total != candidate.CandidateBytes {
		return candidateRecord{}, workspaceRecord{}, workspaceProfile{}, errors.New("candidate record failed content identity validation")
	}
	return candidate, workspace, profile, nil
}

func candidateIdentity(workspaceID, expectedTest string, files []candidateFile, changeCount int) (string, string) {
	digest := sha256.New()
	_, _ = io.WriteString(digest, workspaceID+"\x00"+expectedTest+"\x00")
	for _, file := range files {
		encoded, _ := json.Marshal(file)
		_, _ = digest.Write(encoded)
		_, _ = digest.Write([]byte{0})
	}
	testHash := sha256.New()
	for _, file := range files[changeCount:] {
		encoded, _ := json.Marshal(file)
		_, _ = testHash.Write(encoded)
		_, _ = testHash.Write([]byte{0})
	}
	return "cand_" + hex.EncodeToString(digest.Sum(nil)), "test_" + hex.EncodeToString(testHash.Sum(nil))
}

func observedNamedTestFailure(content []byte, testName string) bool {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Action == "fail" && event.Test == testName {
			return true
		}
	}
	return false
}

func publicCandidate(record candidateRecord) candidateResponse {
	return candidateResponse{
		SchemaVersion: record.SchemaVersion, CandidateID: record.CandidateID, TestArtifactID: record.TestArtifactID,
		WorkspaceID: record.WorkspaceID, ProfileID: record.ProfileID, ProfileSHA256: record.ProfileSHA256, CaseID: record.CaseID,
		RepositoryID: record.RepositoryID, SnapshotID: record.SnapshotID, RevisionSHA: record.RevisionSHA,
		ExpectedTest: record.ExpectedTest, ChangeCount: record.ChangeCount, TestCount: record.TestCount,
		CandidateBytes: record.CandidateBytes, ProposedBy: record.ProposedBy,
		CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
	}
}
