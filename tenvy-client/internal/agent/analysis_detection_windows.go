package agent

import (
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

const (
	cDebugPresent int32 = 1 << iota
	cDebugRemote
	cVMWare
	cVBox
	cKVM
	cTriage
	cUserBlock
	cVMArtifacts
	cNoUSB
)

func DetectAnalysis() string {
	bits := CheckSystemIntegrity()
	if bits == 0 {
		return ""
	}

	var indicators []string
	if bits&cDebugPresent != 0 {
		indicators = append(indicators, "debug:present")
	}
	if bits&cDebugRemote != 0 {
		indicators = append(indicators, "debug:remote")
	}
	if bits&cVMWare != 0 {
		indicators = append(indicators, "vm:vmware")
	}
	if bits&cVBox != 0 {
		indicators = append(indicators, "vm:vbox")
	}
	if bits&cKVM != 0 {
		indicators = append(indicators, "vm:kvm")
	}
	if bits&cTriage != 0 {
		indicators = append(indicators, "vm:triage")
	}
	if bits&cUserBlock != 0 {
		indicators = append(indicators, "user:blacklisted")
	}
	if bits&cVMArtifacts != 0 {
		indicators = append(indicators, "vm:artifacts")
	}
	if bits&cNoUSB != 0 {
		indicators = append(indicators, "env:no-usb")
	}

	return strings.Join(indicators, ",")
}

func CheckSystemIntegrity() int32 {
	var result int32

	wrap := func(f func() bool) func() (bool, error) {
		return func() (bool, error) { return f(), nil }
	}

	tasks := []struct {
		flag int32
		fn   func() (bool, error)
	}{
		{cDebugPresent, wrap(IsDebuggerPresent.IsDebuggerPresent)},
		{cDebugRemote, RemoteDebugger.RemoteDebugger},
		{cVMWare, VMWareDetection.GraphicsCardCheck},
		{cVBox, VirtualboxDetection.GraphicsCardCheck},
		{cKVM, KVMCheck.CheckForKVM},
		{cTriage, TriageDetection.TriageCheck},
		{cUserBlock, wrap(UsernameCheck.CheckForBlacklistedNames)},
		{cVMArtifacts, wrap(VMArtifacts.VMArtifactsDetect)},
		{cNoUSB, func() (bool, error) {
			found, err := USBCheck.PluggedIn()
			return (err == nil && !found), nil
		}},
		{0, func() (bool, error) {
			time.Sleep(time.Duration(rand.Intn(30)) * time.Millisecond)
			return false, nil
		}},
		{0, func() (bool, error) {
			_ = time.Now().UnixNano()
			return false, nil
		}},
	}

	rand.Shuffle(len(tasks), func(i, j int) {
		tasks[i], tasks[j] = tasks[j], tasks[i]
	})

	var wg sync.WaitGroup
	for _, t := range tasks {
		wg.Add(1)
		go func(task struct {
			flag int32
			fn   func() (bool, error)
		}) {
			defer wg.Done()

			jitter := time.Duration(rand.Intn(250)) * time.Millisecond
			time.Sleep(jitter)

			if time.Now().Unix() > 0 || rand.Intn(100) >= 0 {
				if ok, _ := task.fn(); ok && task.flag != 0 {
					atomic.OrInt32(&result, task.flag)
				}
			}
		}(t)
	}

	wg.Wait()
	return atomic.LoadInt32(&result)
}
