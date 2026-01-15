package appvncengine

import (
	"context"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type appVncEngine struct {
	controller *Controller
}

func NewAppVncEngine(cfg Config) Engine {
	c := NewController()
	c.Update(cfg)
	return &appVncEngine{
		controller: c,
	}
}

func (e *appVncEngine) Configure(cfg Config) error {
	e.controller.Update(cfg)
	return nil
}

func (e *appVncEngine) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	return e.controller.HandleCommand(ctx, cmd)
}

func (e *appVncEngine) HandleInputBurst(ctx context.Context, burst protocol.AppVncInputBurst) error {
	return e.controller.HandleInputBurst(ctx, burst)
}

func (e *appVncEngine) Shutdown(ctx context.Context) {
	e.controller.Shutdown(ctx)
}
