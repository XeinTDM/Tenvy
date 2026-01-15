package webcamengine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type webcamEngine struct {
	manager *Manager
}

func NewWebcamEngine(cfg Config) Engine {
	return &webcamEngine{
		manager: NewManager(cfg),
	}
}

func (e *webcamEngine) Configure(cfg Config) error {
	e.manager.UpdateConfig(cfg)
	return nil
}

func (e *webcamEngine) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	return e.manager.HandleCommand(ctx, cmd)
}

func (e *webcamEngine) Shutdown() {
	// Manager cleanup logic if needed
	e.manager.mu.Lock()
	sessions := make([]*streamSession, 0, len(e.manager.sessions))
	for _, s := range e.manager.sessions {
		sessions = append(sessions, s)
	}
	e.manager.sessions = make(map[string]*streamSession)
	e.manager.mu.Unlock()

	for _, s := range sessions {
		s.stop()
		s.wait(2 * time.Second)
	}
}

func pluginEndpoints(baseURL, agentID, pluginID string) (manifestURL, artifactURL string) {
	baseURL = strings.TrimRight(baseURL, "/")
	manifestURL = fmt.Sprintf("%s/api/plugins/%s/manifest", baseURL, pluginID)
	artifactURL = fmt.Sprintf("%s/api/plugins/%s/artifact", baseURL, pluginID)
	return
}
