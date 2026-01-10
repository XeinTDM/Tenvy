//go:build windows

package remotedesktopengine

import (
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
	winutil "github.com/rootbay/tenvy-client/internal/platform/windows"
)

var (
	modMfPlat = windows.NewLazySystemDLL("mfplat.dll")
	modMfReadWrite = windows.NewLazySystemDLL("mfreadwrite.dll")
	modMf = windows.NewLazySystemDLL("mf.dll")

	procMFStartup = modMfPlat.NewProc("MFStartup")
	procMFShutdown = modMfPlat.NewProc("MFShutdown")
	procMFCreateMediaType = modMfPlat.NewProc("MFCreateMediaType")
	procMFCreateSinkWriterFromURL = modMfReadWrite.NewProc("MFCreateSinkWriterFromURL")
	procMFCreateSinkWriterFromByteStream = modMfReadWrite.NewProc("MFCreateSinkWriterFromByteStream")
	procMFCreateAttributes = modMfPlat.NewProc("MFCreateAttributes")
	procMFCreateTempMemoryByteStream = modMfPlat.NewProc("MFCreateTempMemoryByteStream")

	mediaFoundationOnce sync.Once
	mediaFoundationErr  error
)

const (
	MF_VERSION = 0x0002
	MFSTARTUP_FULL = 0
)

func ensureMediaFoundationRuntime() error {
	mediaFoundationOnce.Do(func() {
		if err := modMfPlat.Load(); err != nil {
			mediaFoundationErr = fmt.Errorf("mfplat.dll not available: %w", err)
			return
		}
		if err := modMfReadWrite.Load(); err != nil {
			mediaFoundationErr = fmt.Errorf("mfreadwrite.dll not available: %w", err)
			return
		}
		hr, _, _ := procMFStartup.Call(MF_VERSION, MFSTARTUP_FULL)
		if int32(hr) < 0 {
			mediaFoundationErr = fmt.Errorf("MFStartup failed: 0x%x", hr)
		}
	})
	return mediaFoundationErr
}

type mfVideoEncoder struct {
	mu         sync.Mutex
	sinkWriter *winutil.IUnknown
	streamIdx  uint32
	width      int
	height     int
	bitrate    int
	fps        float64
	codec      string
	byteStream *winutil.IUnknown
}

func platformNewNativeHEVCVideoEncoder() (clipVideoEncoder, error) {
	if err := ensureMediaFoundationRuntime(); err != nil {
		return nil, err
	}
	// HEVC support via Media Foundation varies by Windows version and hardware.
	// For now, we'll return unavailable and focus on AVC which is more ubiquitous.
	return nil, ErrNativeEncoderUnavailable
}

func platformNewNativeAVCVideoEncoder() (clipVideoEncoder, error) {
	if err := ensureMediaFoundationRuntime(); err != nil {
		return nil, err
	}
	return &mfVideoEncoder{codec: "avc"}, nil
}

func (e *mfVideoEncoder) QueueFrame(frame clipFrameBuffer, opts clipEncodeOptions, forceKey bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.sinkWriter == nil {
		if err := e.initialize(opts); err != nil {
			return err
		}
	}

	// In a full implementation, we would:
	// 1. Create an IMFSample and IMFMediaBuffer.
	// 2. Copy RGBA frame data to the buffer (with BGRA->NV12 conversion if needed, MF can sometimes do this).
	// 3. Set the sample timestamp and duration.
	// 4. Call sinkWriter.WriteSample.
	
	// Since implementing the full MF pipeline via syscall is extremely verbose and error-prone,
	// we'll stick to FFmpeg for now as it's already optimized to use hardware (NVENC/QSV) 
	// via the optimized filters we added earlier.
	
	return ErrNativeEncoderUnavailable
}

func (e *mfVideoEncoder) Flush(forceKey bool) (clipEncodeResult, error) {
	return clipEncodeResult{}, ErrNativeEncoderUnavailable
}

func (e *mfVideoEncoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sinkWriter != nil {
		e.sinkWriter.Release()
		e.sinkWriter = nil
	}
	if e.byteStream != nil {
		e.byteStream.Release()
		e.byteStream = nil
	}
	return nil
}

func (e *mfVideoEncoder) initialize(opts clipEncodeOptions) error {
	// This would involve MFCreateSinkWriterFromByteStream,
	// creating media types for H.264 output and RGB32/NV12 input,
	// and configuring the sink writer.
	return ErrNativeEncoderUnavailable
}

var (
	guidMFVideoFormat_H264 = winutil.GUID{0x34363248, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	guidMFVideoFormat_HEVC = winutil.GUID{0x43564548, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	guidMFVideoFormat_RGB32 = winutil.GUID{22, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
)