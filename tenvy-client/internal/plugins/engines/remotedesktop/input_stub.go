//go:build (!windows && !linux && !darwin) || (darwin && !cgo)

package remotedesktopengine

import "errors"

func processRemoteInput(monitors []remoteMonitor, settings RemoteDesktopSettings, events []RemoteDesktopInputEvent) error {
	if len(events) == 0 {
		return nil
	}
	return errors.New("remote desktop input is not implemented for this platform")
}
