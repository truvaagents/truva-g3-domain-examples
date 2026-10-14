package main

import "time"

type sourceLimits struct {
	TimeoutMilliseconds          int64 `json:"timeout_milliseconds"`
	MaximumAPIResponseBytes      int64 `json:"maximum_api_response_bytes"`
	MaximumFileBytes             int64 `json:"maximum_file_bytes"`
	MaximumReadBytes             int64 `json:"maximum_read_bytes"`
	MaximumTreeEntries           int   `json:"maximum_tree_entries"`
	MaximumTreeResults           int   `json:"maximum_tree_results"`
	MaximumSearchFiles           int   `json:"maximum_search_files"`
	MaximumSearchBytes           int64 `json:"maximum_search_bytes"`
	MaximumSearchResults         int   `json:"maximum_search_results"`
	MaximumSnapshotFiles         int   `json:"maximum_snapshot_files"`
	MaximumSnapshotBytes         int64 `json:"maximum_snapshot_bytes"`
	MaximumSnapshotDownloadBytes int64 `json:"maximum_snapshot_download_bytes"`
	SnapshotTTLSeconds           int64 `json:"snapshot_ttl_seconds"`
}

type sourceProfile struct {
	SchemaVersion    int          `json:"schema_version"`
	RepositoryID     string       `json:"repository_id"`
	AssessmentReady  bool         `json:"assessment_ready"`
	Owner            string       `json:"owner"`
	Repository       string       `json:"repository"`
	DefaultRef       string       `json:"default_ref"`
	AllowedNamedRefs []string     `json:"allowed_named_refs"`
	DeniedPaths      []string     `json:"denied_paths"`
	AllowedCallers   []string     `json:"allowed_callers"`
	Limits           sourceLimits `json:"limits"`
	Limitations      []string     `json:"limitations"`

	namedRefSet   map[string]struct{}
	callerSet     map[string]struct{}
	profileSHA256 string
}

type resolveRevisionRequest struct {
	RepositoryID string `json:"repository_id"`
	Revision     string `json:"revision,omitempty"`
}

type resolvedRevisionResponse struct {
	Limits              sourceLimits `json:"limits"`
	RepositoryID        string       `json:"repository_id"`
	SourceProfileSHA256 string       `json:"source_profile_sha256"`
	Repository          string       `json:"repository"`
	RequestedRevision   string       `json:"requested_revision"`
	RevisionSHA         string       `json:"revision_sha"`
	TreeSHA             string       `json:"tree_sha"`
	AssessmentReady     bool         `json:"assessment_ready"`
	ResolvedAt          time.Time    `json:"resolved_at"`
	Limitations         []string     `json:"limitations"`
}

type listTreeRequest struct {
	RepositoryID string `json:"repository_id"`
	RevisionSHA  string `json:"revision_sha"`
	PathPrefix   string `json:"path_prefix,omitempty"`
	After        string `json:"after,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

type sourceTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Mode string `json:"mode"`
	SHA  string `json:"sha"`
	Size int64  `json:"size_bytes,omitempty"`
}

type sourceCoverage struct {
	Complete          bool  `json:"complete"`
	UpstreamTruncated bool  `json:"upstream_truncated"`
	ExcludedEntries   int   `json:"excluded_entries"`
	ScannedFiles      int   `json:"scanned_files,omitempty"`
	ScannedBytes      int64 `json:"scanned_bytes,omitempty"`
	SkippedFiles      int   `json:"skipped_files,omitempty"`
}

type listTreeResponse struct {
	RepositoryID  string            `json:"repository_id"`
	RevisionSHA   string            `json:"revision_sha"`
	TreeSHA       string            `json:"tree_sha"`
	PathPrefix    string            `json:"path_prefix,omitempty"`
	Entries       []sourceTreeEntry `json:"entries"`
	ReturnedCount int               `json:"returned_count"`
	Truncated     bool              `json:"truncated"`
	NextAfter     string            `json:"next_after,omitempty"`
	Coverage      sourceCoverage    `json:"coverage"`
	CollectedAt   time.Time         `json:"collected_at"`
}

type readFileRequest struct {
	RepositoryID string `json:"repository_id"`
	RevisionSHA  string `json:"revision_sha"`
	Path         string `json:"path"`
	ByteStart    int64  `json:"byte_start,omitempty"`
	ByteLimit    int64  `json:"byte_limit,omitempty"`
}

type readFileResponse struct {
	RepositoryID  string    `json:"repository_id"`
	RevisionSHA   string    `json:"revision_sha"`
	TreeSHA       string    `json:"tree_sha"`
	Path          string    `json:"path"`
	BlobSHA       string    `json:"blob_sha"`
	ContentSHA256 string    `json:"content_sha256"`
	ByteStart     int64     `json:"byte_start"`
	ByteEnd       int64     `json:"byte_end"`
	TotalBytes    int64     `json:"total_bytes"`
	Content       string    `json:"content"`
	Truncated     bool      `json:"truncated"`
	CollectedAt   time.Time `json:"collected_at"`
}

type searchSourceRequest struct {
	RepositoryID  string `json:"repository_id"`
	RevisionSHA   string `json:"revision_sha"`
	Query         string `json:"query"`
	PathPrefix    string `json:"path_prefix,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type sourceMatch struct {
	Path       string `json:"path"`
	BlobSHA    string `json:"blob_sha"`
	LineNumber int    `json:"line_number"`
	Line       string `json:"line"`
}

