package main

import (
	"fmt"
	"io"
	"os"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/orchestration/redisprovider"
)

func newSkillRegistry(logger core.Logger) (orchestration.SkillRegistry, io.Closer, error) {
	config, err := redisprovider.LoadClientConfigFromEnvironment(redisprovider.DefaultClientConfig(), os.LookupEnv)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve skill backend: %w", err)
	}
	clients, err := redisprovider.NewOwnedClients(config)
	if err != nil {
		return nil, nil, fmt.Errorf("create skill clients: %w", err)
	}
	store, err := redisprovider.NewSkillStore(clients.ClientSet().Resolve(redisprovider.ClientRoleSkills), redisprovider.WithSkillStoreLogger(logger))
	if err != nil {
		_ = clients.Close()
		return nil, nil, fmt.Errorf("create skill registry: %w", err)
	}
	return store, clients, nil
}
