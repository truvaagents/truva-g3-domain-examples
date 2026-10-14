package serviceevidence

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/telemetry"
)

type upstreamFailureKind string

const (
	failureInvalidResponse upstreamFailureKind = "invalid_response"
	failureNotFound        upstreamFailureKind = "not_found"
	failureUnavailable     upstreamFailureKind = "unavailable"
	failureTimeout         upstreamFailureKind = "timeout"
	failureRateLimited     upstreamFailureKind = "rate_limited"
	failureConflict        upstreamFailureKind = "conflict"
)

type upstreamFailure struct {
	kind upstreamFailureKind
}

func (e *upstreamFailure) Error() string {
	return "upstream request failed: " + string(e.kind)
}

type evidenceClient interface {
	listObservations(context.Context, serviceProfile, time.Time, time.Time, int) (upstreamFeed, error)
	getEvidence(context.Context, serviceProfile, string) (upstreamReceipt, error)
	getOutcome(context.Context, serviceProfile, string) (upstreamOrder, error)
	getBuildInfo(context.Context, serviceProfile) (buildInfo, error)
	getCatalog(context.Context, serviceProfile) ([]product, error)
}

type serviceClient struct {
	httpClient *http.Client
}

func newServiceClient() *serviceClient {
	client := telemetry.NewTracedHTTPClientWithTransport(&http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	})
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &serviceClient{httpClient: client}
}

func (c *serviceClient) listObservations(ctx context.Context, profile serviceProfile, start, end time.Time, limit int) (upstreamFeed, error) {
	query := make(url.Values)
	query.Set("start", start.Format(time.RFC3339Nano))
	query.Set("end", end.Format(time.RFC3339Nano))
	query.Set("limit", fmt.Sprintf("%d", limit))
	var feed upstreamFeed
	err := c.get(ctx, profile, profile.Upstream.Endpoints.Observations, query, &feed)
	if err == nil {
		err = validateFeed(feed, start, end, limit)
	}
	return feed, err
}

func (c *serviceClient) getEvidence(ctx context.Context, profile serviceProfile, observationID string) (upstreamReceipt, error) {
	// The identifier grammar excludes slash, percent, query, and fragment
	// delimiters, so direct substitution is both safe and avoids double-escaping
	// an already escaped path segment through url.URL.String.
	endpoint := strings.Replace(profile.Upstream.Endpoints.Evidence, "{id}", observationID, 1)
	var receipt upstreamReceipt
	err := c.get(ctx, profile, endpoint, nil, &receipt)
	if err == nil {
		err = validateReceipt(receipt, observationID)
	}
	return receipt, err
}

func (c *serviceClient) getOutcome(ctx context.Context, profile serviceProfile, outcomeID string) (upstreamOrder, error) {
	endpoint := strings.Replace(profile.Upstream.Endpoints.BusinessOutcome, "{id}", outcomeID, 1)
	var order upstreamOrder
	err := c.get(ctx, profile, endpoint, nil, &order)
	if err == nil {
		err = validateOrder(order, outcomeID)
	}
	return order, err
}

func (c *serviceClient) getBuildInfo(ctx context.Context, profile serviceProfile) (buildInfo, error) {
	var info buildInfo
	err := c.get(ctx, profile, profile.Upstream.Endpoints.BuildInfo, nil, &info)
	if err == nil {
		err = validateBuildInfo(info, profile.Repository.Name)
	}
	return info, err
}

func (c *serviceClient) getCatalog(ctx context.Context, profile serviceProfile) ([]product, error) {
	var products []product
	err := c.get(ctx, profile, profile.Upstream.Endpoints.Catalog, nil, &products)
	if err == nil {
		err = validateProducts(products, profile.Limits.MaximumItems)
	}
	return products, err
}

