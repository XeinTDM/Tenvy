//go:build windows

package screen

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"unsafe"

	winutil "github.com/rootbay/tenvy-client/internal/platform/windows"
	"golang.org/x/sys/windows"
)

var (
	modDXGI  = windows.NewLazySystemDLL("dxgi.dll")
	modD3D11 = windows.NewLazySystemDLL("d3d11.dll")

	procD3D11CreateDevice = modD3D11.NewProc("D3D11CreateDevice")

	dxgiProbeOnce sync.Once
	dxgiProbeErr  error
)

const (
	D3D11_SDK_VERSION       = 7
	DXGI_ERROR_WAIT_TIMEOUT = 0x887A0027
	DXGI_ERROR_ACCESS_LOST  = 0x887A0026
)

type D3D_DRIVER_TYPE int

const (
	D3D_DRIVER_TYPE_HARDWARE D3D_DRIVER_TYPE = 1
)

type D3D_FEATURE_LEVEL int

var featureLevels = []D3D_FEATURE_LEVEL{
	0xb000, // D3D_FEATURE_LEVEL_11_0
	0xa100, // D3D_FEATURE_LEVEL_10_1
	0xa000, // D3D_FEATURE_LEVEL_10_0
}

func defaultPlatformCaptureCandidates() []backendCandidate {
	return []backendCandidate{{name: "dxgi", factory: newDXGICaptureBackend}}
}

func ensureDXGICapable() error {
	dxgiProbeOnce.Do(func() {
		if err := modDXGI.Load(); err != nil {
			dxgiProbeErr = err
			return
		}
		if err := modD3D11.Load(); err != nil {
			dxgiProbeErr = err
			return
		}
	})
	return dxgiProbeErr
}

type dxgiCaptureBackend struct {
	mu           sync.Mutex
	device       *winutil.IUnknown
	context      *winutil.IUnknown
	duplicator   *winutil.IUnknown
	staging      *winutil.IUnknown
	outputBounds image.Rectangle
}

