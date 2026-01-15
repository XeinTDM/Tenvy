package remotedesktopengine

import (
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/rootbay/tenvy-client/internal/modules/control/screen"
)

func TestCaptureMonitorFrame_AnnotatesBackendErrors(t *testing.T) {
	t.Cleanup(func() {
		captureRectFunc = screen.SafeCaptureRect
		selectedCaptureBackendFunc = screen.SelectedBackend
		captureCapabilityErrorsFunc = screen.CapabilityErrors
	})

	sentinel := errors.New("duplication failed")
	captureRectFunc = func(image.Rectangle) (*image.RGBA, error) {
		return nil, sentinel
	}
	selectedCaptureBackendFunc = func() string { return "dxgi" }

	monitor := remoteMonitor{bounds: image.Rect(0, 0, 10, 10)}
	_, err := captureMonitorFrame(monitor, 10, 10)
	if err == nil {
		t.Fatal("expected error when capture backend fails")
	}
	if !errors.Is(err, sentinel) {
		if !strings.Contains(err.Error(), "dxgi capture failed") {
			t.Fatalf("expected backend annotation in error, got %v", err)
		}
	}
}

func TestCaptureMonitorFrame_ReportsCapabilityDiagnostics(t *testing.T) {
	t.Cleanup(func() {
		captureRectFunc = screen.SafeCaptureRect
		selectedCaptureBackendFunc = screen.SelectedBackend
		captureCapabilityErrorsFunc = screen.CapabilityErrors
	})

	sentinel := errors.New("probe failed")
	captureRectFunc = func(image.Rectangle) (*image.RGBA, error) { return nil, sentinel }
	selectedCaptureBackendFunc = func() string { return "" }
	captureCapabilityErrorsFunc = func() []*screen.CapabilityError {
		return []*screen.CapabilityError{{Backend: "pipewire", Err: errors.New("permission denied")}}
	}

	monitor := remoteMonitor{bounds: image.Rect(0, 0, 5, 5)}
	_, err := captureMonitorFrame(monitor, 5, 5)
	if err == nil {
		t.Fatal("expected error when capture backend fails")
	}
	if !strings.Contains(err.Error(), "capture unavailable") {
		t.Fatalf("expected capability diagnostics in error, got %v", err)
	}
}

func TestRefreshMonitorsLocked_DetectsMultiGPUChanges(t *testing.T) {
	controller := &remoteDesktopSessionController{}
	session := &RemoteDesktopSession{Settings: RemoteDesktopSettings{Monitor: 1}}

	first := []remoteMonitor{
		{info: RemoteDesktopMonitorInfo{ID: 1, Label: "GPU0-A", Width: 1920, Height: 1080}, bounds: image.Rect(0, 0, 1920, 1080)},
		{info: RemoteDesktopMonitorInfo{ID: 2, Label: "GPU0-B", Width: 1280, Height: 1024}, bounds: image.Rect(1920, 0, 3200, 1024)},
	}
	second := []remoteMonitor{
		{info: RemoteDesktopMonitorInfo{ID: 11, Label: "GPU1-A", Width: 2560, Height: 1440}, bounds: image.Rect(0, 0, 2560, 1440)},
		{info: RemoteDesktopMonitorInfo{ID: 12, Label: "GPU1-B", Width: 1920, Height: 1080}, bounds: image.Rect(2560, 0, 4480, 1080)},
	}

	call := 0
	t.Cleanup(func() {
		detectRemoteMonitorsFunc = detectRemoteMonitors
	})
	detectRemoteMonitorsFunc = func() []remoteMonitor {
		if call == 0 {
			call++
			return first
		}
		return second
	}

	controller.refreshMonitorsLocked(session, true)
	if len(session.monitors) != len(first) {
		t.Fatalf("expected %d monitors after initial refresh, got %d", len(first), len(session.monitors))
	}
	if session.Settings.Monitor != 1 {
		t.Fatalf("expected monitor index to remain 1, got %d", session.Settings.Monitor)
	}
	if !session.monitorsDirty {
		t.Fatal("expected monitors to be marked dirty after initial refresh")
	}

	session.lastMonitorRefresh = time.Time{}
	session.monitorsDirty = false
	session.Settings.Monitor = 0
	controller.refreshMonitorsLocked(session, true)

	if len(session.monitors) != len(second) {
		t.Fatalf("expected %d monitors after topology change, got %d", len(second), len(session.monitors))
	}
	if session.Settings.Monitor != 0 {
		t.Fatalf("expected monitor index to clamp to 0, got %d", session.Settings.Monitor)
	}
	if !session.monitorsDirty {
		t.Fatal("expected monitors to be marked dirty after topology change")
	}
	if session.LastFrame != nil {
		t.Fatal("expected last frame to be cleared after topology change")
	}
	if !session.ForceKeyFrame {
		t.Fatal("expected key frame to be forced after topology change")
	}
}
