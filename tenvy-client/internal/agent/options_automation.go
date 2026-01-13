package agent

import (
	"context"
	"time"
)

func (a *Agent) monitorOptionsAutomation(ctx context.Context) {
	if a == nil || a.options == nil {
		return
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	fastTicker := time.NewTicker(5 * time.Second)
	defer fastTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.runPeriodicOptions(ctx)
		case <-fastTicker.C:
			a.runFastPeriodicOptions(ctx)
		}
	}
}

func (a *Agent) runPeriodicOptions(ctx context.Context) {
	state := a.options.Snapshot()

	if state.SpeechSpam {
		a.invokeOptionOperation(ctx, "speech-spam-internal", nil)
	}
}

func (a *Agent) runFastPeriodicOptions(ctx context.Context) {
	state := a.options.Snapshot()

	if state.AutoMinimize {
		a.invokeOptionOperation(ctx, "auto-minimize", map[string]any{"enabled": true})
	}

	if state.KeyboardMode != "" && state.KeyboardMode != "None" {
		a.invokeOptionOperation(ctx, "keyboard-mode", map[string]any{"mode": state.KeyboardMode})
	}
}

func (a *Agent) invokeOptionOperation(ctx context.Context, operation string, metadata map[string]any) {
	_, _ = a.options.ApplyOperation(ctx, operation, metadata, nil)
}
