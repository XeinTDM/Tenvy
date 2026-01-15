//go:build linux || freebsd

package webcamengine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blackjack/webcam"
	"github.com/rootbay/tenvy-client/internal/protocol"
)

var (
	v4l2Glob = filepath.Glob
	v4l2Stat = os.Stat
)

func captureV4L2WebcamInventory() ([]protocol.WebcamDevice, string, error) {
	matches, err := v4l2Glob("/dev/video*")
	if err != nil {
		return nil, "", err
	}
	sort.Strings(matches)

	devices := make([]protocol.WebcamDevice, 0, len(matches))
	warnings := make([]string, 0)

	for _, path := range matches {
		info, err := v4l2Stat(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if info.Mode()&fs.ModeDevice == 0 {
			continue
		}

		cam, err := openV4L2Device(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
			continue
		}

		label := strings.TrimSpace(getCameraName(cam))
		if label == "" {
			label = filepath.Base(path)
		}

		resolutions, frameRates := enumerateCapabilities(cam)

		device := protocol.WebcamDevice{
			ID:    path,
			Label: label,
		}
		if len(resolutions) > 0 || len(frameRates) > 0 {
			device.Capabilities = &protocol.WebcamDeviceCapabilities{}
			if len(resolutions) > 0 {
				device.Capabilities.Resolutions = resolutions
			}
			if len(frameRates) > 0 {
				device.Capabilities.FrameRates = frameRates
			}
		}

		devices = append(devices, device)

		cam.Close()
	}

	sort.SliceStable(devices, func(i, j int) bool {
		if devices[i].Label == devices[j].Label {
			return devices[i].ID < devices[j].ID
		}
		return devices[i].Label < devices[j].Label
	})

	warning := strings.Join(warnings, "; ")
	return devices, warning, nil
}

func enumerateCapabilities(cam v4l2Device) ([]protocol.WebcamResolution, []float64) {
	if cam == nil {
		return nil, nil
	}

	formats := cam.GetSupportedFormats()
	resolutionSet := make(map[string]protocol.WebcamResolution)
	frameRateSet := make(map[float64]struct{})

	for format := range formats {
		sizes := cam.GetSupportedFrameSizes(format)
		for _, size := range sizes {
			addFrameSize(resolutionSet, size)
			if size.StepWidth == 0 && size.StepHeight == 0 {
				rates := cam.GetSupportedFramerates(format, size.MaxWidth, size.MaxHeight)
				collectFrameRates(frameRateSet, rates)
			}
		}
	}

	resolutions := make([]protocol.WebcamResolution, 0, len(resolutionSet))
	for _, res := range resolutionSet {
		resolutions = append(resolutions, res)
	}
	sort.Slice(resolutions, func(i, j int) bool {
		if resolutions[i].Width == resolutions[j].Width {
			return resolutions[i].Height < resolutions[j].Height
		}
		return resolutions[i].Width < resolutions[j].Width
	})

	frameRates := make([]float64, 0, len(frameRateSet))
	for rate := range frameRateSet {
		frameRates = append(frameRates, rate)
	}
	sort.Float64s(frameRates)

	return resolutions, frameRates
}

func addFrameSize(set map[string]protocol.WebcamResolution, size webcam.FrameSize) {
	if size.StepWidth == 0 && size.StepHeight == 0 {
		key := fmt.Sprintf("%dx%d", size.MaxWidth, size.MaxHeight)
		set[key] = protocol.WebcamResolution{Width: int(size.MaxWidth), Height: int(size.MaxHeight)}
		return
	}

	if size.MinWidth > 0 && size.MinHeight > 0 {
		key := fmt.Sprintf("%dx%d", size.MinWidth, size.MinHeight)
		set[key] = protocol.WebcamResolution{Width: int(size.MinWidth), Height: int(size.MinHeight)}
	}
	if size.MaxWidth > 0 && size.MaxHeight > 0 {
		key := fmt.Sprintf("%dx%d", size.MaxWidth, size.MaxHeight)
		set[key] = protocol.WebcamResolution{Width: int(size.MaxWidth), Height: int(size.MaxHeight)}
	}
}

func collectFrameRates(set map[float64]struct{}, rates []webcam.FrameRate) {
	for _, rate := range rates {
		if rate.MinNumerator == 0 || rate.MinDenominator == 0 {
			continue
		}
		if rate.StepNumerator == 0 && rate.StepDenominator == 0 {
			fps := float64(rate.MaxDenominator) / float64(rate.MinNumerator)
			if fps > 0 {
				set[fps] = struct{}{}
			}
			continue
		}

		minFps := float64(rate.MinDenominator) / float64(rate.MaxNumerator)
		maxFps := float64(rate.MaxDenominator) / float64(rate.MinNumerator)
		if minFps > 0 {
			set[minFps] = struct{}{}
		}
		if maxFps > 0 {
			set[maxFps] = struct{}{}
		}
	}
}

func getCameraName(cam v4l2Device) string {
	if cam == nil {
		return ""
	}
	if named, ok := cam.(interface{ GetName() (string, error) }); ok {
		name, err := named.GetName()
		if err == nil {
			return strings.TrimSpace(name)
		}
	}
	return ""
}
