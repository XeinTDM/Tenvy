//go:build linux || freebsd

package webcam

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackjack/webcam"
	"github.com/rootbay/tenvy-client/internal/protocol"
)

const (
	frameWaitTimeout      = uint32(5000) // ms
	reconfigureRetryDelay = 10 * time.Millisecond
)

type v4l2Device interface {
	StartStreaming() error
	StopStreaming() error
	Close() error
	WaitForFrame(timeout uint32) error
	ReadFrame() ([]byte, error)
	SetImageFormat(format webcam.PixelFormat, width, height uint32) (webcam.PixelFormat, uint32, uint32, error)
	SetFramerate(fps float32) error
	GetSupportedFormats() map[webcam.PixelFormat]string
	GetSupportedFrameSizes(format webcam.PixelFormat) []webcam.FrameSize
	GetSupportedFramerates(format webcam.PixelFormat, width, height uint32) []webcam.FrameRate
}

type realV4L2Device struct {
	cam *webcam.Webcam
}

func (r *realV4L2Device) StartStreaming() error {
	return r.cam.StartStreaming()
}

func (r *realV4L2Device) StopStreaming() error {
	return r.cam.StopStreaming()
}

func (r *realV4L2Device) Close() error {
	return r.cam.Close()
}

func (r *realV4L2Device) WaitForFrame(timeout uint32) error {
	return r.cam.WaitForFrame(timeout)
}

func (r *realV4L2Device) ReadFrame() ([]byte, error) {
	return r.cam.ReadFrame()
}

func (r *realV4L2Device) SetImageFormat(format webcam.PixelFormat, width, height uint32) (webcam.PixelFormat, uint32, uint32, error) {
	return r.cam.SetImageFormat(format, width, height)
}

func (r *realV4L2Device) SetFramerate(fps float32) error {
	return r.cam.SetFramerate(fps)
}

func (r *realV4L2Device) GetSupportedFormats() map[webcam.PixelFormat]string {
	return r.cam.GetSupportedFormats()
}

func (r *realV4L2Device) GetSupportedFrameSizes(format webcam.PixelFormat) []webcam.FrameSize {
	return r.cam.GetSupportedFrameSizes(format)
}

func (r *realV4L2Device) GetSupportedFramerates(format webcam.PixelFormat, width, height uint32) []webcam.FrameRate {
	return r.cam.GetSupportedFramerates(format, width, height)
}

func (r *realV4L2Device) GetName() (string, error) {
	return r.cam.GetName()
}

var openV4L2Device = func(path string) (v4l2Device, error) {
	cam, err := webcam.Open(path)
	if err != nil {
		return nil, err
	}
	return &realV4L2Device{cam: cam}, nil
}

func newV4L2FrameSource(deviceID string, settings *protocol.WebcamStreamSettings) (frameSource, error) {
	trimmed := strings.TrimSpace(deviceID)
	if trimmed == "" {
		return nil, errors.New("webcam device identifier is required")
	}

	cam, err := openV4L2Device(trimmed)
	if err != nil {
		return nil, err
	}

	source := &v4l2FrameSource{devicePath: trimmed, camera: cam}
	if err := source.configure(settings); err != nil {
		cam.Close()
		return nil, err
	}
	return source, nil
}

type v4l2FrameSource struct {
	devicePath string
	camera     v4l2Device
	format     webcam.PixelFormat
	mimeType   string
	started    bool
	mu         sync.Mutex

	reconfiguring atomic.Bool
}

func (s *v4l2FrameSource) configure(settings *protocol.WebcamStreamSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.camera == nil {
		return errors.New("webcam device is closed")
	}

	format, mime := selectPixelFormat(s.camera, s.format)
	width, height := selectFrameSize(s.camera, format, settings, s.format)

	actualFormat, _, _, err := s.camera.SetImageFormat(format, width, height)
	if err != nil {
		return err
	}
	s.format = actualFormat
	s.mimeType = mime

	if settings != nil && settings.FrameRate > 0 {
		fps := float32(settings.FrameRate)
		if fps > 0 {
			_ = s.camera.SetFramerate(fps)
		}
	}
	return nil
}

func (s *v4l2FrameSource) Start(ctx context.Context) (<-chan framePacket, error) {
	s.mu.Lock()
	if s.camera == nil {
		s.mu.Unlock()
		return nil, errors.New("webcam device is closed")
	}
	if s.started {
		s.mu.Unlock()
		return nil, errors.New("webcam capture already started")
	}
	if err := s.camera.StartStreaming(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.started = true
	cam := s.camera
	mimeType := s.mimeType
	source := s
	s.mu.Unlock()

	frames := make(chan framePacket)
	go func() {
		defer close(frames)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if err := cam.WaitForFrame(frameWaitTimeout); err != nil {
				if _, ok := err.(*webcam.Timeout); ok {
					continue
				}
				if source.reconfiguring.Load() {
					time.Sleep(reconfigureRetryDelay)
					continue
				}
				select {
				case frames <- framePacket{Err: err}:
				case <-ctx.Done():
				}
				return
			}

			data, err := cam.ReadFrame()
			if err != nil {
				if source.reconfiguring.Load() {
					time.Sleep(reconfigureRetryDelay)
					continue
				}
				select {
				case frames <- framePacket{Err: err}:
				case <-ctx.Done():
				}
				return
			}
			if len(data) == 0 {
				continue
			}
			copyBuf := make([]byte, len(data))
			copy(copyBuf, data)
			packet := framePacket{Data: copyBuf, MimeType: mimeType, CapturedAt: time.Now()}
			select {
			case frames <- packet:
			case <-ctx.Done():
				return
			}
		}
	}()

	return frames, nil
}

