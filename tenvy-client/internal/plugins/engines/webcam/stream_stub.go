//go:build !linux && !windows && !darwin && !freebsd

package webcamengine

import (
	"errors"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

func defaultFrameSourceFactory(deviceID string, settings *protocol.WebcamStreamSettings) (frameSource, error) {
	return nil, errors.New("webcam streaming is not supported on this platform")
}
