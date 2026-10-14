package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/truvaagents/truva-g3/telemetry"
)

const githubAPIVersion = "2022-11-28"

type githubPublicationClient struct {
	baseURL              *url.URL
	token                string
	httpClient           *http.Client
	readLimitMu          sync.Mutex
	readLimit            *githubReadRateLimit
	errorMessageMaxBytes int
}

func newGitHubPublicationClient(baseURL, token string) (*githubPublicationClient, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("GITHUB_API_BASE_URL must be an HTTPS API origin")
	}
	if len(token) < 20 {
		return nil, errors.New("GITHUB_WRITE_TOKEN is required and appears invalid")
	}
	client := telemetry.NewTracedHTTPClientWithTransport(&http.Transport{
		Proxy: http.ProxyFromEnvironment, MaxIdleConns: 16, MaxIdleConnsPerHost: 8,
		IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second, ExpectContinueTimeout: time.Second, ForceAttemptHTTP2: true,
	})
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &githubPublicationClient{baseURL: parsed, token: token, httpClient: client}, nil
}

func (client *githubPublicationClient) getCommit(ctx context.Context, profile publicationProfile, sha string) (githubCommit, error) {
	var commit githubCommit
	status, err := client.request(ctx, profile, http.MethodGet, client.repositoryEndpoint(profile, "commits", sha), nil, nil, &commit)
	if err != nil {
		return commit, err
	}
	if status != http.StatusOK || commit.SHA != sha || !gitSHAPattern.MatchString(commit.Commit.Tree.SHA) {
		return commit, failPublication("github_base_mismatch", false, nil)
	}
	return commit, nil
}

func (client *githubPublicationClient) getBranchHead(ctx context.Context, profile publicationProfile, branch string) (string, error) {
	var response struct {
		Ref    string       `json:"ref"`
		Object githubObject `json:"object"`
	}
	status, err := client.request(ctx, profile, http.MethodGet, client.repositoryEndpoint(profile, "git", "ref", url.PathEscape("heads/"+branch)), nil, nil, &response)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK || response.Ref != "refs/heads/"+branch || !gitSHAPattern.MatchString(response.Object.SHA) {
		return "", failPublication("github_base_mismatch", false, nil)
	}
	return response.Object.SHA, nil
}

func (client *githubPublicationClient) createBlob(ctx context.Context, profile publicationProfile, contentBase64 string) (string, error) {
	var object githubObject
	status, err := client.request(ctx, profile, http.MethodPost, client.repositoryEndpoint(profile, "git", "blobs"), nil, map[string]string{"content": contentBase64, "encoding": "base64"}, &object)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated || !gitSHAPattern.MatchString(object.SHA) {
		return "", failPublication("github_blob_rejected", status >= 500, nil)
	}
	return object.SHA, nil
}

func (client *githubPublicationClient) createTree(ctx context.Context, profile publicationProfile, baseTree string, entries []githubTreeEntry) (string, error) {
	var object githubObject
	status, err := client.request(ctx, profile, http.MethodPost, client.repositoryEndpoint(profile, "git", "trees"), nil, map[string]any{"base_tree": baseTree, "tree": entries}, &object)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated || !gitSHAPattern.MatchString(object.SHA) {
		return "", failPublication("github_tree_rejected", status >= 500, nil)
	}
	return object.SHA, nil
}

func (client *githubPublicationClient) createCommit(ctx context.Context, profile publicationProfile, message, treeSHA, parentSHA string, timestamp time.Time) (string, error) {
	identity := map[string]string{"name": profile.CommitAuthorName, "email": profile.CommitAuthorEmail, "date": timestamp.UTC().Format(time.RFC3339)}
	payload := map[string]any{"message": message, "tree": treeSHA, "parents": []string{parentSHA}, "author": identity, "committer": identity}
	var object githubObject
	status, err := client.request(ctx, profile, http.MethodPost, client.repositoryEndpoint(profile, "git", "commits"), nil, payload, &object)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated || !gitSHAPattern.MatchString(object.SHA) {
		return "", failPublication("github_commit_rejected", status >= 500, nil)
	}
	return object.SHA, nil
}

func (client *githubPublicationClient) ensureBranch(ctx context.Context, profile publicationProfile, branch, commitSHA string) error {
	endpoint := client.repositoryEndpoint(profile, "git", "refs")
	createStatus, createErr := client.request(ctx, profile, http.MethodPost, endpoint, nil, map[string]string{"ref": "refs/heads/" + branch, "sha": commitSHA}, nil)
	if createErr == nil && createStatus == http.StatusCreated {
		return nil
	}
	var response struct {
		Ref    string       `json:"ref"`
		Object githubObject `json:"object"`
	}
	getStatus, getErr := client.request(ctx, profile, http.MethodGet, client.repositoryEndpoint(profile, "git", "ref", url.PathEscape("heads/"+branch)), nil, nil, &response)
	if getErr == nil && getStatus == http.StatusOK && response.Ref == "refs/heads/"+branch && response.Object.SHA == commitSHA {
		return nil
	}
	if createErr != nil && createStatus != http.StatusUnprocessableEntity && createStatus != http.StatusConflict {
		return createErr
	}
	return failPublication("github_branch_conflict", false, getErr)
}

