package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

type sourceSnapshotClient struct {
	endpoint   string
	token      string
	httpClient *http.Client
}

var errSourceAssessmentNotReady = errors.New("source profile is not ready for assessment")

func newSourceSnapshotClient(baseURL, token string) (*sourceSnapshotClient, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return nil, errors.New("MAINTENANCE_SOURCE_TOOL_URL must be a service origin")
	}
	hostname := parsed.Hostname()
	localHTTP := parsed.Scheme == "http" && (hostname == "localhost" || hostname == "127.0.0.1" || strings.HasSuffix(hostname, ".svc.cluster.local"))
	if parsed.Scheme != "https" && !localHTTP {
		return nil, errors.New("source tool URL must use HTTPS except for local or cluster service DNS")
	}
	if len(token) < 32 {
		return nil, errors.New("MAINTENANCE_SOURCE_WORKSPACE_TOKEN is required")
	}
	client := telemetry.NewTracedHTTPClientWithTransport(&http.Transport{
		MaxIdleConns: 8, MaxIdleConnsPerHost: 4,
		IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second, ForceAttemptHTTP2: true,
	})
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &sourceSnapshotClient{endpoint: parsed.String() + "/api/capabilities/get_source_snapshot", token: token, httpClient: client}, nil
}

func (client *sourceSnapshotClient) download(ctx context.Context, snapshotID string, maximumBytes int64) ([]byte, sourceSnapshotChunk, error) {
	const chunkBytes int64 = 256 << 10
	var archive bytes.Buffer
	var identity sourceSnapshotChunk
	for start := int64(0); ; {
		payload, err := json.Marshal(map[string]any{"snapshot_id": snapshotID, "byte_start": start, "byte_limit": chunkBytes})
		if err != nil {
			return nil, identity, errors.New("encode source snapshot request")
		}
		requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, client.endpoint, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return nil, identity, errors.New("create source snapshot request")
		}
		request.Header.Set("Authorization", "Bearer "+client.token)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.httpClient.Do(request)
		if err != nil {
			cancel()
			return nil, identity, fmt.Errorf("source snapshot request failed: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 2*chunkBytes+(16<<10)))
		_ = response.Body.Close()
		cancel()
		if readErr != nil {
			return nil, identity, errors.New("read source snapshot response")
		}
		var envelope struct {
			Success bool                `json:"success"`
			Data    sourceSnapshotChunk `json:"data"`
			Error   *core.ToolError     `json:"error"`
		}
		if err := decodeWorkspaceStrictBytes(data, &envelope); err != nil || response.StatusCode != http.StatusOK || !envelope.Success {
			return nil, identity, errors.New("source tool rejected snapshot retrieval")
		}
		chunk := envelope.Data
		if chunk.SnapshotID != snapshotID || !workspaceNamePattern.MatchString(chunk.RepositoryID) || !sha256Pattern.MatchString(chunk.SourceProfileSHA256) || !gitSHAPattern.MatchString(chunk.RevisionSHA) || !gitSHAPattern.MatchString(chunk.TreeSHA) || !sha256Pattern.MatchString(chunk.ArchiveSHA256) || chunk.Format != "application/gzip" || chunk.ByteStart != start || chunk.ByteEnd < chunk.ByteStart || chunk.TotalBytes <= 0 || chunk.TotalBytes > maximumBytes {
			return nil, identity, errors.New("source tool returned invalid snapshot metadata")
		}
		if !chunk.AssessmentReady {
			return nil, identity, errSourceAssessmentNotReady
		}
		if start == 0 {
			identity = chunk
		} else if chunk.RepositoryID != identity.RepositoryID || chunk.SourceProfileSHA256 != identity.SourceProfileSHA256 || chunk.AssessmentReady != identity.AssessmentReady || chunk.RevisionSHA != identity.RevisionSHA || chunk.TreeSHA != identity.TreeSHA || chunk.ArchiveSHA256 != identity.ArchiveSHA256 || chunk.TotalBytes != identity.TotalBytes || chunk.Format != identity.Format {
			return nil, identity, errors.New("source snapshot identity changed while downloading")
		}
		decoded, err := base64.StdEncoding.DecodeString(chunk.ContentBase64)
		if err != nil || int64(len(decoded)) != chunk.ByteEnd-chunk.ByteStart || int64(archive.Len()+len(decoded)) > maximumBytes {
			return nil, identity, errors.New("source tool returned invalid snapshot content")
		}
		_, _ = archive.Write(decoded)
		if !chunk.Truncated {
			if chunk.ByteEnd != chunk.TotalBytes || int64(archive.Len()) != chunk.TotalBytes {
				return nil, identity, errors.New("source snapshot terminated at an invalid boundary")
			}
			break
		}
		if len(decoded) == 0 || chunk.ByteEnd >= chunk.TotalBytes {
			return nil, identity, errors.New("source snapshot pagination did not advance")
		}
		start = chunk.ByteEnd
	}
	digest := sha256.Sum256(archive.Bytes())
	if hex.EncodeToString(digest[:]) != identity.ArchiveSHA256 || snapshotID != "src_"+identity.ArchiveSHA256 {
		return nil, identity, errors.New("source snapshot digest verification failed")
	}
	return archive.Bytes(), identity, nil
}
