//go:build !windows

package appvncengine

import (
	"context"
	"github.com/rootbay/tenvy-client/internal/protocol"
)

func processAppVncInput(ctx context.Context, session *sessionState, events []protocol.AppVncInputEvent) error {
	return nil
}
