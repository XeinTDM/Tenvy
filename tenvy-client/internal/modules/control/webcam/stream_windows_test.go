//go:build windows

package webcam

import (
	"testing"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

func TestMFFrameSourceApplySettings(t *testing.T) {
	deviceID := "\\\\?\\USB#VID_046D&PID_082D&MI_00#6&1a2b3c4d&0&0000#{65e8773d-8f56-11d0-a3b9-00a0c9223196}\\{bbefb6c7-2fc4-4139-bb8b-a58bba724083}"

	initialSettings := &protocol.WebcamStreamSettings{
		Width:     640,
		Height:    480,
		FrameRate: 15,
	}

	source, err := newMFFrameSource(deviceID, initialSettings)
	if err != nil {
		t.Fatalf("Failed to create frame source: %v", err)
	}

	if source.settings != initialSettings {
		t.Errorf("Initial settings not stored correctly")
	}

	newSettings := &protocol.WebcamStreamSettings{
		Width:     1280,
		Height:    720,
		FrameRate: 30,
	}

	err = source.ApplySettings(newSettings)
	if err != nil {
		t.Errorf("ApplySettings failed: %v", err)
	}

	if source.settings != newSettings {
		t.Errorf("Settings not updated correctly")
	}

	err = source.ApplySettings(nil)
	if err != nil {
		t.Errorf("ApplySettings with nil should not fail: %v", err)
	}

	err = source.ApplySettings(initialSettings)
	if err != nil {
		t.Errorf("ApplySettings before starting failed: %v", err)
	}

	source.reader = nil
	err = source.ApplySettings(newSettings)
	if err != nil {
		t.Errorf("ApplySettings with nil reader should not fail: %v", err)
	}

	t.Log("All ApplySettings logic tests passed")
}
