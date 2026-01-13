package agent

import (
	"context"
	"time"
)

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func timestampNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if ctx == nil {
		panic("sleepContext called with nil context")
	}

	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
