package filemanagerengine

import (
	"context"
	"fmt"
	"strings"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type fileManagerEngine struct {
	manager *Manager
}

func NewFileManagerEngine(cfg Config) Engine {
	return &fileManagerEngine{
		manager: NewManager(cfg),
	}
}

func (e *fileManagerEngine) Configure(cfg Config) error {
	e.manager.UpdateConfig(cfg)
	return nil
}

func (e *fileManagerEngine) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	return e.manager.HandleCommand(ctx, cmd)
}

func (e *fileManagerEngine) Shutdown() {
	// No specific shutdown logic needed for file manager currently
}

func pluginEndpoints(baseURL, agentID, pluginID string) (manifestURL, artifactURL string) {
	baseURL = strings.TrimRight(baseURL, "/")
	manifestURL = fmt.Sprintf("%s/api/plugins/%s/manifest", baseURL, pluginID)
	artifactURL = fmt.Sprintf("%s/api/plugins/%s/artifact", baseURL, pluginID)
	return
}
