package registryengine

import (
	"context"
	"fmt"
	"strings"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type registryEngine struct {
	manager *Manager
}

func NewRegistryEngine(cfg Config) Engine {
	return &registryEngine{
		manager: NewManager(cfg.Logger),
	}
}

func (e *registryEngine) Configure(cfg Config) error {
	e.manager.UpdateLogger(cfg.Logger)
	return nil
}

func (e *registryEngine) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	return e.manager.HandleCommand(ctx, cmd)
}

func (e *registryEngine) Shutdown() {
	// No specific shutdown logic needed for registry manager currently
}

func pluginEndpoints(baseURL, agentID, pluginID string) (manifestURL, artifactURL string) {
	baseURL = strings.TrimRight(baseURL, "/")
	manifestURL = fmt.Sprintf("%s/api/plugins/%s/manifest", baseURL, pluginID)
	artifactURL = fmt.Sprintf("%s/api/plugins/%s/artifact", baseURL, pluginID)
	return
}
