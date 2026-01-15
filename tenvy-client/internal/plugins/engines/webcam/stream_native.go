//go:build windows || darwin

package webcamengine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type nativeFrameConfigurator struct {
	defaultMimeType    string
	defaultPixelFormat string
	fallbackFrameRate  float64
	frameGenerator     func(deviceID string) func() []byte
}

type nativeFrameSource struct {
	deviceID          string
	mimeType          string
	pixelFormat       string
	fallbackFrameRate float64

	mu            sync.Mutex
	started       bool
	frameInterval time.Duration
	updateCh      chan time.Duration
	generator     func() []byte
}

func newNativeFrameSource(deviceID string, settings *protocol.WebcamStreamSettings, cfg nativeFrameConfigurator) (*nativeFrameSource, error) {
	trimmed := strings.TrimSpace(deviceID)
	if trimmed == "" {
		return nil, errors.New("webcam device identifier is required")
	}

	generatorFactory := cfg.frameGenerator
	if generatorFactory == nil {
		generatorFactory = defaultNativeFrameGenerator
	}

	source := &nativeFrameSource{
		deviceID:          trimmed,
		mimeType:          cfg.defaultMimeType,
		pixelFormat:       cfg.defaultPixelFormat,
		fallbackFrameRate: cfg.fallbackFrameRate,
		generator:         generatorFactory(trimmed),
	}

	if source.fallbackFrameRate <= 0 {
		source.fallbackFrameRate = 30
	}

	source.frameInterval = computeFrameInterval(settings, source.fallbackFrameRate)

	if settings != nil {
		if mime := strings.TrimSpace(settings.MimeType); mime != "" {
			source.mimeType = mime
		}
		if format := strings.TrimSpace(settings.PixelFormat); format != "" {
			source.pixelFormat = format
		}
	}

	return source, nil
}

func (s *nativeFrameSource) Start(ctx context.Context) (<-chan framePacket, error) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil, errors.New("webcam capture already started")
	}
	interval := s.frameInterval
	if interval <= 0 {
		interval = time.Second / 30
	}
	updateCh := s.ensureUpdateChannelLocked()
	mimeType := s.mimeType
	generator := s.generator
	s.started = true
	s.mu.Unlock()

	frames := make(chan framePacket)
	go func() {
		defer close(frames)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case newInterval, ok := <-updateCh:
				if !ok {
					return
				}
				if newInterval <= 0 {
					newInterval = time.Second / 30
				}
				ticker.Stop()
				ticker = time.NewTicker(newInterval)
			case <-ticker.C:
				data := generator()
				if len(data) == 0 {
					data = []byte{0}
				}
				packet := framePacket{
					Data:       append([]byte(nil), data...),
					MimeType:   mimeType,
					CapturedAt: time.Now(),
				}
				select {
				case frames <- packet:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return frames, nil
}

func (s *nativeFrameSource) ApplySettings(settings *protocol.WebcamStreamSettings) error {
	if settings == nil {
		return nil
	}

	interval := computeFrameInterval(settings, s.fallbackFrameRate)

	s.mu.Lock()
	s.frameInterval = interval
	if mime := strings.TrimSpace(settings.MimeType); mime != "" {
		s.mimeType = mime
	}
	if format := strings.TrimSpace(settings.PixelFormat); format != "" {
		s.pixelFormat = format
	}
	started := s.started
	updateCh := s.updateCh
	s.mu.Unlock()

	if started && updateCh != nil {
		select {
		case updateCh <- interval:
		default:
		}
	}
	return nil
}

func (s *nativeFrameSource) Close() error {
	s.mu.Lock()
	if s.updateCh != nil {
		close(s.updateCh)
		s.updateCh = nil
	}
	s.started = false
	s.mu.Unlock()
	return nil
}

func (s *nativeFrameSource) ensureUpdateChannelLocked() chan time.Duration {
	if s.updateCh == nil {
		s.updateCh = make(chan time.Duration, 1)
	}
	return s.updateCh
}

func computeFrameInterval(settings *protocol.WebcamStreamSettings, fallback float64) time.Duration {
	fps := fallback
	if settings != nil && settings.FrameRate > 0 {
		fps = settings.FrameRate
	}
	if fps <= 0 {
		fps = 30
	}
	if fps <= 0 {
		return time.Second / 30
	}
	interval := time.Duration(float64(time.Second) / fps)
	if interval <= 0 {
		interval = time.Second / 30
	}
	return interval
}

func defaultNativeFrameGenerator(deviceID string) func() []byte {
	return func() []byte {
		payload := fmt.Sprintf("frame:%s:%d", deviceID, time.Now().UnixNano())
		return []byte(payload)
	}
}
