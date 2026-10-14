package serviceevidence

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestServiceClientBoundsValidatesAndPropagatesContext(t *testing.T) {
	t.Parallel()
	type contextKey string
	const key contextKey = "test"
	client := &serviceClient{httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Context().Value(key) != "propagated" {
			t.Error("request context value was not propagated")
		}
		return jsonResponse(http.StatusOK, `{"schema_version":1,"repository":"truvaagents/retail-order-service","revision":"build-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","git_revision":"cccccccccccccccccccccccccccccccccccccccc","source_matches_commit":true,"framework_version":"v0.5.0"}`), nil
	})}}
	profile := validTestProfile()
	if err := validateProfile(&profile); err != nil {
		t.Fatalf("validateProfile() error = %v", err)
	}
	ctx := context.WithValue(context.Background(), key, "propagated")
	info, err := client.getBuildInfo(ctx, profile)
	if err != nil {
		t.Fatalf("getBuildInfo() error = %v", err)
	}
	if info.Revision != "build-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("revision = %q", info.Revision)
	}
}

func TestServiceClientClassifiesTimeoutAndOversizedResponse(t *testing.T) {
	t.Parallel()
	t.Run("timeout", func(t *testing.T) {
		profile := validTestProfile()
		profile.Limits.TimeoutMilliseconds = 100
		if err := validateProfile(&profile); err != nil {
			t.Fatalf("validateProfile() error = %v", err)
		}
		client := &serviceClient{httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})}}
		_, err := client.getBuildInfo(context.Background(), profile)
		assertFailureKind(t, err, failureTimeout)
	})

	t.Run("oversized", func(t *testing.T) {
		profile := validTestProfile()
		profile.Limits.MaximumResponseBytes = 1024
		if err := validateProfile(&profile); err != nil {
			t.Fatalf("validateProfile() error = %v", err)
		}
		client := &serviceClient{httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, strings.Repeat("x", 1025)), nil
		})}}
		_, err := client.getBuildInfo(context.Background(), profile)
		assertFailureKind(t, err, failureInvalidResponse)
	})
}

func TestServiceClientPropagatesCancellation(t *testing.T) {
	t.Parallel()
	profile := validTestProfile()
	if err := validateProfile(&profile); err != nil {
		t.Fatalf("validateProfile() error = %v", err)
	}
	client := &serviceClient{httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.getBuildInfo(ctx, profile)
	var failure *upstreamFailure
	if !errors.As(err, &failure) || (failure.kind != failureUnavailable && failure.kind != failureTimeout) {
		t.Fatalf("getBuildInfo() error = %v", err)
	}
}

func TestServiceClientClassifiesSanitizedHTTPFailures(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		status int
		kind   upstreamFailureKind
	}{
		"not found":    {status: http.StatusNotFound, kind: failureNotFound},
		"rate limited": {status: http.StatusTooManyRequests, kind: failureRateLimited},
		"unavailable":  {status: http.StatusServiceUnavailable, kind: failureUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			profile := validTestProfile()
			if err := validateProfile(&profile); err != nil {
				t.Fatalf("validateProfile() error = %v", err)
			}
			client := &serviceClient{httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(test.status, `{"private_backend_detail":"must not escape"}`), nil
			})}}
			_, err := client.getBuildInfo(context.Background(), profile)
			assertFailureKind(t, err, test.kind)
			if strings.Contains(err.Error(), "private_backend_detail") {
				t.Fatal("upstream response body leaked through the error")
			}
		})
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func assertFailureKind(t *testing.T, err error, want upstreamFailureKind) {
	t.Helper()
	var failure *upstreamFailure
	if !errors.As(err, &failure) || failure.kind != want {
		t.Fatalf("failure = %v, want %q", err, want)
	}
}
