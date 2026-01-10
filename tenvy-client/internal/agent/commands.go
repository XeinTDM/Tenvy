package agent

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	options "github.com/rootbay/tenvy-client/internal/operations/options"
	"github.com/rootbay/tenvy-client/internal/platform"
	"github.com/rootbay/tenvy-client/internal/protocol"
)

type shellExecutionOptions struct {
	workingDirectory string
	environment      map[string]string
}

func (a *Agent) processCommands(ctx context.Context, commands []protocol.Command) {
	for _, cmd := range commands {
		if err := ctx.Err(); err != nil {
			a.enqueueResult(newFailureResult(cmd.ID, "agent shutting down"))
			continue
		}

		if !a.verifyCommandSignature(cmd) {
			a.logger.Printf("REJECTED: command %s (%s) has invalid signature", cmd.ID, cmd.Name)
			a.enqueueResult(newFailureResult(cmd.ID, "invalid command signature"))
			continue
		}

		a.logger.Printf("executing command %s (%s)", cmd.ID, cmd.Name)
		result := a.executeCommand(ctx, cmd)
		a.enqueueResult(result)
	}
}

func (a *Agent) verifyCommandSignature(cmd protocol.Command) bool {
	if a.commandSecret == "" {
		return true
	}

	if cmd.Signature == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(a.commandSecret))

	payloadString := ""
	if len(cmd.Payload) > 0 {
		payloadString = string(cmd.Payload)
	}

	data := strings.Join([]string{
		cmd.ID,
		cmd.Name,
		payloadString,
		cmd.CreatedAt,
	}, "|")

	mac.Write([]byte(data))
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(cmd.Signature), []byte(expectedMAC))
}

func (a *Agent) executeCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	if a.commands == nil {
		return newFailureResult(cmd.ID, "command router not initialized")
	}
	return a.commands.dispatch(ctx, a, cmd)
}

func pingCommandHandler(_ context.Context, _ *Agent, cmd protocol.Command) protocol.CommandResult {
	var payload protocol.PingCommandPayload
	_ = json.Unmarshal(cmd.Payload, &payload)
	response := "pong"
	if strings.TrimSpace(payload.Message) != "" {
		response = payload.Message
	}
	return newSuccessResult(cmd.ID, response)
}

func shellCommandHandler(ctx context.Context, agent *Agent, cmd protocol.Command) protocol.CommandResult {
	if agent == nil {
		return newFailureResult(cmd.ID, "shell command requires agent context")
	}
	var payload protocol.ShellCommandPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return newFailureResult(cmd.ID, fmt.Sprintf("invalid shell payload: %v", err))
	}

	if strings.TrimSpace(payload.Command) == "" {
		return newFailureResult(cmd.ID, "missing command")
	}

	if payload.Elevated && !platform.CurrentUserIsElevated() {
		return newFailureResult(cmd.ID, "elevated execution requested but agent is not running with sufficient privileges")
	}

	workingDirectory, err := normalizeWorkingDirectory(payload.WorkingDirectory)
	if err != nil {
		return newFailureResult(cmd.ID, err.Error())
	}

	timeout := agent.shellTimeout()
	if payload.TimeoutSeconds > 0 {
		timeout = time.Duration(payload.TimeoutSeconds) * time.Second
	}

	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	streamer := newCommandOutputStreamer(agent, cmd.ID)
	var streamWriter io.Writer
	if streamer != nil {
		streamWriter = streamer
	}

	output, err := runShell(commandCtx, payload.Command, shellExecutionOptions{
		workingDirectory: workingDirectory,
		environment:      payload.Environment,
	}, streamWriter)

	var result protocol.CommandResult
	if err != nil {
		result = newDetailedResult(cmd.ID, false, string(output), err.Error())
	} else {
		result = newDetailedResult(cmd.ID, true, string(output), "")
	}

	if streamer != nil {
		streamer.Complete(result)
	}

	return result
}

var (
	agentShutdownFunc = platform.Shutdown
	agentRestartFunc  = platform.Restart
	agentSleepFunc    = platform.Sleep
	agentLogoffFunc   = platform.Logoff
)

