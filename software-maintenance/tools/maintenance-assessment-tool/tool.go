package main

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/caseorigin"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/maintenanceprofile"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/serviceevidence"
	"github.com/truvaagents/truva-g3/core"
)

const assessmentToolName = "maintenance-assessment-tool"

var observationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var coordinatorCaseIDPattern = observationIDPattern

type assessmentTool struct {
	*core.BaseTool
	profile         maintenanceprofile.Profile
	auth            *coordinatorAuthorizer
	serviceAuth     *coordinatorAuthorizer
	redisClient     redis.UniversalClient
	cases           caseorigin.Reader
	assessments     *lifecycle.Store
	lifecycleLimits lifecycle.Limits
	evidence        *serviceevidence.Reader
	now             func() time.Time
}

func newAssessmentTool() (*assessmentTool, error) {
	profile, err := maintenanceprofile.Load(os.Getenv("MAINTENANCE_ASSESSMENT_PROFILE_FILE"))
	if err != nil {
		return nil, err
	}
	limits, err := lifecycle.ResolveLimits(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	auth, err := newCoordinatorAuthorizer("maintenance-operator", os.Getenv("MAINTENANCE_AGENT_API_TOKEN"))
	if err != nil {
		return nil, err
	}
	serviceAuth, err := newCoordinatorAuthorizer("maintenance-agent", os.Getenv("MAINTENANCE_AGENT_SERVICE_TOKEN"))
	if err != nil {
		return nil, err
	}
	if auth.token == serviceAuth.token {
		return nil, errors.New("assessment operator and service credentials must be distinct")
	}
	evidence, err := serviceevidence.NewReader(os.Getenv("MAINTENANCE_EVIDENCE_PROFILES_FILE"))
	if err != nil {
		return nil, err
	}
	if err := evidence.CheckProfile(profile.ServiceProfileID, profile.ExpectedServiceProfileVersion); err != nil {
		evidence.Close()
		return nil, err
	}
	config, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		evidence.Close()
		return nil, err
	}
	client, err := core.NewRedisUniversalClient(config)
	if err != nil {
		evidence.Close()
		return nil, err
	}
	// Close construction resources on every subsequent configuration failure.
	accepted := false
	defer func() {
		if !accepted {
			_ = client.Close()
			evidence.Close()
		}
	}()
	keys, err := core.NewRedisKeyspace(os.Getenv("TRUVAG3_REDIS_NAMESPACE"))
	if err != nil {
		return nil, err
	}
	environment := os.Getenv("NAMESPACE")
	if environment == "" {
		environment = "local"
	}
	if profile.AssessmentPolicy != nil {
		environment = profile.AssessmentPolicy.EnvironmentID
	}
	store, err := lifecycle.NewStore(client, keys, lifecycle.Scope{CoordinatorProfileID: profile.CoordinatorProfileID, ServiceProfileID: profile.ServiceProfileID, RepositoryID: profile.RepositoryID, EnvironmentID: environment})
	if err != nil {
		return nil, err
	}
	tool := &assessmentTool{BaseTool: core.NewTool(assessmentToolName), profile: profile, auth: auth, serviceAuth: serviceAuth, redisClient: client, cases: caseorigin.Reader{Client: client, Keys: keys, MaximumBytes: limits.OriginMaxBytes}, assessments: store, lifecycleLimits: limits, evidence: evidence, now: func() time.Time { return time.Now().UTC() }}
	checks, err := json.Marshal(tool.assessmentChecks())
	if err != nil || len(checks)+1024 > limits.LookupMaxBytes {
		return nil, errors.New("assessment lookup byte limit cannot contain configured check locators")
	}
	tool.registerAssessmentCapabilities()
	tool.registerRecordInspection()
	accepted = true
	return tool, nil
}
