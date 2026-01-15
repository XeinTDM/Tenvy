package taskmanagerengine

import (
	"context"
	"fmt"
	"strings"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type taskManagerEngine struct {
	manager *Manager
}

func NewTaskManagerEngine(cfg Config) Engine {
	return &taskManagerEngine{
		manager: NewManager(cfg.Logger),
	}
}

func (e *taskManagerEngine) Configure(cfg Config) error {
	e.manager.UpdateLogger(cfg.Logger)
	return nil
}

func (e *taskManagerEngine) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	return e.manager.HandleCommand(ctx, cmd)
}

func (e *taskManagerEngine) Shutdown() {
	// No specific shutdown logic needed for task manager currently
}

func pluginEndpoints(baseURL, agentID, pluginID string) (manifestURL, artifactURL string) {
	baseURL = strings.TrimRight(baseURL, "/")
	manifestURL = fmt.Sprintf("%s/api/plugins/%s/manifest", baseURL, pluginID)
	artifactURL = fmt.Sprintf("%s/api/plugins/%s/artifact", baseURL, pluginID)
	return
}
