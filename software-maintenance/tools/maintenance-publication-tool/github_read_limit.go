package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// This is provider backoff evidence, not a cached PR observation. Only the new
// observation-read path consumes it; publication mutation policy is unchanged.
type githubReadRateLimit struct {
	upstreamStatus int
	notBefore      time.Time
}

func (limited *githubReadRateLimit) Error() string { return "GitHub observation read is rate limited" }

func githubReadRateLimitFromResponse(status int, headers http.Header, body []byte, now time.Time) *githubReadRateLimit {
	if status != http.StatusForbidden && status != http.StatusTooManyRequests {
		return nil
	}
	remaining := strings.TrimSpace(headers.Get("X-RateLimit-Remaining"))
	retryAfter := strings.TrimSpace(headers.Get("Retry-After"))
	var message struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &message)
	text := strings.ToLower(message.Message)
	rateMessage := strings.Contains(text, "api rate limit exceeded") || strings.Contains(text, "secondary rate limit")
	// A 403 needs evidence distinguishing it from repository access denial.
	if status == http.StatusForbidden && remaining != "0" && retryAfter == "" && !rateMessage {
		return nil
	}
	notBefore := now.Add(time.Minute) // GitHub's documented secondary-limit fallback.
	if delay, valid := githubRetryAfterDelay(retryAfter); valid {
		notBefore = now.Add(delay)
	} else if deadline, err := http.ParseTime(retryAfter); err == nil && deadline.After(now) {
		notBefore = deadline
	} else if remaining == "0" {
		if reset, err := strconv.ParseInt(strings.TrimSpace(headers.Get("X-RateLimit-Reset")), 10, 64); err == nil {
			deadline := time.Unix(reset, 0).UTC()
			if deadline.After(now) && deadline.Year() <= 9999 {
				notBefore = deadline
			}
		}
	}
	return &githubReadRateLimit{upstreamStatus: status, notBefore: notBefore.UTC()}
}

// Retry-After delay-seconds is an unsigned decimal integer, not a Go duration.
// Bound multiplication by time.Second so malformed/overflowing values fall back.
func githubRetryAfterDelay(value string) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	seconds, err := strconv.ParseUint(value, 10, 64)
	const maximumSeconds = uint64((1<<63 - 1) / int64(time.Second))
	if err != nil || seconds > maximumSeconds {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

func (client *githubPublicationClient) pendingReadRateLimit(now time.Time) *githubReadRateLimit {
	client.readLimitMu.Lock()
	defer client.readLimitMu.Unlock()
	if client.readLimit == nil || !now.Before(client.readLimit.notBefore) {
		client.readLimit = nil
		return nil
	}
	copy := *client.readLimit
	return &copy
}

func (client *githubPublicationClient) rememberReadRateLimit(limited *githubReadRateLimit) {
	client.readLimitMu.Lock()
	defer client.readLimitMu.Unlock()
	if client.readLimit == nil || limited.notBefore.After(client.readLimit.notBefore) {
		copy := *limited
		client.readLimit = &copy
	}
}

func (limited *githubReadRateLimit) details(now time.Time) map[string]string {
	seconds := limited.notBefore.Unix() - now.Unix()
	if limited.notBefore.Nanosecond() > now.Nanosecond() {
		seconds++
	}
	if seconds < 0 {
		seconds = 0
	}
	return map[string]string{
		"retry_after":      strconv.FormatInt(seconds, 10),
		"retry_not_before": limited.notBefore.Format(time.RFC3339Nano),
		"upstream_status":  strconv.Itoa(limited.upstreamStatus),
		"hint":             "Retry this exact reference after retry_not_before; current PR state is unknown.",
	}
}
