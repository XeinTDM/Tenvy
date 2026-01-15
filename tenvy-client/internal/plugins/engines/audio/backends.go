//go:build cgo && !tenvy_no_audio
// +build cgo,!tenvy_no_audio

package audioengine

import (
	"runtime"

	"github.com/gen2brain/malgo"
)

func fallbackAudioBackendAttempts() [][]malgo.Backend {
	switch runtime.GOOS {
	case "windows":
		return [][]malgo.Backend{
			{malgo.BackendWasapi},
			{malgo.BackendDsound},
			{malgo.BackendWinmm},
		}
	case "darwin":
		return [][]malgo.Backend{
			{malgo.BackendCoreaudio},
		}
	case "android":
		return [][]malgo.Backend{
			{malgo.BackendAaudio},
			{malgo.BackendOpensl},
		}
	case "linux":
		fallthrough
	case "freebsd":
		fallthrough
	case "openbsd":
		fallthrough
	case "netbsd":
		return [][]malgo.Backend{
			{malgo.BackendPulseaudio},
			{malgo.BackendAlsa},
			{malgo.BackendJack},
		}
	default:
		return nil
	}
}
