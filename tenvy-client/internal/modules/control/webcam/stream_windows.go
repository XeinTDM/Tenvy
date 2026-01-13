//go:build windows

package webcam

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	winutil "github.com/rootbay/tenvy-client/internal/platform/windows"
	"github.com/rootbay/tenvy-client/internal/protocol"
	"golang.org/x/sys/windows"
)

var (
	modMfReadWrite                          = windows.NewLazySystemDLL("mfreadwrite.dll")
	procMFCreateSourceReaderFromMediaSource = modMfReadWrite.NewProc("MFCreateSourceReaderFromMediaSource")
)

func defaultFrameSourceFactory(deviceID string, settings *protocol.WebcamStreamSettings) (frameSource, error) {
	if err := ensureMFInventoryRuntime(); err != nil {
		return nil, err
	}
	return newMFFrameSource(deviceID, settings)
}

type mfFrameSource struct {
	deviceID string
	mu       sync.Mutex
	started  bool
	reader   *winutil.IUnknown
	settings *protocol.WebcamStreamSettings
	stopCh   chan struct{}
}

func newMFFrameSource(deviceID string, settings *protocol.WebcamStreamSettings) (*mfFrameSource, error) {
	return &mfFrameSource{
		deviceID: deviceID,
		settings: settings,
		stopCh:   make(chan struct{}),
	}, nil
}

func (s *mfFrameSource) Start(ctx context.Context) (<-chan framePacket, error) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil, errors.New("webcam capture already started")
	}

	reader, err := s.createSourceReader()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.reader = reader
	s.started = true
	s.mu.Unlock()

	frames := make(chan framePacket)
	go s.run(ctx, frames)

	return frames, nil
}

func (s *mfFrameSource) createSourceReader() (*winutil.IUnknown, error) {
	var attributes *winutil.IUnknown
	hr, _, _ := procMFCreateAttributes.Call(uintptr(unsafe.Pointer(&attributes)), 1)
	if int32(hr) < 0 {
		return nil, fmt.Errorf("MFCreateAttributes failed: 0x%x", hr)
	}
	defer attributes.Release()

	attributes.Call(21, uintptr(unsafe.Pointer(&guidMF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE)), uintptr(unsafe.Pointer(&guidMF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE_VIDCAP)))
	linkPtr := windows.StringToUTF16Ptr(s.deviceID)
	attributes.Call(23, uintptr(unsafe.Pointer(&guidMF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE_VIDCAP_SYMBOLIC_LINK)), uintptr(unsafe.Pointer(linkPtr)))

	var source *winutil.IUnknown
	hr, _, _ = modMfPlat.NewProc("MFCreateDeviceSource").Call(uintptr(unsafe.Pointer(attributes)), uintptr(unsafe.Pointer(&source)))
	if int32(hr) < 0 {
		return nil, fmt.Errorf("MFCreateDeviceSource failed: 0x%x", hr)
	}
	defer source.Release()

	var reader *winutil.IUnknown
	hr, _, _ = procMFCreateSourceReaderFromMediaSource.Call(uintptr(unsafe.Pointer(source)), 0, uintptr(unsafe.Pointer(&reader)))
	if int32(hr) < 0 {
		return nil, fmt.Errorf("MFCreateSourceReaderFromMediaSource failed: 0x%x", hr)
	}

	var mt *winutil.IUnknown
	procMFCreateMediaType.Call(uintptr(unsafe.Pointer(&mt)))
	defer mt.Release()

	mt.Call(21, uintptr(unsafe.Pointer(&guidMF_MT_MAJOR_TYPE)), uintptr(unsafe.Pointer(&guidMFMediaType_Video)))
	mt.Call(21, uintptr(unsafe.Pointer(&guidMF_MT_SUBTYPE)), uintptr(unsafe.Pointer(&guidMFVideoFormat_RGB32)))

	if s.settings != nil {
		if s.settings.Width > 0 && s.settings.Height > 0 {
			packedSize := (uint64(s.settings.Width) << 32) | uint64(s.settings.Height)
			mt.Call(21, uintptr(unsafe.Pointer(&guidMF_MT_FRAME_SIZE)), uintptr(packedSize))
		}

		if s.settings.FrameRate > 0 {
			fps := s.settings.FrameRate
			numerator := uint32(fps * 1000)
			denominator := uint32(1000)
			packedRate := (uint64(numerator) << 32) | uint64(denominator)
			mt.Call(21, uintptr(unsafe.Pointer(&guidMF_MT_FRAME_RATE)), uintptr(packedRate))
		}
	}

	// SetCurrentMediaType
	hr, _, _ = reader.Call(imfSourceReaderSetCurrentMediaType, uintptr(mfSourceReaderFirstVideoStream), 0, uintptr(unsafe.Pointer(mt)))

	return reader, nil
}

