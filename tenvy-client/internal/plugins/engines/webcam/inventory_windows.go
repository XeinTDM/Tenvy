//go:build windows

package webcamengine

import (
	"fmt"
	"sort"
	"sync"
	"unsafe"

	winutil "github.com/rootbay/tenvy-client/internal/platform/windows"
	"github.com/rootbay/tenvy-client/internal/protocol"
	"golang.org/x/sys/windows"
)

var (
	modMfPlat = windows.NewLazySystemDLL("mfplat.dll")

	procMFCreateAttributes = modMfPlat.NewProc("MFCreateAttributes")
	procMFEnumDeviceSources = modMfPlat.NewProc("MFEnumDeviceSources")
	procMFCreateMediaType = modMfPlat.NewProc("MFCreateMediaType")

	mfInventoryOnce sync.Once
	mfInventoryErr  error
)

func ensureMFInventoryRuntime() error {
	mfInventoryOnce.Do(func() {
		if err := modMfPlat.Load(); err != nil {
			mfInventoryErr = err
			return
		}
		// MFStartup is called in video_encoder_mf.go if needed, but we should ensure it here too if used standalone.
		// However, MFEnumDeviceSources usually doesn't strictly require MFStartup(MF_VERSION) but it's good practice.
		hr, _, _ := modMfPlat.NewProc("MFStartup").Call(0x0002, 0)
		if int32(hr) < 0 {
			mfInventoryErr = fmt.Errorf("MFStartup failed: 0x%x", hr)
		}
	})
	return mfInventoryErr
}

func platformCaptureWebcamInventory() ([]protocol.WebcamDevice, string, error) {
	if err := ensureMFInventoryRuntime(); err != nil {
		return nil, "", err
	}

	var attributes *winutil.IUnknown
	hr, _, _ := procMFCreateAttributes.Call(uintptr(unsafe.Pointer(&attributes)), 1)
	if int32(hr) < 0 {
		return nil, "", fmt.Errorf("MFCreateAttributes failed: 0x%x", hr)
	}
	defer attributes.Release()

	attributes.Call(21, uintptr(unsafe.Pointer(&guidMF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE)), uintptr(unsafe.Pointer(&guidMF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE_VIDCAP))) // SetGUID

	var activatePtrs **winutil.IUnknown
	var count uint32
	hr, _, _ = procMFEnumDeviceSources.Call(uintptr(unsafe.Pointer(attributes)), uintptr(unsafe.Pointer(&activatePtrs)), uintptr(unsafe.Pointer(&count)))
	if int32(hr) < 0 {
		return nil, "", fmt.Errorf("MFEnumDeviceSources failed: 0x%x", hr)
	}

	if count == 0 {
		return []protocol.WebcamDevice{}, "", nil
	}

	activates := unsafe.Slice(activatePtrs, count)
	defer func() {
		for _, a := range activates {
			a.Release()
		}
		windows.CoTaskMemFree(unsafe.Pointer(activatePtrs))
	}()

	devices := make([]protocol.WebcamDevice, 0, count)
	for _, activate := range activates {
		var namePtr *uint16
		var nameLen uint32
		// GetAllocatedString for Friendly Name
		hr, _, _ = activate.Call(13, uintptr(unsafe.Pointer(&guidMF_DEVSOURCE_ATTRIBUTE_FRIENDLY_NAME)), uintptr(unsafe.Pointer(&namePtr)), uintptr(unsafe.Pointer(&nameLen)))
		friendlyName := ""
		if int32(hr) >= 0 && namePtr != nil {
			friendlyName = windows.UTF16PtrToString(namePtr)
			windows.CoTaskMemFree(unsafe.Pointer(namePtr))
		}

		var linkPtr *uint16
		var linkLen uint32
		// GetAllocatedString for Symbolic Link (ID)
		hr, _, _ = activate.Call(13, uintptr(unsafe.Pointer(&guidMF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE_VIDCAP_SYMBOLIC_LINK)), uintptr(unsafe.Pointer(&linkPtr)), uintptr(unsafe.Pointer(&linkLen)))
		symbolicLink := ""
		if int32(hr) >= 0 && linkPtr != nil {
			symbolicLink = windows.UTF16PtrToString(linkPtr)
			windows.CoTaskMemFree(unsafe.Pointer(linkPtr))
		}

		if symbolicLink == "" {
			continue
		}

		device := protocol.WebcamDevice{
			ID:    symbolicLink,
			Label: friendlyName,
		}
		if device.Label == "" {
			device.Label = "Webcam"
		}

		// Try to get capabilities
		if caps := getDeviceCapabilities(activate); caps != nil {
			device.Capabilities = caps
		}

		devices = append(devices, device)
	}

	sort.SliceStable(devices, func(i, j int) bool {
		if devices[i].Label == devices[j].Label {
			return devices[i].ID < devices[j].ID
		}
		return devices[i].Label < devices[j].Label
	})

	return devices, "", nil
}

