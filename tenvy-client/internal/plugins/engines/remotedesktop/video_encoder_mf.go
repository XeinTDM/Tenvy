//go:build windows

package remotedesktopengine

import (
	"fmt"
	"sync"
	"unsafe"

	winutil "github.com/rootbay/tenvy-client/internal/platform/windows"
	"golang.org/x/sys/windows"
)

var (
	modMfPlat      = windows.NewLazySystemDLL("mfplat.dll")
	modMfReadWrite = windows.NewLazySystemDLL("mfreadwrite.dll")
	modMf          = windows.NewLazySystemDLL("mf.dll")

	procMFStartup                        = modMfPlat.NewProc("MFStartup")
	procMFShutdown                       = modMfPlat.NewProc("MFShutdown")
	procMFCreateMediaType                = modMfPlat.NewProc("MFCreateMediaType")
	procMFCreateSinkWriterFromByteStream = modMfReadWrite.NewProc("MFCreateSinkWriterFromByteStream")
	procMFCreateAttributes               = modMfPlat.NewProc("MFCreateAttributes")
	procMFCreateTempMemoryByteStream     = modMfPlat.NewProc("MFCreateTempMemoryByteStream")
	procMFCreateSample                   = modMfPlat.NewProc("MFCreateSample")
	procMFCreateMemoryBuffer             = modMfPlat.NewProc("MFCreateMemoryBuffer")

	mediaFoundationOnce sync.Once
	mediaFoundationErr  error
)

const (
	MF_VERSION     = 0x0002
	MFSTARTUP_FULL = 0

	MFVideoInterlace_Progressive = 2

	// IMFAttributes methods
	imfAttributesSetUINT32 = 18
	imfAttributesSetUINT64 = 19
	imfAttributesSetGUID   = 21

	// IMFSinkWriter methods
	imfSinkWriterAddStream         = 3
	imfSinkWriterSetInputMediaType = 4
	imfSinkWriterBeginWriting      = 5
	imfSinkWriterWriteSample       = 6
	imfSinkWriterFlush             = 9
	imfSinkWriterFinalize          = 11

	// IMFMediaBuffer methods
	imfMediaBufferLock             = 3
	imfMediaBufferUnlock           = 4
	imfMediaBufferSetCurrentLength = 6

	// IMFSample methods
	imfSampleSetSampleTime     = 33
	imfSampleSetSampleDuration = 35
	imfSampleAddBuffer         = 39

	// IMFByteStream methods
	imfByteStreamGetLength          = 4
	imfByteStreamSetCurrentPosition = 7
	imfByteStreamRead               = 9
)

