package screen

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"sync"
)

var (
	pngEncoder      = png.Encoder{CompressionLevel: png.BestSpeed}
	imageBufferPool = sync.Pool{New: func() interface{} { return new(bytes.Buffer) }}
)

func EncodeRGBAAsPNG(width, height int, data []byte) ([]byte, error) {
	if len(data) == 0 || width <= 0 || height <= 0 {
		return nil, errors.New("invalid frame data")
	}

	img := &image.RGBA{
		Pix:    data,
		Stride: width * 4,
		Rect:   image.Rect(0, 0, width, height),
	}
	bufPtr := imageBufferPool.Get().(*bytes.Buffer)
	bufPtr.Reset()
	defer imageBufferPool.Put(bufPtr)

	if err := pngEncoder.Encode(bufPtr, img); err != nil {
		return nil, err
	}
	return append([]byte(nil), bufPtr.Bytes()...), nil
}

func EncodeRGBAAsJPEG(width, height, quality int, data []byte) ([]byte, error) {
	if len(data) == 0 || width <= 0 || height <= 0 {
		return nil, errors.New("invalid frame data")
	}

	if quality <= 0 {
		quality = jpeg.DefaultQuality
	}
	img := &image.RGBA{
		Pix:    data,
		Stride: width * 4,
		Rect:   image.Rect(0, 0, width, height),
	}
	bufPtr := imageBufferPool.Get().(*bytes.Buffer)
	bufPtr.Reset()
	defer imageBufferPool.Put(bufPtr)

	if err := jpeg.Encode(bufPtr, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return append([]byte(nil), bufPtr.Bytes()...), nil
}
