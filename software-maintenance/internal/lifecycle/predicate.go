package lifecycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var slug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func Digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func ValidatePolicy(policy *Policy) error {
	if policy == nil {
		return nil
	}
	if policy.SchemaVersion != 1 || !slug.MatchString(policy.Revision) || !slug.MatchString(policy.EnvironmentID) || len(policy.Rules) < 1 || len(policy.Rules) > 8 {
		return errors.New("assessment policy schema, revision, environment or rule count is invalid")
	}
	seen := make(map[string]bool)
	for _, rule := range policy.Rules {
		if !slug.MatchString(rule.CheckID) || len(rule.Operation) < 1 || len(rule.Operation) > 256 || !utf8.ValidString(rule.Operation) || strings.TrimSpace(rule.Operation) != rule.Operation || strings.ContainsAny(rule.Operation, "\x00\r\n") || seen[rule.CheckID] {
			return errors.New("assessment check identity or operation is invalid/duplicated")
		}
		seen[rule.CheckID] = true
		if len(rule.AllowEmptyStringPaths) > 32 {
			return errors.New("empty-string policy exceeds the selector limit")
		}
		emptySeen := make(map[string]bool)
		for _, path := range rule.AllowEmptyStringPaths {
			if !validPath(path) || emptySeen[path] || (!slices.Contains(rule.SignaturePaths, path) && !slices.Contains(rule.ComparisonPaths, path)) {
				return errors.New("empty-string policy requires unique selected fact paths")
			}
			emptySeen[path] = true
		}
		for _, paths := range [][]string{rule.SignaturePaths, rule.ComparisonPaths} {
			if len(paths) < 1 || len(paths) > 32 {
				return errors.New("assessment selector count must be in [1, 32]")
			}
			pathsSeen := make(map[string]bool)
			for _, path := range paths {
				if !validPath(path) || pathsSeen[path] {
					return errors.New("assessment selectors require unique bounded JSON pointers")
				}
				pathsSeen[path] = true
			}
		}
	}
	return nil
}

func PolicyDigest(policy *Policy) string {
	encoded, _ := json.Marshal(policy) // Policy is a validated plain-data schema.
	return Digest(encoded)
}

func validPath(path string) bool {
	if len(path) < 2 || len(path) > 512 || path[0] != '/' || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	for index := 0; index < len(path); index++ {
		if path[index] == '~' {
			if index+1 >= len(path) || path[index+1] != '0' && path[index+1] != '1' {
				return false
			}
			index++
		}
	}
	root := strings.Split(path[1:], "/")[0]
	return root == "observation" || root == "outcome" || root == "release" || root == "configuration" || root == "service"
}

func Select(document any, pointer string) (json.RawMessage, error) {
	return selectFact(document, pointer, false)
}

func selectFact(document any, pointer string, allowEmptyString bool) (json.RawMessage, error) {
	value := document
	for _, segment := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		switch node := value.(type) {
		case map[string]any:
			var exists bool
			value, exists = node[segment]
			if !exists {
				return nil, fmt.Errorf("required fact is unavailable at %s", pointer)
			}
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(node) {
				return nil, fmt.Errorf("required fact is unavailable at %s", pointer)
			}
			value = node[index]
		default:
			return nil, fmt.Errorf("required fact is unavailable at %s", pointer)
		}
	}
	if value == nil {
		return nil, fmt.Errorf("required fact is unknown at %s", pointer)
	}
	if text, ok := value.(string); ok && text == "" && !allowEmptyString {
		return nil, fmt.Errorf("required fact is unknown at %s", pointer)
	}
	if containsNull(value) {
		return nil, fmt.Errorf("required fact contains an unknown value at %s", pointer)
	}
	return json.Marshal(value)
}

func containsNull(value any) bool {
	if value == nil {
		return true
	}
	switch node := value.(type) {
	case map[string]any:
		for _, child := range node {
			if containsNull(child) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if containsNull(child) {
				return true
			}
		}
	}
	return false
}

