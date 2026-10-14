// Package lifecycle owns software-maintenance assessment records. It is an
// application store, not an orchestration driver or a framework memory cache.
package lifecycle

import (
	"fmt"
	"strconv"
	"time"
)

type Limits struct {
	PageLimit         int
	FactMaxBytes      int
	LookupMaxBytes    int
	OriginMaxBytes    int
	RevalidateAfter   time.Duration
	RemediationMaxAge time.Duration
	RecordingTimeout  time.Duration
	DueLimit          int
	TerminalRetention time.Duration
}

func ResolveLimits(lookup func(string) (string, bool)) (Limits, error) {
	limits := Limits{PageLimit: 20, FactMaxBytes: 32768, LookupMaxBytes: 65536, OriginMaxBytes: 1 << 20, RevalidateAfter: time.Hour, RemediationMaxAge: time.Minute, RecordingTimeout: 30 * time.Second, DueLimit: 10, TerminalRetention: 90 * 24 * time.Hour}
	for _, setting := range []struct {
		key      string
		target   *int
		min, max int
	}{
		{"MAINTENANCE_ASSESSMENT_PAGE_LIMIT", &limits.PageLimit, 1, 50},
		{"MAINTENANCE_ASSESSMENT_FACT_MAX_BYTES", &limits.FactMaxBytes, 1024, 131072},
		{"MAINTENANCE_ASSESSMENT_LOOKUP_MAX_BYTES", &limits.LookupMaxBytes, 4096, 1048576},
		{"MAINTENANCE_ASSESSMENT_ORIGIN_MAX_BYTES", &limits.OriginMaxBytes, 4096, 8 << 20},
		{"MAINTENANCE_LIFECYCLE_DUE_LIMIT", &limits.DueLimit, 1, 50},
	} {
		if raw, present := lookup(setting.key); present {
			n, err := strconv.Atoi(raw)
			if err != nil || n < setting.min || n > setting.max {
				return Limits{}, fmt.Errorf("%s must be an integer in [%d, %d]", setting.key, setting.min, setting.max)
			}
			*setting.target = n
		}
	}
	for _, setting := range []struct {
		key      string
		target   *time.Duration
		min, max time.Duration
	}{
		{"MAINTENANCE_ASSESSMENT_REVALIDATE_AFTER", &limits.RevalidateAfter, time.Minute, 24 * time.Hour},
		{"MAINTENANCE_REMEDIATION_STATE_MAX_AGE", &limits.RemediationMaxAge, time.Second, 5 * time.Minute},
		{"MAINTENANCE_ASSESSMENT_RECORD_TIMEOUT", &limits.RecordingTimeout, 5 * time.Second, 2 * time.Minute},
		{"MAINTENANCE_LIFECYCLE_TERMINAL_RETENTION", &limits.TerminalRetention, 24 * time.Hour, 365 * 24 * time.Hour},
	} {
		if raw, present := lookup(setting.key); present {
			d, err := time.ParseDuration(raw)
			if err != nil || d < setting.min || d > setting.max {
				return Limits{}, fmt.Errorf("%s requires a Go duration in [%s, %s]", setting.key, setting.min, setting.max)
			}
			*setting.target = d
		}
	}
	return limits, nil
}
