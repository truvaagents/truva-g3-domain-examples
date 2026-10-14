// Command config-preflight validates the example's memory and selector settings
// before deployment. It uses only the standard library, so setup does not need a
// framework checkout/module download, Redis, Kubernetes, or provider credentials.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"
)

func main() {
	if err := validate(os.LookupEnv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Keep ranges aligned with core memory config, orchestration hook options and
// memory backend options. This is a deployment preflight, not a substitute for
// those constructors. Absent values are left absent: owning code supplies defaults.
func validate(lookup func(string) (string, bool)) error {
	if value, present := lookup("MAINTENANCE_ADMISSION_ENABLED"); present {
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("MAINTENANCE_ADMISSION_ENABLED requires a boolean")
		}
	}
	if value, present := lookup("MAINTENANCE_ADMISSION_RUN_ID"); present && !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`).MatchString(value) {
		return fmt.Errorf("MAINTENANCE_ADMISSION_RUN_ID requires a bounded lowercase slug")
	}
	for _, setting := range []struct {
		key      string
		min, max int64
	}{
		{"TRUVAG3_SHARED_MEMORY_ENRICHMENT_MAX_TOKENS", 1, int64(int(^uint(0)>>1) / 4)},
		{"TRUVAG3_SHARED_MEMORY_RECENT_EVENTS_LIMIT", 1, int64(int(^uint(0) >> 1))},
		{"TRUVAG3_SHARED_MEMORY_ENRICHMENT_SUMMARY_MAX_TOKENS", 1, int64(int(^uint(0) >> 1))},
		{"TRUVAG3_SHARED_MEMORY_COMPACTION_RAW_LIMIT", 1, int64(int(^uint(0) >> 1))},
		{"TRUVAG3_SHARED_MEMORY_COMPACTION_RECENT_DETAIL", 0, int64(int(^uint(0) >> 1))},
		{"TRUVAG3_SHARED_MEMORY_DIGEST_INCREMENTAL_THRESHOLD", 1, int64(int(^uint(0) >> 1))},
		{"TRUVAG3_SHARED_MEMORY_EVENT_SUMMARIZER_MAX_TOKENS", 1, int64(int(^uint(0) >> 1))},
		{"TRUVAG3_SHARED_MEMORY_EVENT_SUMMARIZER_MAX_RESPONSE_BYTES", 1, int64(int(^uint(0) >> 1))},
		{"TRUVAG3_SHARED_MEMORY_RECORD_FAILURE_CAUSE_MAX_BYTES", 1, int64(int(^uint(0) >> 1))},
		{"TRUVAG3_ACTIVITY_SIGNAL_QUERY_MAX_LEN", 1, int64(int(^uint(0) >> 1))},
		{"TRUVAG3_ACTIVITY_SIGNAL_MAX_IN_PROMPT", 1, int64(int(^uint(0) >> 1))},
		{"TRUVAG3_SHARED_MEMORY_STREAM_MAXLEN", 1, 1<<63 - 1},
		{"MAINTENANCE_ASSESSMENT_PAGE_LIMIT", 1, 50},
		{"MAINTENANCE_ASSESSMENT_FACT_MAX_BYTES", 1024, 131072},
		{"MAINTENANCE_ASSESSMENT_LOOKUP_MAX_BYTES", 4096, 1048576},
		{"MAINTENANCE_ASSESSMENT_ORIGIN_MAX_BYTES", 4096, 8 << 20},
		{"MAINTENANCE_LIFECYCLE_DUE_LIMIT", 1, 50},
		{"MAINTENANCE_ADMISSION_MAX_CASES", 1, 1000},
		{"MAINTENANCE_ADMISSION_MAX_PENDING", 1, 100},
		{"MAINTENANCE_INVESTIGATION_WORKERS", 1, 100},
		{"MAINTENANCE_ADMISSION_MAX_PER_WINDOW", 1, 1000},
		{"MAINTENANCE_ADMISSION_FAILURE_QUOTA", 1, 1000},
		{"MAINTENANCE_ADMISSION_SUCCESS_QUOTA", 1, 1000},
	} {
		value, present := lookup(setting.key)
		if !present {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < setting.min || n > setting.max {
			return fmt.Errorf("%s must be an integer in [%d, %d]", setting.key, setting.min, setting.max)
		}
	}
	for _, setting := range []struct {
		key       string
		allowZero bool
	}{
		{"TRUVAG3_SHARED_MEMORY_INVESTIGATION_TTL", false},
		{"TRUVAG3_SHARED_MEMORY_DIGEST_CACHE_TTL", false},
		{"TRUVAG3_ACTIVITY_SIGNAL_TTL", false},
		{"TRUVAG3_SKILL_RESOURCE_SELECTION_ATTEMPT_TIMEOUT", true},
	} {
		value, present := lookup(setting.key)
		if !present {
			continue
		}
		d, err := time.ParseDuration(value)
		if err != nil || d < 0 || (d == 0 && !setting.allowZero) {
			return fmt.Errorf("%s requires a valid Go duration in its supported range", setting.key)
		}
	}
	for _, setting := range []struct {
		key      string
		min, max time.Duration
	}{
		{"MAINTENANCE_ASSESSMENT_REVALIDATE_AFTER", time.Minute, 24 * time.Hour},
		{"MAINTENANCE_ASSESSMENT_RECORD_TIMEOUT", 5 * time.Second, 2 * time.Minute},
		{"MAINTENANCE_REMEDIATION_STATE_MAX_AGE", time.Second, 5 * time.Minute},
		{"MAINTENANCE_LIFECYCLE_TERMINAL_RETENTION", 24 * time.Hour, 365 * 24 * time.Hour},
		{"MAINTENANCE_ADMISSION_RUN_DURATION", time.Minute, 24 * time.Hour},
		{"MAINTENANCE_ADMISSION_WINDOW", time.Minute, time.Hour},
		{"MAINTENANCE_ADMISSION_COOLDOWN", time.Second, time.Hour},
	} {
		if value, present := lookup(setting.key); present {
			d, err := time.ParseDuration(value)
			if err != nil || d < setting.min || d > setting.max {
				return fmt.Errorf("%s requires a Go duration in [%s, %s]", setting.key, setting.min, setting.max)
			}
		}
	}
	return nil
}