// BuildFacts transports selected values byte-faithfully after JSON canonical
// encoding. Only owner-reviewed paths exclude instance-specific identities.
func BuildFacts(scope Scope, policy *Policy, rule Rule, document any, mandatory map[string]json.RawMessage, observationID string, observedAt time.Time, evidence []EvidenceReference, maxBytes int) (Facts, error) {
	if err := ValidatePolicy(policy); err != nil || policy == nil {
		return Facts{}, errors.New("a validated owner assessment policy is required")
	}
	if scope.EnvironmentID != policy.EnvironmentID || observationID == "" || observedAt.IsZero() || len(evidence) == 0 || len(mandatory) == 0 {
		return Facts{}, errors.New("assessment facts require complete scope, occurrence, mandatory facts and evidence")
	}
	var configured bool
	ruleBytes, _ := json.Marshal(rule)
	for _, candidate := range policy.Rules {
		candidateBytes, _ := json.Marshal(candidate)
		if bytes.Equal(ruleBytes, candidateBytes) {
			configured = true
		}
	}
	if !configured {
		return Facts{}, errors.New("assessment rule does not match the owner policy")
	}
	facts := Facts{Scope: scope, PolicySHA256: PolicyDigest(policy), CheckID: rule.CheckID, Operation: rule.Operation, ObservationID: observationID, ObservedAt: observedAt, Evidence: evidence, Signature: make(map[string]json.RawMessage), Comparison: make(map[string]json.RawMessage)}
	for path, value := range mandatory {
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		if !validPath(path) || !json.Valid(value) || decoder.Decode(&decoded) != nil || containsNull(decoded) || decoded == "" {
			return Facts{}, errors.New("mandatory comparison fact is invalid or unknown")
		}
		facts.Comparison[path] = append(json.RawMessage(nil), value...)
	}
	for _, target := range []struct {
		paths  []string
		values map[string]json.RawMessage
	}{{rule.SignaturePaths, facts.Signature}, {rule.ComparisonPaths, facts.Comparison}} {
		for _, path := range target.paths {
			value, err := selectFact(document, path, slices.Contains(rule.AllowEmptyStringPaths, path))
			if err != nil {
				return Facts{}, err
			}
			target.values[path] = value
		}
	}
	comparison, err := json.Marshal(facts.Comparison)
	if err != nil {
		return Facts{}, err
	}
	facts.ComparedFactsDigest = Digest(comparison)
	encoded, err := json.Marshal(facts)
	if err != nil || len(encoded) > maxBytes {
		return Facts{}, errors.New("assessment facts exceed the configured byte budget")
	}
	return facts, nil
}

func IssueID(facts Facts) string {
	encoded, _ := json.Marshal(struct {
		Scope         Scope
		Policy, Check string
		Signature     map[string]json.RawMessage
	}{facts.Scope, facts.PolicySHA256, facts.CheckID, facts.Signature})
	return "issue_" + Digest(encoded)
}

func Compare(record Assessment, current Facts, policy *Policy, now time.Time, maximumAge time.Duration) Match {
	match := Match{Assessment: AssessmentReference{AssessmentID: record.AssessmentID, IssueID: record.IssueID, Version: record.Version, OriginCaseID: record.OriginCaseID, OriginExecutionID: record.OriginExecutionID, Disposition: record.Disposition, Summary: record.Summary, RevalidateAfter: record.RevalidateAfter}, Reason: "related_history", ComparedFactsDigest: current.ComparedFactsDigest}
	switch {
	case policy == nil:
		match.Reason = "policy_unconfigured"
	case record.SchemaVersion != 1 || record.Facts.Scope != current.Scope:
		match.Reason = "scope_mismatch"
	case record.Facts.PolicySHA256 != PolicyDigest(policy) || current.PolicySHA256 != record.Facts.PolicySHA256:
		match.Reason = "policy_changed"
	case record.Facts.CheckID != current.CheckID || record.Facts.Operation != current.Operation:
		match.Reason = "check_changed"
	case now.Before(record.AssessedAt) || !now.Before(record.RevalidateAfter) || !now.Before(record.AssessedAt.Add(maximumAge)):
		match.Reason = "assessment_expired"
	case current.ObservationID == "" || len(current.Comparison) == 0 || len(current.Signature) == 0:
		match.Reason = "facts_unknown"
	default:
		previous, _ := json.Marshal(record.Facts.Comparison)
		comparison, _ := json.Marshal(current.Comparison)
		signature, _ := json.Marshal(current.Signature)
		originalSignature, _ := json.Marshal(record.Facts.Signature)
		if record.Facts.ComparedFactsDigest != Digest(previous) || current.ComparedFactsDigest != Digest(comparison) || !bytes.Equal(previous, comparison) || !bytes.Equal(signature, originalSignature) {
			match.Reason = "facts_changed"
		} else {
			match.Eligible, match.Reason = true, "exact_applicability"
		}
	}
	return match
}