func (client *githubPublicationClient) ensureDraftPullRequest(ctx context.Context, profile publicationProfile, branch, baseSHA, commitSHA, title, body string) (githubPullRequest, error) {
	existing, err := client.findPullRequests(ctx, profile, branch)
	if err != nil {
		return githubPullRequest{}, err
	}
	if len(existing) > 0 {
		return validateDraftPullRequest(existing[0], profile, branch, baseSHA, commitSHA)
	}
	payload := map[string]any{"title": title, "body": body, "head": branch, "base": profile.BaseRef, "draft": true}
	var created githubPullRequest
	status, createErr := client.request(ctx, profile, http.MethodPost, client.repositoryEndpoint(profile, "pulls"), nil, payload, &created)
	if createErr == nil && status == http.StatusCreated {
		return validateDraftPullRequest(created, profile, branch, baseSHA, commitSHA)
	}
	existing, err = client.findPullRequests(ctx, profile, branch)
	if err == nil && len(existing) == 1 {
		return validateDraftPullRequest(existing[0], profile, branch, baseSHA, commitSHA)
	}
	if createErr != nil && status != http.StatusUnprocessableEntity && status != http.StatusConflict {
		return githubPullRequest{}, createErr
	}
	ambiguous := &publicationFailure{kind: "github_pr_ambiguous", retryable: true, cause: err}
	var rejected *publicationFailure
	if errors.As(createErr, &rejected) {
		// A successful reconciliation remains authoritative. Otherwise preserve
		// the create response, including 409/422, without changing retry policy.
		ambiguous.github = rejected.github
	}
	return githubPullRequest{}, ambiguous
}

func (client *githubPublicationClient) findPullRequests(ctx context.Context, profile publicationProfile, branch string) ([]githubPullRequest, error) {
	query := make(url.Values)
	query.Set("state", "open")
	query.Set("head", profile.Owner+":"+branch)
	query.Set("base", profile.BaseRef)
	query.Set("per_page", "2")
	var pulls []githubPullRequest
	status, err := client.request(ctx, profile, http.MethodGet, client.repositoryEndpoint(profile, "pulls"), query, nil, &pulls)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || len(pulls) > 1 {
		return nil, failPublication("github_pr_ambiguous", false, nil)
	}
	return pulls, nil
}

func validateDraftPullRequest(pull githubPullRequest, profile publicationProfile, branch, baseSHA, commitSHA string) (githubPullRequest, error) {
	parsed, err := url.Parse(pull.HTMLURL)
	expectedRepository := profile.Owner + "/" + profile.Repository
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || pull.ID <= 0 || pull.Number <= 0 || !pull.Draft || pull.State != "open" || pull.Base.Ref != profile.BaseRef || pull.Base.SHA != baseSHA || pull.Base.Repo.FullName != expectedRepository || pull.Head.Ref != branch || pull.Head.SHA != commitSHA || pull.Head.Repo.FullName != expectedRepository {
		return githubPullRequest{}, failPublication("github_pr_invalid", false, nil)
	}
	return pull, nil
}

func (client *githubPublicationClient) request(ctx context.Context, profile publicationProfile, method, endpoint string, query url.Values, payload any, destination any) (int, error) {
	return client.requestResponse(ctx, profile, method, endpoint, query, payload, destination, nil, false)
}

func (client *githubPublicationClient) requestWithETag(ctx context.Context, profile publicationProfile, method, endpoint string, query url.Values, payload any, destination any, etag *string) (int, error) {
	// Observation reads honor provider backoff. Existing publication operations
	// retain their original request/reconciliation policy through request().
	return client.requestResponse(ctx, profile, method, endpoint, query, payload, destination, etag, true)
}

func (client *githubPublicationClient) requestResponse(ctx context.Context, profile publicationProfile, method, endpoint string, query url.Values, payload any, destination any, etag *string, observeRateLimit bool) (int, error) {
	if observeRateLimit {
		if err := ctx.Err(); err != nil {
			return 0, failPublication("github_unavailable", true, err)
		}
		if limited := client.pendingReadRateLimit(time.Now()); limited != nil {
			return limited.upstreamStatus, limited
		}
	}
	requestURL := strings.TrimRight(client.baseURL.String(), "/") + endpoint
	if encodedQuery := query.Encode(); encodedQuery != "" {
		requestURL += "?" + encodedQuery
	}
	var reader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, failPublication("github_request", false, err)
		}
		reader = bytes.NewReader(encoded)
	}
	requestCtx, cancel := context.WithTimeout(ctx, profile.timeout())
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, requestURL, reader)
	if err != nil {
		return 0, failPublication("github_request", false, err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("User-Agent", "TruvaG3-Maintenance-Publication-Tool")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return 0, failPublication("github_unavailable", true, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if observeRateLimit && (response.StatusCode < 200 || response.StatusCode >= 300) {
		// Headers can establish rate limiting even when the provider's body is
		// unreadable or oversized. An invalid body is not interpreted as evidence.
		errorBody := data
		if readErr != nil || len(data) > 2<<20 {
			errorBody = nil
		}
		if limited := githubReadRateLimitFromResponse(response.StatusCode, response.Header, errorBody, time.Now()); limited != nil {
			client.rememberReadRateLimit(limited)
			return response.StatusCode, limited
		}
	}
	if readErr != nil && observeRateLimit {
		return response.StatusCode, failPublication("github_read_unavailable", true, readErr)
	}
	if readErr != nil || len(data) > 2<<20 {
		return response.StatusCode, failPublication("github_response", false, readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return response.StatusCode, &publicationFailure{
			kind: "github_rejected", retryable: retryable,
			github: client.failureEvidence(method, endpoint, response, data),
		}
	}
	if destination == nil {
		return response.StatusCode, nil
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return response.StatusCode, failPublication("github_response", false, err)
	}
	if etag != nil && len(response.Header.Get("ETag")) <= 512 {
		*etag = response.Header.Get("ETag")
	}
	return response.StatusCode, nil
}

func (client *githubPublicationClient) repositoryEndpoint(profile publicationProfile, parts ...string) string {
	segments := []string{"repos", url.PathEscape(profile.Owner), url.PathEscape(profile.Repository)}
	segments = append(segments, parts...)
	return "/" + strings.Join(segments, "/")
}
