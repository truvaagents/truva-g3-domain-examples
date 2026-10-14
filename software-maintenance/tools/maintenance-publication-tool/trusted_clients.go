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
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

type trustedCapabilityClient struct {
	workspaceEndpoint string
	reviewEndpoint    string
	workspaceToken    string
	reviewToken       string
	httpClient        *http.Client
}

func newTrustedCapabilityClient(workspaceURL, workspaceToken, reviewURL, reviewToken string) (*trustedCapabilityClient, error) {
	workspaceEndpoint, err := trustedCapabilityEndpoint(workspaceURL, "get_publication_bundle")
	if err != nil {
		return nil, errors.New("MAINTENANCE_WORKSPACE_TOOL_URL is invalid")
	}
	reviewEndpoint, err := trustedCapabilityEndpoint(reviewURL, "get_review_receipt")
	if err != nil {
		return nil, errors.New("MAINTENANCE_REVIEW_AGENT_URL is invalid")
	}
	if len(workspaceToken) < 32 || len(reviewToken) < 32 || workspaceToken == reviewToken {
		return nil, errors.New("trusted issuer credentials must be distinct and at least 32 characters")
	}
	client := telemetry.NewTracedHTTPClientWithTransport(&http.Transport{
		MaxIdleConns: 8, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 20 * time.Second, ForceAttemptHTTP2: true,
	})
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &trustedCapabilityClient{
		workspaceEndpoint: workspaceEndpoint, reviewEndpoint: reviewEndpoint,
		workspaceToken: workspaceToken, reviewToken: reviewToken, httpClient: client,
	}, nil
}

func trustedCapabilityEndpoint(baseURL, capability string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return "", errors.New("invalid trusted capability origin")
	}
	hostname := parsed.Hostname()
	localHTTP := parsed.Scheme == "http" && (hostname == "localhost" || hostname == "127.0.0.1" || strings.HasSuffix(hostname, ".svc.cluster.local"))
	if parsed.Scheme != "https" && !localHTTP {
		return "", errors.New("trusted capability origin must use HTTPS outside the cluster")
	}
	return parsed.String() + "/api/capabilities/" + capability, nil
}

func (client *trustedCapabilityClient) workspaceBundle(ctx context.Context, request publishRequest, timeout time.Duration) (workspaceBundle, error) {
	var result workspaceBundle
	err := client.invoke(ctx, client.workspaceEndpoint, client.workspaceToken, map[string]string{
		"case_id": request.CaseID, "candidate_id": request.CandidateID, "verification_id": request.VerificationID,
	}, timeout, 2<<20, &result)
	return result, err
}

func (client *trustedCapabilityClient) reviewReceipt(ctx context.Context, request publishRequest, timeout time.Duration) (reviewReceipt, error) {
	var result reviewReceipt
	err := client.invoke(ctx, client.reviewEndpoint, client.reviewToken, map[string]string{
		"case_id": request.CaseID, "review_id": request.ReviewID,
	}, timeout, 256<<10, &result)
	return result, err
}

func (client *trustedCapabilityClient) invoke(ctx context.Context, endpoint, token string, payload any, timeout time.Duration, maximumBytes int64, destination any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return failPublication("issuer_request", false, err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return failPublication("issuer_request", false, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return failPublication("issuer_unavailable", true, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if err != nil || int64(len(data)) > maximumBytes {
		return failPublication("issuer_response", false, err)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   *core.ToolError `json:"error"`
	}
	if err := decodePublicationStrictBytes(data, &envelope); err != nil {
		return failPublication("issuer_response", false, err)
	}
	if response.StatusCode != http.StatusOK || !envelope.Success {
		retryable := response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests
		return failPublication("issuer_rejected", retryable, nil)
	}
	if err := decodePublicationStrictBytes(envelope.Data, destination); err != nil {
		return failPublication("issuer_response", false, err)
	}
	return nil
}
