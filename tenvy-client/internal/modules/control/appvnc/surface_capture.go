package appvnc

import (
	"context"
	"errors"
	"image"

	"github.com/rootbay/tenvy-client/internal/modules/control/screen"
)

type screenshotSurfaceCapturer struct {
	bounds image.Rectangle
}

func captureRect(ctx context.Context, bounds image.Rectangle) (*surfaceFrame, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	img, err := screen.SafeCaptureRect(bounds)
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, errors.New("nil capture result")
	}
	frame := &surfaceFrame{
		image: &surfaceImage{
			width:  img.Rect.Dx(),
			height: img.Rect.Dy(),
			stride: img.Stride,
			data:   append([]byte(nil), img.Pix...),
		},
	}
	return frame, nil
}

func (c *screenshotSurfaceCapturer) Capture(ctx context.Context) (*surfaceFrame, error) {
	return captureRect(ctx, c.bounds)
}

func (c *screenshotSurfaceCapturer) Close() error {
	return nil
}
