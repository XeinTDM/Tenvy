package appvncengine

import (
	"context"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type Engine interface {
	Configure(cfg Config) error
	HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult
	HandleInputBurst(ctx context.Context, burst protocol.AppVncInputBurst) error
	Shutdown(ctx context.Context)
}
