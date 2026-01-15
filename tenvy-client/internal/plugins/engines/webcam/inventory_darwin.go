//go:build darwin

package webcamengine

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type spCameraReport struct {
	Entries []map[string]any `json:"SPCameraDataType"`
}

func platformCaptureWebcamInventory() ([]protocol.WebcamDevice, string, error) {
	cmd := exec.Command("/usr/sbin/system_profiler", "-json", "SPCameraDataType")
	output, err := cmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("system_profiler: %w", err)
	}

	var report spCameraReport
	if err := json.Unmarshal(output, &report); err != nil {
		return nil, "", fmt.Errorf("parse system_profiler output: %w", err)
	}

	devices := make([]protocol.WebcamDevice, 0, len(report.Entries))
	for idx, entry := range report.Entries {
		label := valueAsString(entry["_name"])
		if label == "" {
			label = fmt.Sprintf("Camera %d", idx+1)
		}
		id := valueAsString(entry["unique_id"])
		if id == "" {
			id = valueAsString(entry["camera_unique_id"])
		}
		if id == "" {
			id = strings.ReplaceAll(strings.ToLower(label), " ", "-")
			if id == "" {
				id = fmt.Sprintf("camera-%d", idx+1)
			}
		}

		devices = append(devices, protocol.WebcamDevice{ID: id, Label: label})
	}

	sort.SliceStable(devices, func(i, j int) bool {
		if devices[i].Label == devices[j].Label {
			return devices[i].ID < devices[j].ID
		}
		return devices[i].Label < devices[j].Label
	})

	warning := ""
	if len(devices) == 0 {
		warning = "no webcams detected"
	}

	return devices, warning, nil
}

func valueAsString(input any) string {
	switch v := input.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}
