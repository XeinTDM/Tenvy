//go:build linux

package webcamengine

import "github.com/rootbay/tenvy-client/internal/protocol"

func platformCaptureWebcamInventory() ([]protocol.WebcamDevice, string, error) {
	return captureV4L2WebcamInventory()
}