func (s *mfFrameSource) run(ctx context.Context, frames chan<- framePacket) {
	defer func() {
		s.mu.Lock()
		if s.reader != nil {
			s.reader.Release()
			s.reader = nil
		}
		s.started = false
		s.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		default:
		}

		var streamIdx uint32
		var streamFlags uint32
		var timestamp int64
		var sample *winutil.IUnknown

		// ReadSample
		hr, _, _ := s.reader.Call(imfSourceReaderReadSample, uintptr(mfSourceReaderFirstVideoStream), 0, uintptr(unsafe.Pointer(&streamIdx)), uintptr(unsafe.Pointer(&streamFlags)), uintptr(unsafe.Pointer(&timestamp)), uintptr(unsafe.Pointer(&sample)))
		if int32(hr) < 0 {
			select {
			case frames <- framePacket{Err: fmt.Errorf("ReadSample failed: 0x%x", hr)}:
			case <-ctx.Done():
			}
			return
		}

		if sample != nil {
			data, err := s.extractBuffer(sample)
			sample.Release()
			if err == nil && len(data) > 0 {
				select {
				case frames <- framePacket{
					Data:       data,
					MimeType:   "image/raw", // Since we requested RGB32
					CapturedAt: time.Now(),
				}:
				case <-ctx.Done():
					return
				}
			}
		}

		if streamFlags&mfSourceReaderFlagEndOfStream != 0 {
			return
		}
	}
}

func (s *mfFrameSource) extractBuffer(sample *winutil.IUnknown) ([]byte, error) {
	var buffer *winutil.IUnknown
	// GetConvertToContiguousBuffer
	hr, _, _ := sample.Call(imfSampleConvertToContiguousBuffer, uintptr(unsafe.Pointer(&buffer)))
	if int32(hr) < 0 {
		return nil, fmt.Errorf("ConvertToContiguousBuffer failed: 0x%x", hr)
	}
	defer buffer.Release()

	var pData *byte
	var cbLen uint32
	hr, _, _ = buffer.Call(imfMediaBufferLock,
		uintptr(unsafe.Pointer(&pData)), 0, uintptr(unsafe.Pointer(&cbLen)))

	if int32(hr) < 0 {
		return nil, fmt.Errorf("IMFMediaBuffer::Lock failed: 0x%x", hr)
	}
	defer buffer.Call(imfMediaBufferUnlock)

	if cbLen == 0 || pData == nil {
		return nil, nil
	}

	data := make([]byte, cbLen)
	copy(data, unsafe.Slice(pData, cbLen))
	return data, nil
}

func (s *mfFrameSource) ApplySettings(settings *protocol.WebcamStreamSettings) error {
	if settings == nil {
		return nil
	}

	s.mu.Lock()
	started := s.started
	reader := s.reader
	s.settings = settings
	s.mu.Unlock()

	if reader == nil {
		return nil
	}

	if started {
		s.mu.Lock()
		if s.reader != nil {
			s.reader.Release()
			s.reader = nil
		}
		s.started = false
		s.mu.Unlock()
	}

	newReader, err := s.createSourceReader()
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.reader = newReader
	if started {
		s.started = true
	}
	s.mu.Unlock()

	return nil
}

func (s *mfFrameSource) Close() error {
	close(s.stopCh)
	return nil
}

const (
	mfSourceReaderFirstVideoStream     = 0xFFFFFFFC
	mfSourceReaderFlagEndOfStream      = 0x00000002
	imfSourceReaderSetCurrentMediaType = 7
	imfSourceReaderReadSample          = 9
	imfSampleConvertToContiguousBuffer = 38
	imfMediaBufferLock                 = 3
	imfMediaBufferUnlock               = 4
)