func agentControlCommandHandler(_ context.Context, agent *Agent, cmd protocol.Command) protocol.CommandResult {
	if agent == nil {
		return newFailureResult(cmd.ID, "agent-control command requires agent context")
	}

	var payload protocol.AgentControlCommandPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return newFailureResult(cmd.ID, fmt.Sprintf("invalid agent-control payload: %v", err))
	}

	action := strings.ToLower(strings.TrimSpace(payload.Action))
	switch action {
	case "disconnect":
		agent.requestDisconnect()
		return newSuccessResult(cmd.ID, "disconnect requested")
	case "reconnect":
		agent.requestReconnect()
		return newSuccessResult(cmd.ID, "reconnect requested")
	case "shutdown":
		if err := agentShutdownFunc(); err != nil {
			return newFailureResult(cmd.ID, fmt.Sprintf("failed to shutdown: %v", err))
		}
		return newSuccessResult(cmd.ID, "shutdown requested")
	case "restart":
		if err := agentRestartFunc(); err != nil {
			return newFailureResult(cmd.ID, fmt.Sprintf("failed to restart: %v", err))
		}
		return newSuccessResult(cmd.ID, "restart requested")
	case "sleep":
		if err := agentSleepFunc(); err != nil {
			return newFailureResult(cmd.ID, fmt.Sprintf("failed to sleep: %v", err))
		}
		return newSuccessResult(cmd.ID, "sleep requested")
	case "logoff":
		if err := agentLogoffFunc(); err != nil {
			return newFailureResult(cmd.ID, fmt.Sprintf("failed to logoff: %v", err))
		}
		return newSuccessResult(cmd.ID, "logoff requested")
	}

	if action == "" {
		return newFailureResult(cmd.ID, "missing agent-control action")
	}

	return newFailureResult(cmd.ID, fmt.Sprintf("unsupported agent-control action: %s", action))
}

func openURLCommandHandler(_ context.Context, _ *Agent, cmd protocol.Command) protocol.CommandResult {
	var payload protocol.OpenURLCommandPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return newFailureResult(cmd.ID, fmt.Sprintf("invalid open-url payload: %v", err))
	}

	trimmed := strings.TrimSpace(payload.URL)
	if trimmed == "" {
		return newFailureResult(cmd.ID, "missing url")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return newFailureResult(cmd.ID, fmt.Sprintf("invalid url: %v", err))
	}

	if !parsed.IsAbs() || parsed.Host == "" {
		return newFailureResult(cmd.ID, "url must be absolute")
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return newFailureResult(cmd.ID, fmt.Sprintf("unsupported url scheme: %s", parsed.Scheme))
	}

	normalized := parsed.String()
	if err := openURLInBrowser(normalized); err != nil {
		return newFailureResult(cmd.ID, fmt.Sprintf("failed to open url: %v", err))
	}

	return newSuccessResult(cmd.ID, fmt.Sprintf("opened %s", normalized))
}

