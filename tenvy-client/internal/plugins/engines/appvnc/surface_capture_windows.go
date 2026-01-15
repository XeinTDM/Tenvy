//go:build windows

package appvncengine

import (
	"context"
	"errors"
	"image"
	"unsafe"

	"github.com/kbinani/screenshot"
	"github.com/lxn/win"
	winutil "github.com/rootbay/tenvy-client/internal/platform/windows"
)

func defaultSurfaceCaptureFactory(session *sessionState) (surfaceCapturer, error) {
	if session != nil && session.processID != 0 {
		return &windowSurfaceCapturer{pid: uint32(session.processID)}, nil
	}

	displays := screenshot.NumActiveDisplays()
	if displays <= 0 {
		return nil, errors.New("no active displays")
	}
	bounds := screenshot.GetDisplayBounds(0)
	return &screenshotSurfaceCapturer{bounds: bounds}, nil
}

type windowSurfaceCapturer struct {
	pid uint32
}

func (c *windowSurfaceCapturer) Capture(ctx context.Context) (*surfaceFrame, error) {
	hwnd := winutil.FindWindowForPID(c.pid)
	if hwnd == 0 {
		return nil, errors.New("application window not found")
	}

	var rect win.RECT
	if !win.GetWindowRect(hwnd, &rect) {
		return nil, errors.New("failed to get window rect")
	}

	width := rect.Right - rect.Left
	height := rect.Bottom - rect.Top
	if width <= 0 || height <= 0 {
		return nil, errors.New("invalid window dimensions")
	}

	hdcWindow := win.GetDC(hwnd)
	if hdcWindow == 0 {
		return nil, errors.New("failed to get window DC")
	}
	defer win.ReleaseDC(hwnd, hdcWindow)

	hdcMem := win.CreateCompatibleDC(hdcWindow)
	if hdcMem == 0 {
		return nil, errors.New("failed to create compatible DC")
	}
	defer win.DeleteDC(hdcMem)

	hbm := win.CreateCompatibleBitmap(hdcWindow, width, height)
	if hbm == 0 {
		return nil, errors.New("failed to create compatible bitmap")
	}
	defer win.DeleteObject(win.HGDIOBJ(hbm))

	oldObj := win.SelectObject(hdcMem, win.HGDIOBJ(hbm))
	defer win.SelectObject(hdcMem, oldObj)

	if !win.BitBlt(hdcMem, 0, 0, width, height, hdcWindow, 0, 0, win.SRCCOPY) {
		return nil, errors.New("BitBlt failed")
	}

	var bi win.BITMAPINFO
	bi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bi.BmiHeader))
	bi.BmiHeader.BiWidth = width
	bi.BmiHeader.BiHeight = -height
	bi.BmiHeader.BiPlanes = 1
	bi.BmiHeader.BiBitCount = 32
	bi.BmiHeader.BiCompression = win.BI_RGB

	rgba := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	if win.GetDIBits(hdcWindow, hbm, 0, uint32(height), (*byte)(unsafe.Pointer(&rgba.Pix[0])), &bi, win.DIB_RGB_COLORS) == 0 {
		return nil, errors.New("GetDIBits failed")
	}

	frame := &surfaceFrame{
		image: &surfaceImage{
			width:  int(width),
			height: int(height),
			stride: rgba.Stride,
			data:   rgba.Pix,
		},
	}
	return frame, nil
}

func (c *windowSurfaceCapturer) Close() error {
	return nil
}
