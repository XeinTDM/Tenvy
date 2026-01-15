package startupengine

import (
	"context"
	"fmt"
	"strings"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type startupEngine struct {
	manager *Manager
}

func NewStartupEngine(cfg Config) Engine {
	return &startupEngine{
		manager: NewManager(cfg.Logger),
	}
}

func (e *startupEngine) Configure(cfg Config) error {
	e.manager.UpdateLogger(cfg.Logger)
	return nil
}

func (e *startupEngine) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	return e.manager.HandleCommand(ctx, cmd)
}

func (e *startupEngine) Shutdown() {
	// No specific shutdown logic needed for startup manager currently
}

func pluginEndpoints(baseURL, agentID, pluginID string) (manifestURL, artifactURL string) {
	baseURL = strings.TrimRight(baseURL, "/")
	manifestURL = fmt.Sprintf("%s/api/plugins/%s/manifest", baseURL, pluginID)
	artifactURL = fmt.Sprintf("%s/api/plugins/%s/artifact", baseURL, pluginID)
	return
}
