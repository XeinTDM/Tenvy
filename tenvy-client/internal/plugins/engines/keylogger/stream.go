package keyloggerengine

import (
	"context"
	"sync"
)

type channelEventStream struct {
	events    chan CaptureEvent
	closeOnce sync.Once
}

func newChannelEventStream(size int) *channelEventStream {
	if size <= 0 {
		size = defaultBufferSize
	}
	return &channelEventStream{events: make(chan CaptureEvent, size)}
}

func (s *channelEventStream) Events() <-chan CaptureEvent {
	return s.events
}

func (s *channelEventStream) Close() error {
	s.closeOnce.Do(func() {
		close(s.events)
	})
	return nil
}

func (s *channelEventStream) emit(ctx context.Context, event CaptureEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case s.events <- event:
		return true
	}
}
