//go:build freebsd

package webcamengine

import "github.com/rootbay/tenvy-client/internal/protocol"

func defaultFrameSourceFactory(deviceID string, settings *protocol.WebcamStreamSettings) (frameSource, error) {
	return newV4L2FrameSource(deviceID, settings)
}