func getDeviceCapabilities(activate *winutil.IUnknown) *protocol.WebcamDeviceCapabilities {
	var source *winutil.IUnknown
	// ActivateObject
	hr, _, _ := activate.Call(imfActivateActivateObject, uintptr(unsafe.Pointer(&guidIMFMediaSource)), uintptr(unsafe.Pointer(&source)))
	if int32(hr) < 0 {
		return nil
	}
	defer source.Release()

	var pd *winutil.IUnknown
	// CreatePresentationDescriptor
	hr, _, _ = source.Call(imfMediaSourceCreatePresentationDescriptor, uintptr(unsafe.Pointer(&pd)))
	if int32(hr) < 0 {
		return nil
	}
	defer pd.Release()

	var streamCount uint32
	pd.Call(imfPresentationDescriptorGetStreamDescriptorCount, uintptr(unsafe.Pointer(&streamCount)))

	resolutions := make(map[string]protocol.WebcamResolution)
	frameRates := make(map[float64]struct{})

	for i := uint32(0); i < streamCount; i++ {
		var selected int32
		var sd *winutil.IUnknown
		hr, _, _ = pd.Call(imfPresentationDescriptorGetStreamDescriptorByIndex, uintptr(i), uintptr(unsafe.Pointer(&selected)), uintptr(unsafe.Pointer(&sd)))
		if int32(hr) < 0 {
			continue
		}
		defer sd.Release()

		var handler *winutil.IUnknown
		hr, _, _ = sd.Call(imfStreamDescriptorGetMediaTypeHandler, uintptr(unsafe.Pointer(&handler)))
		if int32(hr) < 0 {
			continue
		}
		defer handler.Release()

		var typeCount uint32
		handler.Call(imfMediaTypeHandlerGetMediaTypeCount, uintptr(unsafe.Pointer(&typeCount)))

		for j := uint32(0); j < typeCount; j++ {
			var mt *winutil.IUnknown
			hr, _, _ = handler.Call(imfMediaTypeHandlerGetMediaTypeByIndex, uintptr(j), uintptr(unsafe.Pointer(&mt)))
			if int32(hr) < 0 {
				continue
			}
			defer mt.Release()

			// MF style packed UINT64 for frame size
			var packedSize uint64
			hr, _, _ = mt.Call(19, uintptr(unsafe.Pointer(&guidMF_MT_FRAME_SIZE)), uintptr(unsafe.Pointer(&packedSize)))
			if int32(hr) >= 0 {
				w := int(packedSize >> 32)
				h := int(packedSize & 0xFFFFFFFF)
				if w > 0 && h > 0 {
					key := fmt.Sprintf("%dx%d", w, h)
					resolutions[key] = protocol.WebcamResolution{Width: w, Height: h}
				}
			}

			var packedRate uint64
			hr, _, _ = mt.Call(19, uintptr(unsafe.Pointer(&guidMF_MT_FRAME_RATE)), uintptr(unsafe.Pointer(&packedRate)))
			if int32(hr) >= 0 {
				num := uint32(packedRate >> 32)
				den := uint32(packedRate & 0xFFFFFFFF)
				if den > 0 {
					fps := float64(num) / float64(den)
					if fps > 0 {
						frameRates[fps] = struct{}{}
					}
				}
			}
		}
	}

	if len(resolutions) == 0 {
		return nil
	}

	resList := make([]protocol.WebcamResolution, 0, len(resolutions))
	for _, r := range resolutions {
		resList = append(resList, r)
	}
	sort.Slice(resList, func(i, j int) bool {
		if resList[i].Width == resList[j].Width {
			return resList[i].Height < resList[j].Height
		}
		return resList[i].Width < resList[j].Width
	})

	fpsList := make([]float64, 0, len(frameRates))
	for f := range frameRates {
		fpsList = append(fpsList, f)
	}
	sort.Float64s(fpsList)

	return &protocol.WebcamDeviceCapabilities{
		Resolutions: resList,
		FrameRates:  fpsList,
	}
}

const (
	imfActivateActivateObject = 3
	imfMediaSourceCreatePresentationDescriptor = 7
	imfPresentationDescriptorGetStreamDescriptorCount = 3
	imfPresentationDescriptorGetStreamDescriptorByIndex = 4
	imfStreamDescriptorGetMediaTypeHandler = 5
	imfMediaTypeHandlerGetMediaTypeCount = 3
	imfMediaTypeHandlerGetMediaTypeByIndex = 4
)
