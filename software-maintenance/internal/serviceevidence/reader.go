package serviceevidence

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Reader reads only operator-allowlisted external service APIs. It has no
// registry, discovery, agent client or orchestration dependency.
type Reader struct {
	profiles *profileStore
	client   evidenceClient
	now      func() time.Time
}

func NewReader(filename string) (*Reader, error) {
	profiles, err := loadProfileStore(filename)
	if err != nil {
		return nil, err
	}
	return &Reader{profiles: profiles, client: newServiceClient(), now: func() time.Time { return time.Now().UTC() }}, nil
}

func (r *Reader) CheckProfile(id, version string) error {
	p, ok := r.profiles.get(id)
	if !ok || p.version != version {
		return errors.New("service evidence profile does not match its authorized pin")
	}
	return nil
}

// Read uses the same validated response builders as the evidence HTTP tool.
// Parameters select identifiers within the startup profile, never URLs.
func (r *Reader) Read(ctx context.Context, operation string, params map[string]string) (json.RawMessage, error) {
	p, ok := r.profiles.get(params["profile_id"])
	if !ok {
		return nil, errors.New("authorized service evidence profile was not found")
	}
	var result any
	var err error
	switch operation {
	case "get_service_profile":
		result = p.public()
	case "get_service_evidence":
		if !identifierPattern.MatchString(params["observation_id"]) {
			return nil, errors.New("invalid observation identifier")
		}
		result, err = readEvidence(ctx, p, r.client, r.now, params["observation_id"])
	case "get_business_outcome":
		if !identifierPattern.MatchString(params["outcome_id"]) {
			return nil, errors.New("invalid outcome identifier")
		}
		result, err = readOutcome(ctx, p, r.client, r.now, params["outcome_id"])
	case "get_release_context":
		if v := params["expected_revision"]; v != "" && !buildRevisionPattern.MatchString(v) {
			return nil, errors.New("invalid serving revision")
		}
		result, err = readRelease(ctx, p, r.client, r.now, params["expected_revision"])
	case "get_runtime_configuration":
		result, err = readConfiguration(ctx, p, r.client, r.now)
	default:
		return nil, errors.New("unsupported service evidence read")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (r *Reader) Close() {
	if c, ok := r.client.(*serviceClient); ok {
		c.httpClient.CloseIdleConnections()
	}
}
