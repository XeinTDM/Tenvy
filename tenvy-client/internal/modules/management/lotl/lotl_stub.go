//go:build !windows

package lotl

import (
	"context"
	"fmt"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type LotlManager struct {
	logger Logger
}

type Logger interface {
	Printf(format string, args ...interface{})
}

func NewManager(logger Logger) *LotlManager {
	return &LotlManager{logger: logger}
}

func (m *LotlManager) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	return protocol.CommandResult{
		CommandID: cmd.ID,
		Success:   false,
		Error:     "lotl module not supported on this platform",
	}
}
