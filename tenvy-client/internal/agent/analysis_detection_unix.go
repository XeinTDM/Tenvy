//go:build !windows

package agent

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
)

func DetectAnalysis() string {
	var indicators []string

	if mac := getAnalysisMACIndicator(); mac != "" {
		indicators = append(indicators, fmt.Sprintf("mac:%s", mac))
	}
	if file := getAnalysisFileIndicator(); file != "" {
		indicators = append(indicators, fmt.Sprintf("file:%s", file))
	}
	if len(indicators) == 0 {
		return ""
	}

	return strings.Join(indicators, ",")
}

func getAnalysisMACIndicator() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range interfaces {
		mac := strings.ToUpper(iface.HardwareAddr.String())
		if mac == "" {
			continue
		}

		if strings.HasPrefix(mac, "00:05:69") || strings.HasPrefix(mac, "00:0C:29") || strings.HasPrefix(mac, "00:50:56") {
			return "vmware"
		}
		if strings.HasPrefix(mac, "08:00:27") {
			return "vbox"
		}
		if strings.HasPrefix(mac, "00:03:FF") {
			return "hyperv"
		}
		if strings.HasPrefix(mac, "52:54:00") {
			return "qemu"
		}
	}
	return ""
}

func getAnalysisFileIndicator() string {
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/sys/class/dmi/id/product_name"); err == nil {
			data, _ := os.ReadFile("/sys/class/dmi/id/product_name")
			content := strings.ToLower(string(data))
			if strings.Contains(content, "vmware") || strings.Contains(content, "virtualbox") || strings.Contains(content, "qemu") {
				return "dmi-match"
			}
		}
	}
	return ""
}
