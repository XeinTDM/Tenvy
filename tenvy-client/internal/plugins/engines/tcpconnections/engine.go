package tcpconnectionsengine

import (
	"context"
	"fmt"
	"strings"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type tcpConnectionsEngine struct {
	manager *Manager
}

func NewTcpConnectionsEngine(cfg Config) Engine {
	return &tcpConnectionsEngine{
		manager: NewManager(cfg),
	}
}

func (e *tcpConnectionsEngine) Configure(cfg Config) error {
	e.manager.UpdateConfig(cfg)
	return nil
}

func (e *tcpConnectionsEngine) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	return e.manager.HandleCommand(ctx, cmd)
}

func (e *tcpConnectionsEngine) Shutdown() {
	// No specific shutdown logic needed for tcpconnections manager currently
}

func pluginEndpoints(baseURL, agentID, pluginID string) (manifestURL, artifactURL string) {
	baseURL = strings.TrimRight(baseURL, "/")
	manifestURL = fmt.Sprintf("%s/api/plugins/%s/manifest", baseURL, pluginID)
	artifactURL = fmt.Sprintf("%s/api/plugins/%s/artifact", baseURL, pluginID)
	return
}
