package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type sourceOperations struct {
	client *githubSourceClient
	store  snapshotStore
	now    func() time.Time
}

func (operations *sourceOperations) resolveRevision(ctx context.Context, profile sourceProfile, requested string) (resolvedRevisionResponse, error) {
	if requested == "" {
		requested = profile.DefaultRef
	}
	if !profile.allowsRevision(requested) {
		return resolvedRevisionResponse{}, errors.New("revision is not allowed by the source profile")
	}
	commit, err := operations.client.resolveCommit(ctx, profile, requested)
	if err != nil {
		return resolvedRevisionResponse{}, err
	}
	return resolvedRevisionResponse{
		Limits:              profile.Limits,
		RepositoryID:        profile.RepositoryID,
		SourceProfileSHA256: profile.profileSHA256,
		Repository:          profile.repositoryName(),
		RequestedRevision:   requested,
		RevisionSHA:         commit.SHA,
		TreeSHA:             commit.Commit.Tree.SHA,
		AssessmentReady:     profile.AssessmentReady,
		ResolvedAt:          operations.now(),
		Limitations:         append([]string{}, profile.Limitations...),
	}, nil
}

func (operations *sourceOperations) loadExactTree(ctx context.Context, profile sourceProfile, revisionSHA string) (githubCommit, githubTree, error) {
	commit, err := exactCommit(ctx, operations.client, profile, revisionSHA)
	if err != nil {
		return githubCommit{}, githubTree{}, err
	}
	tree, err := operations.client.getTree(ctx, profile, commit.Commit.Tree.SHA)
	if err != nil {
		return githubCommit{}, githubTree{}, err
	}
	return commit, tree, nil
}