var (
	guidMFMediaType_Video        = winutil.GUID{Data1: 0x73646976, Data2: 0x0000, Data3: 0x0010, Data4: [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	guidMFVideoFormat_H264       = winutil.GUID{Data1: 0x34363248, Data2: 0x0000, Data3: 0x0010, Data4: [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	guidMFVideoFormat_HEVC       = winutil.GUID{Data1: 0x43564548, Data2: 0x0000, Data3: 0x0010, Data4: [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	guidMFVideoFormat_RGB32      = winutil.GUID{Data1: 22, Data2: 0x0000, Data3: 0x0010, Data4: [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	guidMF_MT_MAJOR_TYPE         = winutil.GUID{Data1: 0x48eba18e, Data2: 0xf8c9, Data3: 0x4684, Data4: [8]byte{0xa1, 0x11, 0x54, 0x4e, 0x9c, 0x13, 0x30, 0x06}}
	guidMF_MT_SUBTYPE            = winutil.GUID{Data1: 0xf7e34c9a, Data2: 0x592d, Data3: 0x4e00, Data4: [8]byte{0x89, 0x50, 0x14, 0x30, 0x00, 0x96, 0x53, 0x0a}}
	guidMF_MT_FRAME_SIZE         = winutil.GUID{Data1: 0x1652c333, Data2: 0xd6ba, Data3: 0x40dd, Data4: [8]byte{0x87, 0x16, 0x72, 0xb1, 0xd0, 0x3c, 0x56, 0x3b}}
	guidMF_MT_FRAME_RATE         = winutil.GUID{Data1: 0xc459a2e8, Data2: 0x052c, Data3: 0x472c, Data4: [8]byte{0x85, 0x04, 0x00, 0xd9, 0xef, 0xc2, 0x3b, 0x25}}
	guidMF_MT_PIXEL_ASPECT_RATIO = winutil.GUID{Data1: 0xc63764b4, Data2: 0xd1d8, Data3: 0x4cf1, Data4: [8]byte{0xaf, 0x1d, 0x2c, 0x1c, 0x20, 0x6b, 0xb2, 0xe0}}
	guidMF_MT_INTERLACE_MODE     = winutil.GUID{Data1: 0xe2724d22, Data2: 0x3402, Data3: 0x4e5b, Data4: [8]byte{0x91, 0x10, 0x33, 0x34, 0x6f, 0x49, 0x10, 0x04}}
	guidMF_MT_AVG_BITRATE        = winutil.GUID{Data1: 0xcf0d924d, Data2: 0x4346, Data3: 0x44a5, Data4: [8]byte{0xb1, 0x4c, 0x47, 0x4c, 0xd2, 0x01, 0x55, 0x10}}
	guidMF_SINK_WRITER_DISABLE_THROTTLING = winutil.GUID{Data1: 0x08b845d8, Data2: 0x2b74, Data3: 0x4afe, Data4: [8]byte{0x9d, 0x53, 0xbe, 0x16, 0xd2, 0xd5, 0xae, 0x4f}}
	guidMF_READWRITE_ENABLE_HARDWARE_TRANSFORMS = winutil.GUID{Data1: 0xa22d4a6a, Data2: 0xd9b8, Data3: 0x477d, Data4: [8]byte{0x8e, 0x43, 0x16, 0x37, 0xc3, 0x55, 0xf7, 0x56}}
	guidMFSampleExtension_CleanPoint      = winutil.GUID{Data1: 0x9cd19179, Data2: 0x821e, Data3: 0x4027, Data4: [8]byte{0xa3, 0x99, 0x53, 0x34, 0xd0, 0x59, 0x2f, 0xc0}}
)


func packSize(w, h int) uint64 {
	return (uint64(w) << 32) | uint64(h)
}

func packRatio(n, d int) uint64 {
	return (uint64(n) << 32) | uint64(d)
}

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
	frameCount int64
}

func platformNewNativeHEVCVideoEncoder() (clipVideoEncoder, error) {
	if err := ensureMediaFoundationRuntime(); err != nil {
		return nil, err
	}
	return &mfVideoEncoder{codec: "hevc"}, nil
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

	var buffer *winutil.IUnknown
	cbBuffer := uint32(e.width * e.height * 4)
	hr, _, _ := procMFCreateMemoryBuffer.Call(uintptr(cbBuffer), uintptr(unsafe.Pointer(&buffer)))
	if int32(hr) < 0 {
		return fmt.Errorf("MFCreateMemoryBuffer failed: 0x%x", hr)
	}
	defer buffer.Release()

	var pData uintptr
	hr, _, _ = buffer.Call(imfMediaBufferLock, uintptr(unsafe.Pointer(&pData)), 0, 0)
	if int32(hr) < 0 {
		return fmt.Errorf("IMFMediaBuffer::Lock failed: 0x%x", hr)
	}
	dest := unsafe.Slice((*byte)(unsafe.Pointer(pData)), int(cbBuffer))
	copy(dest, frame.Buffer[:cbBuffer])
	buffer.Call(imfMediaBufferUnlock)
	buffer.Call(imfMediaBufferSetCurrentLength, uintptr(cbBuffer))

	var sample *winutil.IUnknown
	hr, _, _ = procMFCreateSample.Call(uintptr(unsafe.Pointer(&sample)))
	if int32(hr) < 0 {
		return fmt.Errorf("MFCreateSample failed: 0x%x", hr)
	}
	defer sample.Release()

	sample.Call(imfSampleAddBuffer, uintptr(unsafe.Pointer(buffer)))

	duration := int64(10000000 / e.fps)
	timestamp := e.frameCount * duration
	sample.Call(imfSampleSetSampleTime, uintptr(timestamp))
	sample.Call(imfSampleSetSampleDuration, uintptr(duration))

	if forceKey {
		sample.Call(imfAttributesSetUINT32, uintptr(unsafe.Pointer(&guidMFSampleExtension_CleanPoint)), 1)
	}

	hr, _, _ = e.sinkWriter.Call(imfSinkWriterWriteSample, uintptr(e.streamIdx), uintptr(unsafe.Pointer(sample)))
	if int32(hr) < 0 {
		return fmt.Errorf("IMFSinkWriter::WriteSample failed: 0x%x", hr)
	}

	e.frameCount++
	return nil
}

func (e *mfVideoEncoder) Flush(forceKey bool) (clipEncodeResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.sinkWriter == nil {
		return clipEncodeResult{}, nil
	}

	hr, _, _ := e.sinkWriter.Call(imfSinkWriterFinalize)
	if int32(hr) < 0 {
		return clipEncodeResult{}, fmt.Errorf("IMFSinkWriter::Finalize failed: 0x%x", hr)
	}

	var length uint64
	e.byteStream.Call(imfByteStreamGetLength, uintptr(unsafe.Pointer(&length)))
	e.byteStream.Call(imfByteStreamSetCurrentPosition, 0)

	data := make([]byte, length)
	var cbRead uint32
	hr, _, _ = e.byteStream.Call(imfByteStreamRead, uintptr(unsafe.Pointer(&data[0])), uintptr(length), uintptr(unsafe.Pointer(&cbRead)))
	if int32(hr) < 0 {
		return clipEncodeResult{}, fmt.Errorf("IMFByteStream::Read failed: 0x%x", hr)
	}

	e.sinkWriter.Release()
	e.sinkWriter = nil
	e.byteStream.Release()
	e.byteStream = nil

	encoding := remoteClipEncodingH264
	if e.codec == "hevc" {
		encoding = remoteClipEncodingHEVC
	}

	result := clipEncodeResult{
		Frames: []RemoteDesktopClipFrame{{
			OffsetMs: 0,
			Width:    e.width,
			Height:   e.height,
			Encoding: encoding,
			Data:     data,
		}},
		Bytes:       len(data),
		Encoding:    encoding,
		EncoderName: "MediaFoundation",
	}

	return result, nil
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
	e.width = opts.Width
	e.height = opts.Height
	e.fps = estimateClipFPSFromInterval(opts.FrameInterval)
	if e.fps == 0 {
		e.fps = 30
	}
	e.bitrate = estimateClipBitrate(opts.Width, opts.Height, opts.Quality, opts.TargetBitrate) * 1000
	e.frameCount = 0

	hr, _, _ := procMFCreateTempMemoryByteStream.Call(uintptr(unsafe.Pointer(&e.byteStream)))
	if int32(hr) < 0 {
		return fmt.Errorf("MFCreateTempMemoryByteStream failed: 0x%x", hr)
	}

	var attrs *winutil.IUnknown
	hr, _, _ = procMFCreateAttributes.Call(uintptr(unsafe.Pointer(&attrs)), 1)
	if int32(hr) < 0 {
		return fmt.Errorf("MFCreateAttributes failed: 0x%x", hr)
	}
	defer attrs.Release()
	attrs.Call(imfAttributesSetUINT32, uintptr(unsafe.Pointer(&guidMF_SINK_WRITER_DISABLE_THROTTLING)), 1)
	attrs.Call(imfAttributesSetUINT32, uintptr(unsafe.Pointer(&guidMF_READWRITE_ENABLE_HARDWARE_TRANSFORMS)), 1)

	hr, _, _ = procMFCreateSinkWriterFromByteStream.Call(uintptr(unsafe.Pointer(e.byteStream)), uintptr(unsafe.Pointer(attrs)), uintptr(unsafe.Pointer(&e.sinkWriter)))
	if int32(hr) < 0 {
		return fmt.Errorf("MFCreateSinkWriterFromByteStream failed: 0x%x", hr)
	}

	var outType *winutil.IUnknown
	procMFCreateMediaType.Call(uintptr(unsafe.Pointer(&outType)))
	defer outType.Release()

	subtype := guidMFVideoFormat_H264
	if e.codec == "hevc" {
		subtype = guidMFVideoFormat_HEVC
	}

	outType.Call(imfAttributesSetGUID, uintptr(unsafe.Pointer(&guidMF_MT_MAJOR_TYPE)), uintptr(unsafe.Pointer(&guidMFMediaType_Video)))
	outType.Call(imfAttributesSetGUID, uintptr(unsafe.Pointer(&guidMF_MT_SUBTYPE)), uintptr(unsafe.Pointer(&subtype)))
	outType.Call(imfAttributesSetUINT64, uintptr(unsafe.Pointer(&guidMF_MT_FRAME_SIZE)), uintptr(packSize(e.width, e.height)))
	outType.Call(imfAttributesSetUINT64, uintptr(unsafe.Pointer(&guidMF_MT_FRAME_RATE)), uintptr(packRatio(int(e.fps), 1)))
	outType.Call(imfAttributesSetUINT64, uintptr(unsafe.Pointer(&guidMF_MT_PIXEL_ASPECT_RATIO)), uintptr(packRatio(1, 1)))
	outType.Call(imfAttributesSetUINT32, uintptr(unsafe.Pointer(&guidMF_MT_INTERLACE_MODE)), MFVideoInterlace_Progressive)
	outType.Call(imfAttributesSetUINT32, uintptr(unsafe.Pointer(&guidMF_MT_AVG_BITRATE)), uintptr(e.bitrate))

	hr, _, _ = e.sinkWriter.Call(imfSinkWriterAddStream, uintptr(unsafe.Pointer(outType)), uintptr(unsafe.Pointer(&e.streamIdx)))
	if int32(hr) < 0 {
		return fmt.Errorf("IMFSinkWriter::AddStream failed: 0x%x", hr)
	}

	var inType *winutil.IUnknown
	procMFCreateMediaType.Call(uintptr(unsafe.Pointer(&inType)))
	defer inType.Release()

	inType.Call(imfAttributesSetGUID, uintptr(unsafe.Pointer(&guidMF_MT_MAJOR_TYPE)), uintptr(unsafe.Pointer(&guidMFMediaType_Video)))
	inType.Call(imfAttributesSetGUID, uintptr(unsafe.Pointer(&guidMF_MT_SUBTYPE)), uintptr(unsafe.Pointer(&guidMFVideoFormat_RGB32)))
	inType.Call(imfAttributesSetUINT64, uintptr(unsafe.Pointer(&guidMF_MT_FRAME_SIZE)), uintptr(packSize(e.width, e.height)))
	inType.Call(imfAttributesSetUINT64, uintptr(unsafe.Pointer(&guidMF_MT_FRAME_RATE)), uintptr(packRatio(int(e.fps), 1)))
	inType.Call(imfAttributesSetUINT64, uintptr(unsafe.Pointer(&guidMF_MT_PIXEL_ASPECT_RATIO)), uintptr(packRatio(1, 1)))
	inType.Call(imfAttributesSetUINT32, uintptr(unsafe.Pointer(&guidMF_MT_INTERLACE_MODE)), MFVideoInterlace_Progressive)

	hr, _, _ = e.sinkWriter.Call(imfSinkWriterSetInputMediaType, uintptr(e.streamIdx), uintptr(unsafe.Pointer(inType)), 0)
	if int32(hr) < 0 {
		return fmt.Errorf("IMFSinkWriter::SetInputMediaType failed: 0x%x", hr)
	}

	hr, _, _ = e.sinkWriter.Call(imfSinkWriterBeginWriting)
	if int32(hr) < 0 {
		return fmt.Errorf("IMFSinkWriter::BeginWriting failed: 0x%x", hr)
	}

		return nil

	}

	