type searchSourceResponse struct {
	RepositoryID  string         `json:"repository_id"`
	RevisionSHA   string         `json:"revision_sha"`
	TreeSHA       string         `json:"tree_sha"`
	PathPrefix    string         `json:"path_prefix,omitempty"`
	Matches       []sourceMatch  `json:"matches"`
	ReturnedCount int            `json:"returned_count"`
	Truncated     bool           `json:"truncated"`
	Coverage      sourceCoverage `json:"coverage"`
	CollectedAt   time.Time      `json:"collected_at"`
}

type createSnapshotRequest struct {
	RepositoryID string `json:"repository_id"`
	RevisionSHA  string `json:"revision_sha"`
}

type snapshotMetadata struct {
	SchemaVersion       int       `json:"schema_version"`
	SnapshotID          string    `json:"snapshot_id"`
	RepositoryID        string    `json:"repository_id"`
	SourceProfileSHA256 string    `json:"source_profile_sha256"`
	AssessmentReady     bool      `json:"assessment_ready"`
	Repository          string    `json:"repository"`
	RevisionSHA         string    `json:"revision_sha"`
	TreeSHA             string    `json:"tree_sha"`
	ArchiveSHA256       string    `json:"archive_sha256"`
	ArchiveBytes        int64     `json:"archive_bytes"`
	UncompressedBytes   int64     `json:"uncompressed_bytes"`
	FileCount           int       `json:"file_count"`
	ExcludedEntries     int       `json:"excluded_entries"`
	Format              string    `json:"format"`
	CreatedAt           time.Time `json:"created_at"`
	ExpiresAt           time.Time `json:"expires_at"`
}

type snapshotResponse struct {
	snapshotMetadata
	Limitations []string `json:"limitations"`
}

type getSnapshotRequest struct {
	SnapshotID string `json:"snapshot_id"`
	ByteStart  int64  `json:"byte_start"`
	ByteLimit  int64  `json:"byte_limit"`
}

type getSnapshotResponse struct {
	SnapshotID          string `json:"snapshot_id"`
	RepositoryID        string `json:"repository_id"`
	SourceProfileSHA256 string `json:"source_profile_sha256"`
	AssessmentReady     bool   `json:"assessment_ready"`
	RevisionSHA         string `json:"revision_sha"`
	TreeSHA             string `json:"tree_sha"`
	ArchiveSHA256       string `json:"archive_sha256"`
	Format              string `json:"format"`
	ByteStart           int64  `json:"byte_start"`
	ByteEnd             int64  `json:"byte_end"`
	TotalBytes          int64  `json:"total_bytes"`
	ContentBase64       string `json:"content_base64"`
	Truncated           bool   `json:"truncated"`
}

type githubCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	} `json:"commit"`
}

type githubTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

type githubTree struct {
	SHA       string            `json:"sha"`
	Tree      []githubTreeEntry `json:"tree"`
	Truncated bool              `json:"truncated"`
}

type githubBlob struct {
	SHA      string `json:"sha"`
	Size     int64  `json:"size"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}