func toolActivationCommandHandler(ctx context.Context, agent *Agent, cmd protocol.Command) protocol.CommandResult {
	var payload protocol.ToolActivationCommandPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return newFailureResult(cmd.ID, fmt.Sprintf("invalid tool activation payload: %v", err))
	}

	action := strings.TrimSpace(payload.Action)
	if action == "" {
		action = "open"
	}

	toolID := strings.TrimSpace(payload.ToolID)
	if toolID == "" {
		return newFailureResult(cmd.ID, "missing tool identifier")
	}

	if agent != nil && agent.logger != nil {
		agent.logger.Printf(
			"tool activation received: action=%s tool=%s initiatedBy=%s metadata=%v",
			action,
			toolID,
			strings.TrimSpace(payload.InitiatedBy),
			payload.Metadata,
		)
	}

	if agent != nil && strings.EqualFold(toolID, "options") && strings.HasPrefix(action, "operation:") {
		operation := strings.TrimSpace(action[len("operation:"):])
		if operation == "" {
			return newFailureResult(cmd.ID, "missing options operation")
		}

		lowerOp := strings.ToLower(operation)
		switch lowerOp {
		case "script-status":
			return newSuccessResult(cmd.ID, agent.scriptAutomationSummary())
		case "script-stop":
			if agent.scriptRunner == nil {
				return newFailureResult(cmd.ID, "script automation unavailable")
			}
			agent.scriptRunner.Stop()
			summary := agent.scriptAutomationSummary()
			if strings.TrimSpace(summary) == "" {
				summary = "Script automation stopped"
			}
			return newSuccessResult(cmd.ID, summary)
		}

		if agent.options == nil {
			return newFailureResult(cmd.ID, "options manager unavailable")
		}
		summary, err := agent.options.ApplyOperation(ctx, operation, payload.Metadata, agent.fetchStagedScript)
		if err != nil {
			return newFailureResult(cmd.ID, err.Error())
		}
		return newSuccessResult(cmd.ID, summary)
	}

	summary := fmt.Sprintf("%s %s", action, toolID)
	if actor := strings.TrimSpace(payload.InitiatedBy); actor != "" {
		summary = fmt.Sprintf("%s by %s", summary, actor)
	}

	return newSuccessResult(cmd.ID, summary)
}

func (a *Agent) fetchStagedScript(ctx context.Context, token string) (*options.ScriptPayload, error) {
	if a == nil {
		return nil, fmt.Errorf("options manager unavailable")
	}

	trimmedToken := strings.TrimSpace(token)
	if trimmedToken == "" {
		return nil, fmt.Errorf("staging token is required")
	}

	base := strings.TrimSpace(a.baseURL)
	if base == "" {
		return nil, fmt.Errorf("missing base url")
	}

	agentID := strings.TrimSpace(a.id)
	if agentID == "" {
		return nil, fmt.Errorf("missing agent identifier")
	}

	endpoint, err := url.JoinPath(base, "api", "agents", url.PathEscape(agentID), "options", "script")
	if err != nil {
		return nil, err
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}

	query := parsed.Query()
	query.Set("token", trimmedToken)
	parsed.RawQuery = query.Encode()

	key := strings.TrimSpace(a.key)
	if key == "" {
		return nil, fmt.Errorf("missing agent key")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", a.userAgent())
	applyRequestDecorations(req, a.requestHeaders, a.requestCookies)

	client := a.client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		limited := io.LimitReader(resp.Body, 2048)
		body, _ := io.ReadAll(limited)
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = fmt.Sprintf("status %d", resp.StatusCode)
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("script download unauthorized: %s", message)
		case http.StatusNotFound:
			return nil, fmt.Errorf("script staging token not found")
		default:
			return nil, fmt.Errorf("script download failed: %s", message)
		}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("staged script payload empty")
	}

	payload := &options.ScriptPayload{
		Data: data,
		Size: int64(len(data)),
	}

	if name := strings.TrimSpace(resp.Header.Get("X-Tenvy-Script-Name")); name != "" {
		payload.Name = name
	}
	if scriptType := strings.TrimSpace(resp.Header.Get("X-Tenvy-Script-Type")); scriptType != "" {
		payload.Type = scriptType
	} else if scriptType := strings.TrimSpace(resp.Header.Get("Content-Type")); scriptType != "" {
		payload.Type = scriptType
	}
	if sizeHeader := strings.TrimSpace(resp.Header.Get("X-Tenvy-Script-Size")); sizeHeader != "" {
		if parsedSize, err := strconv.ParseInt(sizeHeader, 10, 64); err == nil && parsedSize > 0 {
			payload.Size = parsedSize
		}
	}

	return payload, nil
}

func normalizeWorkingDirectory(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}

	resolved := trimmed
	if !filepath.IsAbs(resolved) {
		absPath, err := filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
		resolved = absPath
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("invalid working directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory is not a directory: %s", resolved)
	}

	return resolved, nil
}

