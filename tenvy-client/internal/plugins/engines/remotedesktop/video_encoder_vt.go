//go:build darwin

package remotedesktopengine

import (
	"sync"
)

var (
	videoToolboxOnce sync.Once
	videoToolboxErr  error
)

func ensureVideoToolboxRuntime() error {
	videoToolboxOnce.Do(func() {
		videoToolboxErr = ErrNativeEncoderUnavailable
	})
	return videoToolboxErr
}

func platformNewNativeHEVCVideoEncoder() (clipVideoEncoder, error) {
	if err := ensureVideoToolboxRuntime(); err != nil {
		return nil, err
	}
	return nil, ErrNativeEncoderUnavailable
}

func platformNewNativeAVCVideoEncoder() (clipVideoEncoder, error) {
	if err := ensureVideoToolboxRuntime(); err != nil {
		return nil, err
	}
	return nil, ErrNativeEncoderUnavailable
}
