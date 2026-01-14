package agent

import (
	"strings"

	"github.com/EvilBytecode/GoDefender/AntiDebug/IsDebuggerPresent"
	"github.com/EvilBytecode/GoDefender/AntiDebug/RemoteDebugger"
	"github.com/EvilBytecode/GoDefender/AntiVirtualization/KVMCheck"
	"github.com/EvilBytecode/GoDefender/AntiVirtualization/TriageDetection"
	"github.com/EvilBytecode/GoDefender/AntiVirtualization/USBCheck"
	"github.com/EvilBytecode/GoDefender/AntiVirtualization/UsernameCheck"
	"github.com/EvilBytecode/GoDefender/AntiVirtualization/VMArtifacts"
	"github.com/EvilBytecode/GoDefender/AntiVirtualization/VMWareDetection"
	"github.com/EvilBytecode/GoDefender/AntiVirtualization/VirtualboxDetection"
)

// DetectAnalysis attempts to identify if the agent is running in an analysis or virtualized environment.
func DetectAnalysis() string {
	var indicators []string

	if ok := IsDebuggerPresent.IsDebuggerPresent(); ok {
		indicators = append(indicators, "debug:present")
	}

	if ok, _ := RemoteDebugger.RemoteDebugger(); ok {
		indicators = append(indicators, "debug:remote")
	}

	if ok, _ := VMWareDetection.GraphicsCardCheck(); ok {
		indicators = append(indicators, "vm:vmware")
	}

	if ok, _ := VirtualboxDetection.GraphicsCardCheck(); ok {
		indicators = append(indicators, "vm:vbox")
	}

	if ok, _ := KVMCheck.CheckForKVM(); ok {
		indicators = append(indicators, "vm:kvm")
	}

	if ok, _ := TriageDetection.TriageCheck(); ok {
		indicators = append(indicators, "analysis:triage")
	}

	if ok := UsernameCheck.CheckForBlacklistedNames(); ok {
		indicators = append(indicators, "analysis:user")
	}

	if ok := VMArtifacts.VMArtifactsDetect(); ok {
		indicators = append(indicators, "vm:artifacts")
	}

	if ok, err := USBCheck.PluggedIn(); err == nil && !ok {
		// No USB history usually indicates a fresh VM
		indicators = append(indicators, "vm:no-usb")
	}

	if len(indicators) == 0 {
		return ""
	}

	return strings.Join(indicators, ",")
}