func runShell(ctx context.Context, command string, options shellExecutionOptions, stream io.Writer) ([]byte, error) {
	var execCmd *exec.Cmd
	if runtime.GOOS == "windows" {
		execCmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		execCmd = exec.CommandContext(ctx, shell, "-c", command)
	}

	if options.workingDirectory != "" {
		execCmd.Dir = options.workingDirectory
	}

	if len(options.environment) > 0 {
		execCmd.Env = mergeEnvironments(os.Environ(), options.environment)
	}

	var buffer bytes.Buffer
	writer := io.Writer(&buffer)
	if stream != nil {
		writer = io.MultiWriter(&buffer, stream)
	}

	execCmd.Stdout = writer
	execCmd.Stderr = writer

	if err := execCmd.Run(); err != nil {
		return buffer.Bytes(), err
	}
	return buffer.Bytes(), nil
}

func mergeEnvironments(base []string, overrides map[string]string) []string {
	return mergeEnvironmentsWithComparer(base, overrides, runtime.GOOS == "windows")
}

func mergeEnvironmentsWithComparer(base []string, overrides map[string]string, caseInsensitive bool) []string {
	type overrideEntry struct {
		normalized string
		key        string
		value      string
	}

	normalizeKey := func(key string) string {
		if caseInsensitive {
			return strings.ToLower(key)
		}
		return key
	}

	overridesByKey := make(map[string]overrideEntry, len(overrides))
	for key, value := range overrides {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		normalizedKey := normalizeKey(trimmedKey)
		overridesByKey[normalizedKey] = overrideEntry{
			normalized: normalizedKey,
			key:        trimmedKey,
			value:      value,
		}
	}

	env := make([]string, 0, len(base)+len(overridesByKey))
	seenBaseKeys := make(map[string]struct{}, len(base))
	pendingOverrides := make([]overrideEntry, 0, len(overridesByKey))

	for _, kv := range base {
		keyPortion := kv
		if idx := strings.IndexRune(kv, '='); idx >= 0 {
			keyPortion = kv[:idx]
		}
		trimmedKey := strings.TrimSpace(keyPortion)
		normalizedKey := normalizeKey(trimmedKey)

		if normalizedKey == "" {
			env = append(env, kv)
			continue
		}

		if _, seen := seenBaseKeys[normalizedKey]; seen {
			continue
		}
		seenBaseKeys[normalizedKey] = struct{}{}

		if entry, overridden := overridesByKey[normalizedKey]; overridden {
			pendingOverrides = append(pendingOverrides, entry)
			delete(overridesByKey, normalizedKey)
			continue
		}

		env = append(env, kv)
	}

	for _, entry := range pendingOverrides {
		env = append(env, fmt.Sprintf("%s=%s", entry.key, entry.value))
	}

	if len(overridesByKey) > 0 {
		remaining := make([]overrideEntry, 0, len(overridesByKey))
		for _, entry := range overridesByKey {
			remaining = append(remaining, entry)
		}
		sort.SliceStable(remaining, func(i, j int) bool {
			return remaining[i].key < remaining[j].key
		})
		for _, entry := range remaining {
			env = append(env, fmt.Sprintf("%s=%s", entry.key, entry.value))
		}
	}

	return env
}

func newDetailedResult(cmdID string, success bool, output, errMsg string) protocol.CommandResult {
	return protocol.CommandResult{
		CommandID:   cmdID,
		Success:     success,
		Output:      output,
		Error:       errMsg,
		CompletedAt: timestampNow(),
	}
}

func newFailureResult(cmdID, errMsg string) protocol.CommandResult {
	return newDetailedResult(cmdID, false, "", errMsg)
}

func newSuccessResult(cmdID, output string) protocol.CommandResult {
	return newDetailedResult(cmdID, true, output, "")
}

func openURLInBrowser(target string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	if cmd.Process != nil {
		return cmd.Process.Release()
	}

	return nil
}

func (a *Agent) shellTimeout() time.Duration {
	if a.timing.ShellTimeout > 0 {
		return a.timing.ShellTimeout
	}
	return defaultShellTimeout
}
