package screen

import (
	"errors"
	"fmt"
	"image"
	"sync"

	"github.com/kbinani/screenshot"
)

type captureBackend interface {
	Capture(bounds image.Rectangle) (*image.RGBA, error)
	Name() string
}

type backendFactory func() (captureBackend, error)

type backendCandidate struct {
	name    string
	factory backendFactory
}

type CapabilityError struct {
	Backend string
	Err     error
}

func (e *CapabilityError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("capture backend %s unavailable", e.Backend)
	}
	return fmt.Sprintf("capture backend %s unavailable: %v", e.Backend, e.Err)
}

func (e *CapabilityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

var (
	backendOnce sync.Once
	backend     captureBackend
	backendErr  error

	backendName string

	capabilityErrMu sync.Mutex
	capabilityErrs  []*CapabilityError

	platformCaptureCandidates func() []backendCandidate = defaultPlatformCaptureCandidates

	fallbackCaptureFactory backendFactory = newScreenshotBackend
)

// SafeCaptureRect recovers from platform panics so transient graphics driver
// resets do not crash the agent.
func SafeCaptureRect(bounds image.Rectangle) (img *image.RGBA, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("capture panic: %v", r)
			img = nil
		}
	}()

	candidate, err := ensureBackend()
	if err != nil {
		return nil, err
	}
	return candidate.Capture(bounds)
}

func SelectedBackend() string {
	ensureBackend()
	return backendName
}

func CapabilityErrors() []*CapabilityError {
	capabilityErrMu.Lock()
	defer capabilityErrMu.Unlock()
	out := make([]*CapabilityError, len(capabilityErrs))
	copy(out, capabilityErrs)
	return out
}

func ensureBackend() (captureBackend, error) {
	backendOnce.Do(func() {
		var candidates []backendCandidate
		if platformCaptureCandidates != nil {
			candidates = append(candidates, platformCaptureCandidates()...)
		}
		candidates = append(candidates, backendCandidate{name: "screenshot", factory: fallbackCaptureFactory})

		for _, candidate := range candidates {
			instance, err := candidate.factory()
			if err != nil {
				recordCapabilityError(candidate.name, err)
				continue
			}
			backend = instance
			if name := instance.Name(); name != "" {
				backendName = name
			} else {
				backendName = candidate.name
			}
			return
		}

		backendErr = errors.New("no capture backend available")
	})

	if backend != nil {
		return backend, nil
	}
	return nil, backendErr
}

func recordCapabilityError(name string, err error) {
	capabilityErrMu.Lock()
	defer capabilityErrMu.Unlock()
	capabilityErrs = append(capabilityErrs, &CapabilityError{Backend: name, Err: err})
}

func resetCaptureBackendForTesting() {
	capabilityErrMu.Lock()
	defer capabilityErrMu.Unlock()

	backendOnce = sync.Once{}
	backend = nil
	backendErr = nil
	backendName = ""
	capabilityErrs = nil
	fallbackCaptureFactory = newScreenshotBackend
}

type screenshotBackend struct{}

func newScreenshotBackend() (captureBackend, error) {
	return screenshotBackend{}, nil
}

func (screenshotBackend) Name() string {
	return "screenshot"
}

func (screenshotBackend) Capture(bounds image.Rectangle) (*image.RGBA, error) {
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, errors.New("nil capture result")
	}

	// Normalise the image to RGBA in the unlikely scenario the backend
	// returns a different colour model.
	var raw image.Image = img
	if rgba, ok := raw.(*image.RGBA); ok {
		return rgba, nil
	}

	normalized := image.NewRGBA(raw.Bounds())
	drawImage(normalized, raw)
	return normalized, nil
}

func drawImage(dst *image.RGBA, src image.Image) {
	bounds := src.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dst.Set(x, y, src.At(x, y))
		}
	}
}
