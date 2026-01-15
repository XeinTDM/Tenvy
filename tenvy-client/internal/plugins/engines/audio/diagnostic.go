//go:build cgo && !tenvy_no_audio
// +build cgo,!tenvy_no_audio

package audioengine

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
)

func RunCaptureDiagnostic(ctx context.Context, captureWindow time.Duration) (*AudioDiagnosticResult, error) {
	if captureWindow <= 0 {
		captureWindow = 2 * time.Second
	}

	inventory, err := captureAudioInventory()
	if err != nil {
		return nil, err
	}

	result := &AudioDiagnosticResult{
		Inventory: inventory,
		Duration:  captureWindow,
	}

	if inventory == nil || len(inventory.Inputs) == 0 {
		return result, errors.New("no input audio devices available")
	}

	allocatedCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return result, fmt.Errorf("failed to initialize audio context: %w", err)
	}
	defer func() {
		_ = allocatedCtx.Context.Uninit()
		allocatedCtx.Free()
	}()

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = 1
	deviceConfig.SampleRate = 48000
	deviceConfig.Alsa.NoMMap = 1
	deviceConfig.Capture.ShareMode = malgo.Shared

	var bytesCaptured atomic.Uint64

	callbacks := malgo.DeviceCallbacks{
		Data: func(_ []byte, input []byte, _ uint32) {
			if len(input) == 0 {
				return
			}
			bytesCaptured.Add(uint64(len(input)))
		},
	}

	device, err := malgo.InitDevice(allocatedCtx.Context, deviceConfig, callbacks)
	if err != nil {
		return result, fmt.Errorf("failed to initialize capture device: %w", err)
	}
	defer device.Uninit()

	if err := device.Start(); err != nil {
		return result, fmt.Errorf("failed to start capture device: %w", err)
	}

	timer := time.NewTimer(captureWindow)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		_ = device.Stop()
		result.BytesCaptured = bytesCaptured.Load()
		return result, ctx.Err()
	case <-timer.C:
	}

	if err := device.Stop(); err != nil {
		result.BytesCaptured = bytesCaptured.Load()
		return result, fmt.Errorf("failed to stop capture device: %w", err)
	}

	result.BytesCaptured = bytesCaptured.Load()
	return result, nil
}