func (c *serviceClient) get(ctx context.Context, profile serviceProfile, endpoint string, query url.Values, destination any) error {
	requestURL := *profile.parsedBaseURL
	requestURL.Path = endpoint
	requestURL.RawQuery = query.Encode()

	requestCtx, cancel := context.WithTimeout(ctx, profile.timeout())
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return &upstreamFailure{kind: failureTimeout}
		}
		return &upstreamFailure{kind: failureUnavailable}
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return classifyStatus(response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "" && !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, profile.Limits.MaximumResponseBytes+1))
	if err != nil {
		return &upstreamFailure{kind: failureUnavailable}
	}
	if int64(len(data)) > profile.Limits.MaximumResponseBytes {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	if err := decodeStrictBytes(data, destination); err != nil {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	return nil
}

func classifyStatus(status int) error {
	switch status {
	case http.StatusNotFound:
		return &upstreamFailure{kind: failureNotFound}
	case http.StatusConflict:
		return &upstreamFailure{kind: failureConflict}
	case http.StatusTooManyRequests:
		return &upstreamFailure{kind: failureRateLimited}
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return &upstreamFailure{kind: failureTimeout}
	default:
		if status >= 500 {
			return &upstreamFailure{kind: failureUnavailable}
		}
		return &upstreamFailure{kind: failureInvalidResponse}
	}
}

func validateFeed(feed upstreamFeed, requestedStart, requestedEnd time.Time, limit int) error {
	if !feed.Window.Start.Equal(requestedStart) || !feed.Window.End.Equal(requestedEnd) || len(feed.Candidates) > limit {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	for _, candidate := range feed.Candidates {
		if !identifierPattern.MatchString(candidate.ObservationID) || candidate.Operation != "create_order" || candidate.At.Before(feed.Window.Start) || candidate.At.After(feed.Window.End) || candidate.HTTPStatus < 100 || candidate.HTTPStatus > 599 {
			return &upstreamFailure{kind: failureInvalidResponse}
		}
	}
	return nil
}

var (
	sha256Pattern           = regexp.MustCompile(`^[a-f0-9]{64}$`)
	buildRevisionPattern    = regexp.MustCompile(`^build-[a-f0-9]{64}$`)
	frameworkVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	traceIDPattern          = regexp.MustCompile(`^[a-f0-9]{32}$`)
	currencyPattern         = regexp.MustCompile(`^[A-Z]{3}$`)
)

func validateReceipt(receipt upstreamReceipt, expectedID string) error {
	if receipt.ObservationID != expectedID || !identifierPattern.MatchString(receipt.ObservationID) || receipt.Operation != "create_order" || receipt.At.IsZero() || !buildRevisionPattern.MatchString(receipt.Revision) || receipt.HTTPStatus < 100 || receipt.HTTPStatus > 599 {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	if receipt.OrderID != "" && !identifierPattern.MatchString(receipt.OrderID) {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	if receipt.OrderRevision != "" && !buildRevisionPattern.MatchString(receipt.OrderRevision) {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	if receipt.TraceID != "" && !traceIDPattern.MatchString(receipt.TraceID) {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	if receipt.Currency != "" && !currencyPattern.MatchString(receipt.Currency) {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	if receipt.UnitPrice < 0 || receipt.Quantity < 0 || receipt.Discount < 0 || (receipt.LedgerTotal != nil && *receipt.LedgerTotal < 0) || (receipt.LedgerWrites != nil && *receipt.LedgerWrites < 0) {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	return nil
}

func validateOrder(order upstreamOrder, expectedID string) error {
	if order.Request.OrderID != expectedID || order.Receipt.OrderID != expectedID || !sha256Pattern.MatchString(order.RequestSHA256) || order.Receipt.HTTPStatus < 200 || order.Receipt.HTTPStatus >= 300 || order.Ledger.Total < 0 || order.Ledger.Writes < 1 {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	if err := validateReceipt(order.Receipt, order.Receipt.ObservationID); err != nil {
		return err
	}
	if order.Request.SKU == "" || order.Request.Quantity < 1 || order.Receipt.SKU != order.Request.SKU || order.Receipt.Quantity != order.Request.Quantity || order.Receipt.Currency == "" {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	return nil
}

func validateBuildInfo(info buildInfo, repository string) error {
	if info.SchemaVersion != 1 || info.Repository != repository || !buildRevisionPattern.MatchString(info.Revision) || !sha256Pattern.MatchString(info.SourceSHA256) || !gitRevisionPattern.MatchString(info.GitRevision) || !frameworkVersionPattern.MatchString(info.FrameworkVersion) {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	return nil
}

func validateProducts(products []product, maximumItems int) error {
	if len(products) == 0 || len(products) > maximumItems {
		return &upstreamFailure{kind: failureInvalidResponse}
	}
	seen := make(map[string]struct{}, len(products))
	for _, item := range products {
		if !identifierPattern.MatchString(item.SKU) || strings.TrimSpace(item.Name) == "" || item.UnitPrice < 0 || !currencyPattern.MatchString(item.Currency) || item.MaxOrderQuantity < 1 {
			return &upstreamFailure{kind: failureInvalidResponse}
		}
		if _, exists := seen[item.SKU]; exists {
			return &upstreamFailure{kind: failureInvalidResponse}
		}
		seen[item.SKU] = struct{}{}
	}
	return nil
}
