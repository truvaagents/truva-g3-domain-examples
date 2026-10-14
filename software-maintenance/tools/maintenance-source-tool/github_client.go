package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/telemetry"
)

const githubAPIVersion = "2022-11-28"

type sourceFailureKind string

const (
	sourceFailureInvalid     sourceFailureKind = "invalid_response"
	sourceFailureNotFound    sourceFailureKind = "not_found"
	sourceFailureUnavailable sourceFailureKind = "unavailable"
	sourceFailureTimeout     sourceFailureKind = "timeout"
	sourceFailureRateLimit   sourceFailureKind = "rate_limit"
	sourceFailureAuth        sourceFailureKind = "auth"
)

type sourceFailure struct {
	kind sourceFailureKind
}

func (failure *sourceFailure) Error() string {
	return "source provider request failed: " + string(failure.kind)
}

type githubSourceClient struct {
	httpClient *http.Client
	baseURL    *url.URL
	token      string
}

func newGitHubSourceClient(baseURL, token string) (*githubSourceClient, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("GITHUB_API_BASE_URL must be an HTTPS API origin")
	}
	if len(token) < 20 {
		return nil, errors.New("GITHUB_TOKEN is required and appears invalid")
	}
	client := telemetry.NewTracedHTTPClientWithTransport(&http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	})
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &githubSourceClient{httpClient: client, baseURL: parsed, token: token}, nil
}

func (client *githubSourceClient) resolveCommit(ctx context.Context, profile sourceProfile, revision string) (githubCommit, error) {
	var commit githubCommit
	endpoint := client.repositoryEndpoint(profile, "commits", url.PathEscape(revision))
	err := client.getJSON(ctx, profile, endpoint, nil, profile.Limits.MaximumAPIResponseBytes, &commit)
	if err == nil && (!gitSHAPattern.MatchString(commit.SHA) || !gitSHAPattern.MatchString(commit.Commit.Tree.SHA)) {
		err = &sourceFailure{kind: sourceFailureInvalid}
	}
	return commit, err
}

func (client *githubSourceClient) getTree(ctx context.Context, profile sourceProfile, treeSHA string) (githubTree, error) {
	var tree githubTree
	query := make(url.Values)
	query.Set("recursive", "1")
	endpoint := client.repositoryEndpoint(profile, "git", "trees", treeSHA)
	err := client.getJSON(ctx, profile, endpoint, query, profile.Limits.MaximumAPIResponseBytes, &tree)
	if err != nil {
		return tree, err
	}
	if tree.SHA != treeSHA || !gitSHAPattern.MatchString(tree.SHA) || len(tree.Tree) > profile.Limits.MaximumTreeEntries {
		return tree, &sourceFailure{kind: sourceFailureInvalid}
	}
	seen := make(map[string]struct{}, len(tree.Tree))
	for _, entry := range tree.Tree {
		if _, err := normalizeSourcePath(entry.Path, false); err != nil || !gitSHAPattern.MatchString(entry.SHA) || entry.Size < 0 {
			return tree, &sourceFailure{kind: sourceFailureInvalid}
		}
		switch entry.Type {
		case "blob", "tree", "commit":
		default:
			return tree, &sourceFailure{kind: sourceFailureInvalid}
		}
		if _, exists := seen[entry.Path]; exists {
			return tree, &sourceFailure{kind: sourceFailureInvalid}
		}
		seen[entry.Path] = struct{}{}
	}
	return tree, nil
}

func (client *githubSourceClient) getBlob(ctx context.Context, profile sourceProfile, blobSHA string) ([]byte, error) {
	var blob githubBlob
	endpoint := client.repositoryEndpoint(profile, "git", "blobs", blobSHA)
	maximumJSONBytes := profile.Limits.MaximumFileBytes*2 + 4096
	if maximumJSONBytes > profile.Limits.MaximumAPIResponseBytes {
		maximumJSONBytes = profile.Limits.MaximumAPIResponseBytes
	}
	if err := client.getJSON(ctx, profile, endpoint, nil, maximumJSONBytes, &blob); err != nil {
		return nil, err
	}
	if blob.SHA != blobSHA || blob.Encoding != "base64" || blob.Size < 0 || blob.Size > profile.Limits.MaximumFileBytes {
		return nil, &sourceFailure{kind: sourceFailureInvalid}
	}
	encoded := strings.ReplaceAll(blob.Content, "\n", "")
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || int64(len(data)) != blob.Size {
		return nil, &sourceFailure{kind: sourceFailureInvalid}
	}
	return data, nil
}

func (client *githubSourceClient) getJSON(ctx context.Context, profile sourceProfile, endpoint string, query url.Values, maximumBytes int64, destination any) error {
	requestURL := strings.TrimRight(client.baseURL.String(), "/") + endpoint
	if encodedQuery := query.Encode(); encodedQuery != "" {
		requestURL += "?" + encodedQuery
	}
	requestCtx, cancel := context.WithTimeout(ctx, profile.timeout())
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		return &sourceFailure{kind: sourceFailureInvalid}
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("User-Agent", "TruvaG3-Maintenance-Source-Tool")
	response, err := client.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return &sourceFailure{kind: sourceFailureTimeout}
		}
		return &sourceFailure{kind: sourceFailureUnavailable}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return classifySourceStatus(response)
	}
	if contentType := strings.ToLower(response.Header.Get("Content-Type")); contentType != "" && !strings.Contains(contentType, "json") {
		return &sourceFailure{kind: sourceFailureInvalid}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if err != nil {
		return &sourceFailure{kind: sourceFailureUnavailable}
	}
	if int64(len(data)) > maximumBytes {
		return &sourceFailure{kind: sourceFailureInvalid}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return &sourceFailure{kind: sourceFailureInvalid}
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return &sourceFailure{kind: sourceFailureInvalid}
	}
	return nil
}

func (client *githubSourceClient) repositoryEndpoint(profile sourceProfile, parts ...string) string {
	segments := []string{"repos", url.PathEscape(profile.Owner), url.PathEscape(profile.Repository)}
	segments = append(segments, parts...)
	return "/" + strings.Join(segments, "/")
}

func classifySourceStatus(response *http.Response) error {
	switch response.StatusCode {
	case http.StatusNotFound, http.StatusUnprocessableEntity:
		return &sourceFailure{kind: sourceFailureNotFound}
	case http.StatusUnauthorized:
		return &sourceFailure{kind: sourceFailureAuth}
	case http.StatusForbidden:
		if response.Header.Get("X-RateLimit-Remaining") == "0" {
			return &sourceFailure{kind: sourceFailureRateLimit}
		}
		return &sourceFailure{kind: sourceFailureAuth}
	case http.StatusTooManyRequests:
		return &sourceFailure{kind: sourceFailureRateLimit}
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return &sourceFailure{kind: sourceFailureTimeout}
	default:
		if response.StatusCode >= 500 {
			return &sourceFailure{kind: sourceFailureUnavailable}
		}
		return &sourceFailure{kind: sourceFailureInvalid}
	}
}

func exactCommit(ctx context.Context, client *githubSourceClient, profile sourceProfile, revisionSHA string) (githubCommit, error) {
	if !gitSHAPattern.MatchString(revisionSHA) {
		return githubCommit{}, errors.New("revision SHA is invalid")
	}
	commit, err := client.resolveCommit(ctx, profile, revisionSHA)
	if err != nil {
		return githubCommit{}, err
	}
	if commit.SHA != revisionSHA {
		return githubCommit{}, fmt.Errorf("resolved revision does not match requested commit")
	}
	return commit, nil
}