func newDXGICaptureBackend() (captureBackend, error) {
	if err := ensureDXGICapable(); err != nil {
		return nil, err
	}

	b := &dxgiCaptureBackend{}
	if err := b.initialize(); err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

func (b *dxgiCaptureBackend) Name() string {
	return "dxgi"
}

func (b *dxgiCaptureBackend) initialize() error {
	var device, context *winutil.IUnknown
	var featureLevel D3D_FEATURE_LEVEL

	hr, _, _ := procD3D11CreateDevice.Call(
		0,
		uintptr(D3D_DRIVER_TYPE_HARDWARE),
		0,
		0,
		uintptr(unsafe.Pointer(&featureLevels[0])),
		uintptr(len(featureLevels)),
		D3D11_SDK_VERSION,
		uintptr(unsafe.Pointer(&device)),
		uintptr(unsafe.Pointer(&featureLevel)),
		uintptr(unsafe.Pointer(&context)),
	)

	if int32(hr) < 0 {
		return fmt.Errorf("D3D11CreateDevice failed: 0x%x", hr)
	}
	b.device = device
	b.context = context

	var dxgiDevice *winutil.IUnknown
	hr = b.device.QueryInterface(&guidIDXGIDevice, unsafe.Pointer(&dxgiDevice))
	if int32(hr) < 0 {
		return fmt.Errorf("QueryInterface IDXGIDevice failed: 0x%x", hr)
	}
	defer dxgiDevice.Release()

	var adapter *winutil.IUnknown
	hr, _, _ = dxgiDevice.Call(9, uintptr(unsafe.Pointer(&adapter))) // GetAdapter
	if int32(hr) < 0 {
		return fmt.Errorf("GetAdapter failed: 0x%x", hr)
	}
	defer adapter.Release()

	var output *winutil.IUnknown
	hr, _, _ = adapter.Call(7, 0, uintptr(unsafe.Pointer(&output))) // EnumOutputs
	if int32(hr) < 0 {
		return fmt.Errorf("EnumOutputs failed: 0x%x", hr)
	}
	defer output.Release()

	var desc struct {
		DeviceName         [32]uint16
		DesktopCoordinates struct {
			Left, Top, Right, Bottom int32
		}
		AttachedToDesktop int32
		Rotation          int32
		Monitor           uintptr
	}
	hr, _, _ = output.Call(7, uintptr(unsafe.Pointer(&desc))) // GetDesc
	if int32(hr) < 0 {
		return fmt.Errorf("GetDesc failed: 0x%x", hr)
	}
	b.outputBounds = image.Rect(
		int(desc.DesktopCoordinates.Left),
		int(desc.DesktopCoordinates.Top),
		int(desc.DesktopCoordinates.Right),
		int(desc.DesktopCoordinates.Bottom),
	)

	var output1 *winutil.IUnknown
	hr = output.QueryInterface(&guidIDXGIOutput1, unsafe.Pointer(&output1))
	if int32(hr) < 0 {
		return fmt.Errorf("QueryInterface IDXGIOutput1 failed: 0x%x", hr)
	}
	defer output1.Release()

	hr, _, _ = output1.Call(10, uintptr(unsafe.Pointer(b.device)), uintptr(unsafe.Pointer(&b.duplicator))) // DuplicateOutput
	if int32(hr) < 0 {
		return fmt.Errorf("DuplicateOutput failed: 0x%x", hr)
	}

	return nil
}

func (b *dxgiCaptureBackend) Capture(bounds image.Rectangle) (*image.RGBA, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.duplicator == nil {
		if err := b.initialize(); err != nil {
			return nil, err
		}
	}

	var resource *winutil.IUnknown
	var frameInfo struct {
		LastPresentTime           int64
		LastMouseUpdateTime       int64
		AccumulatedFrames         uint32
		RectsCoalesced            int32
		ProtectedContentMaskedOut int32
		PointerPosition           struct {
			Position struct{ x, y int32 }
			Visible  int32
		}
		TotalMetadataBufferSize uint32
		PointerShapeBufferSize  uint32
	}

	hr, _, _ := b.duplicator.Call(7, 0, uintptr(unsafe.Pointer(&frameInfo)), uintptr(unsafe.Pointer(&resource))) // AcquireNextFrame
	if uint32(hr) == DXGI_ERROR_ACCESS_LOST {
		b.reset()
		return nil, errors.New("dxgi access lost")
	}

	if int32(hr) < 0 && uint32(hr) != DXGI_ERROR_WAIT_TIMEOUT {
		return nil, fmt.Errorf("AcquireNextFrame failed: 0x%x", hr)
	}

	if resource != nil {
		defer resource.Release()
		defer b.duplicator.Call(8) // ReleaseFrame

		var texture *winutil.IUnknown
		hr = resource.QueryInterface(&guidID3D11Texture2D, unsafe.Pointer(&texture))
		if int32(hr) < 0 {
			return nil, fmt.Errorf("QueryInterface ID3D11Texture2D failed: 0x%x", hr)
		}
		defer texture.Release()

		img, err := b.copyTextureToRGBA(texture)
		if err != nil {
			return nil, err
		}

		if bounds != b.outputBounds {
			return b.cropImage(img, bounds), nil
		}
		return img, nil
	}

	return nil, errors.New("no frame available from dxgi")
}

func (b *dxgiCaptureBackend) copyTextureToRGBA(texture *winutil.IUnknown) (*image.RGBA, error) {
	var desc struct {
		Width, Height, MipLevels, ArraySize uint32
		Format                              uint32
		SampleDesc                          struct{ Count, Quality uint32 }
		Usage                               int32
		BindFlags                           uint32
		CPUAccessFlags                      uint32
		MiscFlags                           uint32
	}
	texture.Call(9, uintptr(unsafe.Pointer(&desc))) // GetDesc

	if b.staging == nil {
		stagingDesc := desc
		stagingDesc.Usage = 3 // D3D11_USAGE_STAGING
		stagingDesc.BindFlags = 0
		stagingDesc.CPUAccessFlags = 0x20000 // D3D11_CPU_ACCESS_READ
		stagingDesc.MiscFlags = 0

		hr, _, _ := b.device.Call(24, uintptr(unsafe.Pointer(&stagingDesc)), 0, uintptr(unsafe.Pointer(&b.staging))) // CreateTexture2D
		if int32(hr) < 0 {
			return nil, fmt.Errorf("CreateTexture2D staging failed: 0x%x", hr)
		}
	}

	b.context.Call(47, uintptr(unsafe.Pointer(b.staging)), uintptr(unsafe.Pointer(texture))) // CopyResource

	var mapped struct {
		Data       uintptr
		RowPitch   uint32
		DepthPitch uint32
	}
	hr, _, _ := b.context.Call(14, uintptr(unsafe.Pointer(b.staging)), 0, 1, 0, uintptr(unsafe.Pointer(&mapped))) // Map
	if int32(hr) < 0 {
		return nil, fmt.Errorf("Map staging texture failed: 0x%x", hr)
	}
	defer b.context.Call(15, uintptr(unsafe.Pointer(b.staging)), 0) // Unmap

	width := int(desc.Width)
	height := int(desc.Height)
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))

	src := unsafe.Slice((*byte)(unsafe.Pointer(mapped.Data)), int(mapped.RowPitch)*height)
	for y := range height {
		srcRow := src[y*int(mapped.RowPitch) : y*int(mapped.RowPitch)+width*4]
		dstRow := rgba.Pix[y*rgba.Stride : y*rgba.Stride+width*4]
		// B8G8R8A8_UNORM
		for x := range width {
			dstRow[x*4+0] = srcRow[x*4+2] // R
			dstRow[x*4+1] = srcRow[x*4+1] // G
			dstRow[x*4+2] = srcRow[x*4+0] // B
			dstRow[x*4+3] = 255           // A (force opaque)
		}
	}

	return rgba, nil
}