func (s *v4l2FrameSource) ApplySettings(settings *protocol.WebcamStreamSettings) error {
	if settings == nil {
		return nil
	}
	s.mu.Lock()
	started := s.started
	cam := s.camera
	s.mu.Unlock()

	if cam == nil {
		return errors.New("webcam device is closed")
	}

	if started {
		s.reconfiguring.Store(true)
		defer s.reconfiguring.Store(false)
	}

	if started {
		if err := cam.StopStreaming(); err != nil {
			return err
		}
		s.mu.Lock()
		s.started = false
		s.mu.Unlock()
	}

	if err := s.configure(settings); err != nil {
		return err
	}

	s.mu.Lock()
	if s.camera == nil {
		s.mu.Unlock()
		return errors.New("webcam device is closed")
	}
	if started {
		if err := s.camera.StartStreaming(); err != nil {
			s.mu.Unlock()
			return err
		}
		s.started = true
	}
	s.mu.Unlock()
	return nil
}

func (s *v4l2FrameSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.camera != nil {
		if s.started {
			if stopErr := s.camera.StopStreaming(); stopErr != nil {
				err = stopErr
			}
			s.started = false
		}
		if closeErr := s.camera.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		s.camera = nil
	}
	return err
}

func selectPixelFormat(cam v4l2Device, current webcam.PixelFormat) (webcam.PixelFormat, string) {
	if cam == nil {
		return current, "application/octet-stream"
	}
	formats := cam.GetSupportedFormats()
	var fallback webcam.PixelFormat
	fallbackMime := "application/octet-stream"

	for format, desc := range formats {
		lower := strings.ToLower(desc)
		if strings.Contains(lower, "mjpg") || strings.Contains(lower, "jpeg") {
			return format, "image/jpeg"
		}
		if fallback == 0 {
			fallback = format
			if strings.Contains(lower, "yuyv") || strings.Contains(lower, "yuv") {
				fallbackMime = "image/raw"
			}
		}
	}

	if fallback != 0 {
		return fallback, fallbackMime
	}
	if current != 0 {
		return current, fallbackMime
	}
	return webcam.PixelFormat(0), fallbackMime
}

func selectFrameSize(cam v4l2Device, format webcam.PixelFormat, settings *protocol.WebcamStreamSettings, previous webcam.PixelFormat) (uint32, uint32) {
	desiredWidth := uint32(0)
	desiredHeight := uint32(0)
	if settings != nil {
		if settings.Width > 0 {
			desiredWidth = uint32(settings.Width)
		}
		if settings.Height > 0 {
			desiredHeight = uint32(settings.Height)
		}
	}

	sizes := cam.GetSupportedFrameSizes(format)
	if len(sizes) == 0 && previous != 0 {
		sizes = cam.GetSupportedFrameSizes(previous)
	}
	if len(sizes) == 0 {
		if desiredWidth == 0 {
			desiredWidth = 640
		}
		if desiredHeight == 0 {
			desiredHeight = 480
		}
		return desiredWidth, desiredHeight
	}

	type candidate struct {
		width  uint32
		height uint32
	}

	discrete := make([]candidate, 0, len(sizes))
	for _, size := range sizes {
		if size.StepWidth == 0 && size.StepHeight == 0 {
			discrete = append(discrete, candidate{width: size.MaxWidth, height: size.MaxHeight})
		} else {
			if size.MinWidth > 0 && size.MinHeight > 0 {
				discrete = append(discrete, candidate{width: size.MinWidth, height: size.MinHeight})
			}
			if size.MaxWidth > 0 && size.MaxHeight > 0 {
				discrete = append(discrete, candidate{width: size.MaxWidth, height: size.MaxHeight})
			}
		}
	}

	if len(discrete) == 0 {
		return sizes[0].MaxWidth, sizes[0].MaxHeight
	}

	if desiredWidth == 0 && desiredHeight == 0 {
		return discrete[0].width, discrete[0].height
	}

	best := discrete[0]
	bestScore := math.MaxFloat64
	for _, option := range discrete {
		score := math.Abs(float64(int(option.width)-int(desiredWidth))) + math.Abs(float64(int(option.height)-int(desiredHeight)))
		if score < bestScore {
			best = option
			bestScore = score
		}
	}
	return best.width, best.height
}
