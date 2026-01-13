//go:build !windows

package appvnc

import (
	"errors"

	"github.com/kbinani/screenshot"
)

func defaultSurfaceCaptureFactory(session *sessionState) (surfaceCapturer, error) {
	displays := screenshot.NumActiveDisplays()
	if displays <= 0 {
		return nil, errors.New("no active displays")
	}
	bounds := screenshot.GetDisplayBounds(0)
	return &screenshotSurfaceCapturer{bounds: bounds}, nil
}
