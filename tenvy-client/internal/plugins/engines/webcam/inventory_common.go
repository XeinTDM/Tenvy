package webcamengine

import "github.com/rootbay/tenvy-client/internal/protocol"

type inventoryCaptureFunc func() ([]protocol.WebcamDevice, string, error)

var captureWebcamInventory inventoryCaptureFunc = platformCaptureWebcamInventory
