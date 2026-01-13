//go:build linux && !cgo

package remotedesktopengine

import "errors"

func getWaylandBackend() (linuxInputBackend, error) {
	return nil, errors.New("wayland input backend requires cgo")
}