func (b *dxgiCaptureBackend) cropImage(img *image.RGBA, bounds image.Rectangle) *image.RGBA {
	rel := bounds.Sub(b.outputBounds.Min)
	out := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		srcY := rel.Min.Y + y
		if srcY < 0 || srcY >= img.Bounds().Dy() {
			continue
		}
		copy(out.Pix[y*out.Stride:], img.Pix[srcY*img.Stride+rel.Min.X*4:srcY*img.Stride+rel.Max.X*4])
	}
	return out
}

func (b *dxgiCaptureBackend) reset() {
	if b.duplicator != nil {
		b.duplicator.Release()
		b.duplicator = nil
	}
	if b.staging != nil {
		b.staging.Release()
		b.staging = nil
	}
	if b.device != nil {
		b.device.Release()
		b.device = nil
	}
	if b.context != nil {
		b.context.Release()
		b.context = nil
	}
}

func (b *dxgiCaptureBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reset()
	return nil
}

var (
	guidIDXGIDevice     = winutil.GUID{0x54ecf641, 0x11d7, 0x450a, [8]byte{0x8b, 0xa3, 0x1d, 0x15, 0x9e, 0x47, 0xc1, 0xd4}}
	guidIDXGIOutput1    = winutil.GUID{0x00cddea8, 0x939b, 0x4b83, [8]byte{0xa3, 0x43, 0x21, 0x64, 0xc8, 0x5b, 0x00, 0x72}}
	guidID3D11Texture2D = winutil.GUID{0x6f156113, 0xd224, 0x4515, [8]byte{0x88, 0x14, 0x13, 0x01, 0x9b, 0xb0, 0x33, 0x6f}}
)