func (operations *sourceOperations) listTree(ctx context.Context, profile sourceProfile, request listTreeRequest) (listTreeResponse, error) {
	prefix, err := normalizeSourcePath(request.PathPrefix, true)
	if err != nil {
		return listTreeResponse{}, errors.New("path prefix is invalid")
	}
	after, err := normalizeSourcePath(request.After, true)
	if err != nil {
		return listTreeResponse{}, errors.New("tree cursor is invalid")
	}
	if request.Limit == 0 {
		request.Limit = profile.Limits.MaximumTreeResults
	}
	if request.Limit < 1 || request.Limit > profile.Limits.MaximumTreeResults {
		return listTreeResponse{}, errors.New("tree result limit is invalid")
	}
	_, tree, err := operations.loadExactTree(ctx, profile, request.RevisionSHA)
	if err != nil {
		return listTreeResponse{}, err
	}
	entries := append([]githubTreeEntry(nil), tree.Tree...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	result := make([]sourceTreeEntry, 0, request.Limit)
	excluded := 0
	more := false
	for _, entry := range entries {
		if profile.denies(entry.Path) {
			excluded++
			continue
		}
		if prefix != "" && entry.Path != prefix && !strings.HasPrefix(entry.Path, prefix+"/") {
			continue
		}
		if after != "" && entry.Path <= after {
			continue
		}
		if len(result) == request.Limit {
			more = true
			break
		}
		result = append(result, sourceTreeEntry{Path: entry.Path, Type: entry.Type, Mode: entry.Mode, SHA: entry.SHA, Size: entry.Size})
	}
	response := listTreeResponse{
		RepositoryID: profile.RepositoryID, RevisionSHA: request.RevisionSHA, TreeSHA: tree.SHA,
		PathPrefix: prefix, Entries: result, ReturnedCount: len(result),
		Truncated:   more || tree.Truncated,
		Coverage:    sourceCoverage{Complete: !tree.Truncated, UpstreamTruncated: tree.Truncated, ExcludedEntries: excluded},
		CollectedAt: operations.now(),
	}
	if more && len(result) > 0 {
		response.NextAfter = result[len(result)-1].Path
	}
	return response, nil
}

func (operations *sourceOperations) readFile(ctx context.Context, profile sourceProfile, request readFileRequest) (readFileResponse, error) {
	normalized, err := normalizeSourcePath(request.Path, false)
	if err != nil || profile.denies(normalized) {
		return readFileResponse{}, errors.New("source path is not allowed")
	}
	if request.ByteStart < 0 {
		return readFileResponse{}, errors.New("byte_start must be non-negative")
	}
	if request.ByteLimit == 0 {
		request.ByteLimit = profile.Limits.MaximumReadBytes
	}
	if request.ByteLimit < 1 || request.ByteLimit > profile.Limits.MaximumReadBytes {
		return readFileResponse{}, errors.New("byte_limit is outside the source profile")
	}
	_, tree, err := operations.loadExactTree(ctx, profile, request.RevisionSHA)
	if err != nil {
		return readFileResponse{}, err
	}
	entry, ok := findRegularBlob(tree.Tree, normalized)
	if !ok {
		return readFileResponse{}, &sourceFailure{kind: sourceFailureNotFound}
	}
	if entry.Size > profile.Limits.MaximumFileBytes {
		return readFileResponse{}, errors.New("source file exceeds the profile limit")
	}
	content, err := operations.client.getBlob(ctx, profile, entry.SHA)
	if err != nil {
		return readFileResponse{}, err
	}
	if int64(len(content)) != entry.Size {
		return readFileResponse{}, &sourceFailure{kind: sourceFailureInvalid}
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return readFileResponse{}, errors.New("source file is not supported UTF-8 text")
	}
	total := int64(len(content))
	if request.ByteStart > total {
		return readFileResponse{}, errors.New("byte_start exceeds source file size")
	}
	end := request.ByteStart + request.ByteLimit
	if end > total {
		end = total
	}
	digest := sha256.Sum256(content)
	return readFileResponse{
		RepositoryID: profile.RepositoryID, RevisionSHA: request.RevisionSHA, TreeSHA: tree.SHA,
		Path: normalized, BlobSHA: entry.SHA, ContentSHA256: hex.EncodeToString(digest[:]),
		ByteStart: request.ByteStart, ByteEnd: end, TotalBytes: total,
		Content: string(content[request.ByteStart:end]), Truncated: end < total, CollectedAt: operations.now(),
	}, nil
}

func (operations *sourceOperations) search(ctx context.Context, profile sourceProfile, request searchSourceRequest) (searchSourceResponse, error) {
	prefix, err := normalizeSourcePath(request.PathPrefix, true)
	if err != nil {
		return searchSourceResponse{}, errors.New("path prefix is invalid")
	}
	if len(request.Query) < 2 || len(request.Query) > 128 || strings.ContainsRune(request.Query, 0) {
		return searchSourceResponse{}, errors.New("literal search query is invalid")
	}
	if request.Limit == 0 {
		request.Limit = profile.Limits.MaximumSearchResults
	}
	if request.Limit < 1 || request.Limit > profile.Limits.MaximumSearchResults {
		return searchSourceResponse{}, errors.New("search result limit is invalid")
	}
	_, tree, err := operations.loadExactTree(ctx, profile, request.RevisionSHA)
	if err != nil {
		return searchSourceResponse{}, err
	}
	entries := append([]githubTreeEntry(nil), tree.Tree...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	needle := request.Query
	if !request.CaseSensitive {
		needle = strings.ToLower(needle)
	}
	matches := make([]sourceMatch, 0, request.Limit)
	coverage := sourceCoverage{Complete: !tree.Truncated, UpstreamTruncated: tree.Truncated}
	truncated := tree.Truncated
	for _, entry := range entries {
		if entry.Type != "blob" {
			continue
		}
		if !isRegularMode(entry.Mode) || profile.denies(entry.Path) || !isLikelyTextPath(entry.Path) {
			coverage.ExcludedEntries++
			continue
		}
		if prefix != "" && entry.Path != prefix && !strings.HasPrefix(entry.Path, prefix+"/") {
			continue
		}
		if coverage.ScannedFiles >= profile.Limits.MaximumSearchFiles || coverage.ScannedBytes+entry.Size > profile.Limits.MaximumSearchBytes {
			coverage.Complete = false
			truncated = true
			break
		}
		if entry.Size > profile.Limits.MaximumFileBytes {
			coverage.SkippedFiles++
			coverage.Complete = false
			continue
		}
		content, err := operations.client.getBlob(ctx, profile, entry.SHA)
		if err != nil {
			return searchSourceResponse{}, err
		}
		if int64(len(content)) != entry.Size {
			return searchSourceResponse{}, &sourceFailure{kind: sourceFailureInvalid}
		}
		coverage.ScannedFiles++
		coverage.ScannedBytes += int64(len(content))
		if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
			coverage.SkippedFiles++
			coverage.Complete = false
			continue
		}
		for index, line := range strings.Split(string(content), "\n") {
			haystack := line
			if !request.CaseSensitive {
				haystack = strings.ToLower(line)
			}
			if strings.Contains(haystack, needle) {
				if len(matches) == request.Limit {
					truncated = true
					break
				}
				matches = append(matches, sourceMatch{Path: entry.Path, BlobSHA: entry.SHA, LineNumber: index + 1, Line: truncateUTF8(line, 500)})
			}
		}
		if len(matches) == request.Limit {
			coverage.Complete = false
			break
		}
	}
	return searchSourceResponse{
		RepositoryID: profile.RepositoryID, RevisionSHA: request.RevisionSHA, TreeSHA: tree.SHA,
		PathPrefix: prefix, Matches: matches, ReturnedCount: len(matches), Truncated: truncated,
		Coverage: coverage, CollectedAt: operations.now(),
	}, nil
}

func (operations *sourceOperations) createSnapshot(ctx context.Context, profile sourceProfile, revisionSHA string) (snapshotResponse, error) {
	_, tree, err := operations.loadExactTree(ctx, profile, revisionSHA)
	if err != nil {
		return snapshotResponse{}, err
	}
	if tree.Truncated {
		return snapshotResponse{}, errors.New("upstream tree is truncated; a complete snapshot cannot be proven")
	}
	entries := make([]githubTreeEntry, 0, len(tree.Tree))
	excluded := 0
	var declaredBytes int64
	for _, entry := range tree.Tree {
		if entry.Type != "blob" {
			continue
		}
		if !isRegularMode(entry.Mode) || profile.denies(entry.Path) {
			excluded++
			continue
		}
		if entry.Size > profile.Limits.MaximumFileBytes {
			return snapshotResponse{}, errors.New("snapshot contains a file above the approved limit")
		}
		declaredBytes += entry.Size
		if len(entries)+1 > profile.Limits.MaximumSnapshotFiles || declaredBytes > profile.Limits.MaximumSnapshotBytes {
			return snapshotResponse{}, errors.New("snapshot exceeds the approved file or byte limit")
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return snapshotResponse{}, errors.New("snapshot contains no allowed regular files")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	archive, actualBytes, err := operations.buildArchive(ctx, profile, entries)
	if err != nil {
		return snapshotResponse{}, err
	}
	archiveDigest := sha256.Sum256(archive)
	digest := hex.EncodeToString(archiveDigest[:])
	createdAt := operations.now()
	ttl := time.Duration(profile.Limits.SnapshotTTLSeconds) * time.Second
	metadata := snapshotMetadata{
		SchemaVersion: 1, SnapshotID: "src_" + digest,
		RepositoryID: profile.RepositoryID, SourceProfileSHA256: profile.profileSHA256,
		AssessmentReady: profile.AssessmentReady, Repository: profile.repositoryName(), RevisionSHA: revisionSHA,
		TreeSHA: tree.SHA, ArchiveSHA256: digest, ArchiveBytes: int64(len(archive)),
		UncompressedBytes: actualBytes, FileCount: len(entries), ExcludedEntries: excluded,
		Format: "application/gzip", CreatedAt: createdAt, ExpiresAt: createdAt.Add(ttl),
	}
	if err := operations.store.put(ctx, metadata, archive, ttl); err != nil {
		return snapshotResponse{}, err
	}
	return snapshotResponse{snapshotMetadata: metadata, Limitations: append([]string{}, profile.Limitations...)}, nil
}

func (operations *sourceOperations) buildArchive(ctx context.Context, profile sourceProfile, entries []githubTreeEntry) ([]byte, int64, error) {
	var buffer bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return nil, 0, errors.New("create snapshot compressor")
	}
	gzipWriter.ModTime = time.Time{}
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	var total int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		content, err := operations.client.getBlob(ctx, profile, entry.SHA)
		if err != nil {
			return nil, 0, err
		}
		if int64(len(content)) != entry.Size {
			return nil, 0, &sourceFailure{kind: sourceFailureInvalid}
		}
		total += int64(len(content))
		if total > profile.Limits.MaximumSnapshotBytes {
			return nil, 0, errors.New("snapshot exceeded its byte limit while materializing")
		}
		mode := int64(0o644)
		if entry.Mode == "100755" {
			mode = 0o755
		}
		header := &tar.Header{
			Name: entry.Path, Mode: mode, Size: int64(len(content)),
			ModTime: time.Unix(0, 0).UTC(), AccessTime: time.Unix(0, 0).UTC(), ChangeTime: time.Unix(0, 0).UTC(),
			Format: tar.FormatPAX,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, 0, errors.New("write snapshot header")
		}
		if _, err := tarWriter.Write(content); err != nil {
			return nil, 0, errors.New("write snapshot content")
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, 0, errors.New("finalize snapshot archive")
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, 0, errors.New("finalize snapshot compression")
	}
	if int64(buffer.Len()) > profile.Limits.MaximumSnapshotBytes {
		return nil, 0, errors.New("compressed snapshot exceeds the approved limit")
	}
	return buffer.Bytes(), total, nil
}

func findRegularBlob(entries []githubTreeEntry, wanted string) (githubTreeEntry, bool) {
	for _, entry := range entries {
		if entry.Path == wanted && entry.Type == "blob" && isRegularMode(entry.Mode) {
			return entry, true
		}
	}
	return githubTreeEntry{}, false
}

func isRegularMode(mode string) bool {
	return mode == "100644" || mode == "100755"
}

func isLikelyTextPath(name string) bool {
	base := filepath.Base(name)
	switch base {
	case "Dockerfile", "Makefile", "Procfile", "go.mod", "go.sum":
		return true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".md", ".txt", ".json", ".yaml", ".yml", ".toml", ".sh", ".js", ".jsx", ".ts", ".tsx", ".py", ".java", ".rs", ".c", ".h", ".cpp", ".sql", ".html", ".css", ".xml":
		return true
	default:
		return false
	}
}

func truncateUTF8(value string, maximumBytes int) string {
	if len(value) <= maximumBytes {
		return value
	}
	truncated := value[:maximumBytes]
	for !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + "…"
}
