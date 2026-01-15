package triggerengine

import (
	"context"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type triggerEngine struct {
	manager *Manager
}

func NewTriggerEngine(cfg Config) Engine {
	return &triggerEngine{
		manager: NewManager(),
	}
}

func (e *triggerEngine) Configure(cfg Config) error {
	// No specific configuration for trigger manager currently
	return nil
}

func (e *triggerEngine) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	return e.manager.HandleCommand(ctx, cmd)
}

func (e *triggerEngine) Shutdown() {
	// No specific shutdown logic needed for trigger manager currently
}
