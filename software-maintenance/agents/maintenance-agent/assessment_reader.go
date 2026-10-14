package main

import (
	"encoding/json"
	"errors"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
)

// Read-only admission checks use the same store scope. The assessment tool is its public writer.
func (agent *maintenanceAgent) initializeAssessments(lookup func(string) (string, bool)) error {
	limits, err := lifecycle.ResolveLimits(lookup)
	if err != nil {
		return err
	}
	environment, _ := lookup("NAMESPACE")
	if environment == "" {
		environment = "local"
	}
	if agent.profile.AssessmentPolicy != nil {
		environment = agent.profile.AssessmentPolicy.EnvironmentID
	}
	checks, err := json.Marshal(agent.assessmentChecks())
	if err != nil || len(checks)+1024 > limits.LookupMaxBytes {
		return errors.New("assessment lookup byte limit cannot contain the configured check locators")
	}
	scope := lifecycle.Scope{CoordinatorProfileID: agent.profile.CoordinatorProfileID, ServiceProfileID: agent.profile.ServiceProfileID, RepositoryID: agent.profile.RepositoryID, EnvironmentID: environment}
	store, err := lifecycle.NewStore(agent.cases.client, agent.cases.keyspace, scope)
	if err != nil {
		return err
	}
	auth, err := newCoordinatorAuthorizer(maintenanceAgentName, agent.serviceToken)
	if err != nil {
		return err
	}
	agent.assessments, agent.assessmentAuth, agent.lifecycleLimits = store, auth, limits
	return nil
}
