//go:build windows

package appvnc

import (
	"context"
	"errors"
	"image"

	"github.com/kbinani/screenshot"
	"github.com/lxn/win"
	winutil "github.com/rootbay/tenvy-client/internal/platform/windows"
)

func defaultSurfaceCaptureFactory(session *sessionState) (surfaceCapturer, error) {
	if session != nil && session.processID != 0 {
		return &windowSurfaceCapturer{pid: uint32(session.processID)}, nil
	}

	displays := screenshot.NumActiveDisplays()
	if displays <= 0 {
		return nil, errors.New("no active displays")
	}
	bounds := screenshot.GetDisplayBounds(0)
	return &screenshotSurfaceCapturer{bounds: bounds}, nil
}

type windowSurfaceCapturer struct {
	pid uint32
}

func (c *windowSurfaceCapturer) Capture(ctx context.Context) (*surfaceFrame, error) {
	hwnd := winutil.FindWindowForPID(c.pid)
	if hwnd == 0 {
		return nil, errors.New("application window not found")
	}

	var rect win.RECT
	if !win.GetWindowRect(hwnd, &rect) {
		return nil, errors.New("failed to get window rect")
	}

	bounds := image.Rect(int(rect.Left), int(rect.Top), int(rect.Right), int(rect.Bottom))
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, errors.New("invalid window bounds")
	}

	// For now, we still use screen capture but cropped to window.
	// This captures whatever is on top of the window too.
	return captureRect(ctx, bounds)
}

func (c *windowSurfaceCapturer) Close() error {
	return nil
}
