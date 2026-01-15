//go:build !cgo || tenvy_no_audio
// +build !cgo tenvy_no_audio

package audioengine

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type (
	Command       = protocol.Command
	CommandResult = protocol.CommandResult
)

type AudioBridge struct {
	cfg Config
}

func NewAudioBridge(cfg Config) *AudioBridge {
	return &AudioBridge{cfg: cfg}
}

func (b *AudioBridge) UpdateConfig(cfg Config) {
	if b == nil {
		return
	}
	b.cfg = cfg
}

func (b *AudioBridge) Shutdown() {}

func (b *AudioBridge) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	_ = ctx

	return CommandResult{
		CommandID:   cmd.ID,
		Success:     false,
		Error:       "audio-control unavailable: client built without cgo/audio support",
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func RunCaptureDiagnostic(ctx context.Context, duration time.Duration) (*AudioDiagnosticResult, error) {
	_ = ctx
	_ = duration
	return nil, errors.New("audio diagnostics unavailable: client built without audio support")
}
