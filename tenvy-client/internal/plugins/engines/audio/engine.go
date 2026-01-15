package audioengine

import (
	"context"
	"fmt"
	"strings"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type audioEngine struct {
	bridge *AudioBridge
}

func NewAudioEngine(cfg Config) Engine {
	return &audioEngine{
		bridge: NewAudioBridge(cfg),
	}
}

func (e *audioEngine) Configure(cfg Config) error {
	e.bridge.UpdateConfig(cfg)
	return nil
}

func (e *audioEngine) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	return e.bridge.HandleCommand(ctx, cmd)
}

func (e *audioEngine) Shutdown() {
	if e.bridge != nil {
		e.bridge.Shutdown()
	}
}

func pluginEndpoints(baseURL, agentID, pluginID string) (manifestURL, artifactURL string) {
	baseURL = strings.TrimRight(baseURL, "/")
	manifestURL = fmt.Sprintf("%s/api/plugins/%s/manifest", baseURL, pluginID)
	artifactURL = fmt.Sprintf("%s/api/plugins/%s/artifact", baseURL, pluginID)
	return
}
