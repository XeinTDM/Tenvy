//go:build windows

package startupengine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath         = `Software\\Microsoft\\Windows\\CurrentVersion\\Run`
	runDisabledKeyPath = `Software\\Microsoft\\Windows\\CurrentVersion\\RunDisabled`
)

type nativeProvider struct{}

func newNativeProvider() Provider {
	return &nativeProvider{}
}

func (p *nativeProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Enumerate: true, Manage: true}
}

func (p *nativeProvider) List(ctx context.Context, req ListRequest) (Inventory, error) {
	entries := make([]Entry, 0, 32)

	machineEntries, err := p.enumerateRegistryScope(registry.LOCAL_MACHINE, ScopeMachine)
	if err != nil {
		return Inventory{}, err
	}
	entries = append(entries, machineEntries...)

	userEntries, err := p.enumerateRegistryScope(registry.CURRENT_USER, ScopeUser)
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return Inventory{}, err
	}
	entries = append(entries, userEntries...)

	taskEntries, err := p.enumerateScheduledTasks(ctx)
	if err == nil {
		entries = append(entries, taskEntries...)
	}

	summary := computeSummary(entries)

	return Inventory{
		Entries:     entries,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Summary:     &summary,
	}, nil
}

func (p *nativeProvider) Toggle(ctx context.Context, req ToggleRequest) (Entry, error) {
	if strings.HasPrefix(req.EntryID, "registry:") {
		return p.toggleRegistry(req.EntryID, req.Enabled)
	}
	if strings.HasPrefix(req.EntryID, "task:") {
		return p.toggleTask(ctx, req.EntryID, req.Enabled)
	}
	return Entry{}, fmt.Errorf("unsupported startup entry id %q", req.EntryID)
}

func (p *nativeProvider) Create(ctx context.Context, req CreateRequest) (Entry, error) {
	def := normalizeDefinition(req.Definition)
	switch def.Source {
	case SourceRegistry:
		return p.createRegistryEntry(def)
	case SourceScheduledTask:
		return p.createScheduledTask(ctx, def)
	default:
		return Entry{}, fmt.Errorf("startup source %q not supported", def.Source)
	}
}

func (p *nativeProvider) Remove(ctx context.Context, req RemoveRequest) (RemoveResult, error) {
	if strings.HasPrefix(req.EntryID, "registry:") {
		if err := p.removeRegistryEntry(req.EntryID); err != nil {
			return RemoveResult{}, err
		}
		return RemoveResult{EntryID: req.EntryID}, nil
	}
	if strings.HasPrefix(req.EntryID, "task:") {
		if err := p.removeScheduledTask(ctx, req.EntryID); err != nil {
			return RemoveResult{}, err
		}
		return RemoveResult{EntryID: req.EntryID}, nil
	}
	return RemoveResult{}, fmt.Errorf("unsupported startup entry id %q", req.EntryID)
}

func (p *nativeProvider) enumerateRegistryScope(root registry.Key, scope StartupScope) ([]Entry, error) {
	entries := make([]Entry, 0, 16)

	enabledEntries, err := p.enumerateRegistryKey(root, runKeyPath, scope, true)
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return nil, err
	}
	entries = append(entries, enabledEntries...)

	disabledEntries, err := p.enumerateRegistryKey(root, runDisabledKeyPath, scope, false)
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return nil, err
	}
	entries = append(entries, disabledEntries...)

	return entries, nil
}

func (p *nativeProvider) enumerateRegistryKey(root registry.Key, path string, scope StartupScope, enabled bool) ([]Entry, error) {
	key, err := registry.OpenKey(root, path, registry.READ)
	if err != nil {
		return nil, err
	}
	defer key.Close()

	names, err := key.ReadValueNames(0)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(names))
	for _, valueName := range names {
		command, valType, err := key.GetStringValue(valueName)
		if err != nil {
			if valType == registry.EXPAND_SZ {
				command, _, err = key.GetStringValue(valueName)
			}
		}
		if err != nil {
			continue
		}
		executable, arguments := splitCommand(command)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		displayName := displayRegistryValueName(valueName)
		entries = append(entries, Entry{
			ID:              buildRegistryID(scope, valueName),
			Name:            displayName,
			Path:            executable,
			Arguments:       arguments,
			Enabled:         enabled,
			Scope:           scope,
			Source:          SourceRegistry,
			Impact:          ImpactNotMeasured,
			Publisher:       "",
			Description:     "Registry run key entry",
			Location:        registryLocation(scope, path),
			StartupTime:     0,
			LastEvaluatedAt: now,
		})
	}
	return entries, nil
}

func (p *nativeProvider) toggleRegistry(entryID string, enabled bool) (Entry, error) {
	scope, valueName, err := parseRegistryID(entryID)
	if err != nil {
		return Entry{}, err
	}
	valueKeyName := normalizeRegistryValueName(valueName)
	root := registry.LOCAL_MACHINE
	if scope == ScopeUser {
		root = registry.CURRENT_USER
	}
	if enabled {
		entry, err := p.enableRegistryValue(root, scope, valueKeyName)
		if err != nil {
			return Entry{}, err
		}
		return entry, nil
	}
	entry, err := p.disableRegistryValue(root, scope, valueKeyName)
	if err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (p *nativeProvider) enableRegistryValue(root registry.Key, scope StartupScope, valueName string) (Entry, error) {
	disabledKey, err := registry.OpenKey(root, runDisabledKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return Entry{}, err
	}
	var command string
	if disabledKey != 0 {
		defer disabledKey.Close()
		command, _, err = disabledKey.GetStringValue(valueName)
		if err != nil {
			command = ""
		}
	}
	enabledKey, _, err := registry.CreateKey(root, runKeyPath, registry.CREATE_SUB_KEY|registry.SET_VALUE)
	if err != nil {
		return Entry{}, err
	}
	defer enabledKey.Close()
	if command == "" {
		command, _, err = enabledKey.GetStringValue(valueName)
		if err != nil {
			return Entry{}, fmt.Errorf("registry value %q not found", displayRegistryValueName(valueName))
		}
	} else {
		if err := enabledKey.SetStringValue(valueName, command); err != nil {
			return Entry{}, err
		}
		if disabledKey != 0 {
			_ = disabledKey.DeleteValue(valueName)
		}
	}
	executable, arguments := splitCommand(command)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return Entry{
		ID:              buildRegistryID(scope, valueName),
		Name:            displayRegistryValueName(valueName),
		Path:            executable,
		Arguments:       arguments,
		Enabled:         true,
		Scope:           scope,
		Source:          SourceRegistry,
		Impact:          ImpactNotMeasured,
		Description:     "Registry run key entry",
		Location:        registryLocation(scope, runKeyPath),
		StartupTime:     0,
		LastEvaluatedAt: now,
	}, nil
}

func (p *nativeProvider) disableRegistryValue(root registry.Key, scope StartupScope, valueName string) (Entry, error) {
	enabledKey, err := registry.OpenKey(root, runKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return Entry{}, err
	}
	defer enabledKey.Close()
	command, _, err := enabledKey.GetStringValue(valueName)
	if err != nil {
		return Entry{}, fmt.Errorf("registry value %q not found", displayRegistryValueName(valueName))
	}
	disabledKey, _, err := registry.CreateKey(root, runDisabledKeyPath, registry.CREATE_SUB_KEY|registry.SET_VALUE)
	if err != nil {
		return Entry{}, err
	}
	defer disabledKey.Close()
	if err := disabledKey.SetStringValue(valueName, command); err != nil {
		return Entry{}, err
	}
	_ = enabledKey.DeleteValue(valueName)
	executable, arguments := splitCommand(command)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return Entry{
		ID:              buildRegistryID(scope, valueName),
		Name:            displayRegistryValueName(valueName),
		Path:            executable,
		Arguments:       arguments,
		Enabled:         false,
		Scope:           scope,
		Source:          SourceRegistry,
		Impact:          ImpactNotMeasured,
		Description:     "Registry run key entry",
		Location:        registryLocation(scope, runDisabledKeyPath),
		StartupTime:     0,
		LastEvaluatedAt: now,
	}, nil
}

func (p *nativeProvider) createRegistryEntry(def EntryDefinition) (Entry, error) {
	root := registry.LOCAL_MACHINE
	if def.Scope == ScopeUser {
		root = registry.CURRENT_USER
	}
	targetKey := runKeyPath
	if !def.Enabled {
		targetKey = runDisabledKeyPath
	}
	key, _, err := registry.CreateKey(root, targetKey, registry.CREATE_SUB_KEY|registry.SET_VALUE)
	if err != nil {
		return Entry{}, err
	}
	defer key.Close()

	valueName := normalizeRegistryValueName(def.Name)
	command := buildCommandString(def.Path, def.Arguments)
	if command == "" {
		return Entry{}, fmt.Errorf("startup command path is required")
	}
	if err := key.SetStringValue(valueName, command); err != nil {
		return Entry{}, err
	}
	executable, arguments := splitCommand(command)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return Entry{
		ID:              buildRegistryID(def.Scope, valueName),
		Name:            displayRegistryValueName(valueName),
		Path:            executable,
		Arguments:       arguments,
		Enabled:         def.Enabled,
		Scope:           def.Scope,
		Source:          SourceRegistry,
		Impact:          ImpactNotMeasured,
		Publisher:       def.Publisher,
		Description:     def.Description,
		Location:        registryLocation(def.Scope, targetKey),
		StartupTime:     0,
		LastEvaluatedAt: now,
	}, nil
}

func (p *nativeProvider) removeRegistryEntry(entryID string) error {
	scope, valueName, err := parseRegistryID(entryID)
	if err != nil {
		return err
	}
	root := registry.LOCAL_MACHINE
	if scope == ScopeUser {
		root = registry.CURRENT_USER
	}
	normalized := normalizeRegistryValueName(valueName)
	enabledKey, err := registry.OpenKey(root, runKeyPath, registry.SET_VALUE)
	if err == nil {
		_ = enabledKey.DeleteValue(normalized)
		enabledKey.Close()
	}
	disabledKey, err := registry.OpenKey(root, runDisabledKeyPath, registry.SET_VALUE)
	if err == nil {
		_ = disabledKey.DeleteValue(normalized)
		disabledKey.Close()
	}
	return nil
}

func (p *nativeProvider) toggleTask(ctx context.Context, entryID string, enabled bool) (Entry, error) {
	taskName, err := parseTaskID(entryID)
	if err != nil {
		return Entry{}, err
	}
	args := []string{"/Change", "/TN", taskName}
	if enabled {
		args = append(args, "/Enable")
	} else {
		args = append(args, "/Disable")
	}
	cmd := exec.CommandContext(ctx, "schtasks", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return Entry{}, fmt.Errorf("update scheduled task: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	tasks, err := p.enumerateScheduledTasks(ctx)
	if err != nil {
		return Entry{}, err
	}
	for _, entry := range tasks {
		if entry.ID == entryID {
			entry.Enabled = enabled
			entry.LastEvaluatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			return entry, nil
		}
	}
	return Entry{}, fmt.Errorf("scheduled task %s not found", taskName)
}

func (p *nativeProvider) createScheduledTask(ctx context.Context, def EntryDefinition) (Entry, error) {
	taskName := normalizeTaskName(def.Name)
	if taskName == "" {
		return Entry{}, fmt.Errorf("scheduled task name required")
	}
	command := buildCommandString(def.Path, def.Arguments)
	if command == "" {
		return Entry{}, fmt.Errorf("scheduled task command required")
	}
	args := []string{"/Create", "/TN", taskName, "/TR", command, "/SC", "ONLOGON"}
	if def.Scope == ScopeMachine {
		args = append(args, "/RU", "SYSTEM")
	}
	if !def.Enabled {
		args = append(args, "/Disable")
	}
	cmd := exec.CommandContext(ctx, "schtasks", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return Entry{}, fmt.Errorf("create scheduled task: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	tasks, err := p.enumerateScheduledTasks(ctx)
	if err != nil {
		return Entry{}, err
	}
	for _, entry := range tasks {
		if strings.EqualFold(entry.Name, taskName) {
			entry.LastEvaluatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			entry.Enabled = def.Enabled
			entry.Description = def.Description
			entry.Publisher = def.Publisher
			return entry, nil
		}
	}
	return Entry{}, fmt.Errorf("scheduled task %s not found after creation", taskName)
}

func (p *nativeProvider) removeScheduledTask(ctx context.Context, entryID string) error {
	taskName, err := parseTaskID(entryID)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "schtasks", "/Delete", "/TN", taskName, "/F")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete scheduled task: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (p *nativeProvider) enumerateScheduledTasks(ctx context.Context) ([]Entry, error) {
	cmd := exec.CommandContext(ctx, "schtasks", "/Query", "/FO", "CSV", "/V")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	reader := csv.NewReader(bytes.NewReader(output))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) <= 1 {
		return []Entry{}, nil
	}
	header := records[0]
	nameIdx := indexOf(header, "TaskName")
	runIdx := indexOf(header, "Task To Run")
	stateIdx := indexOf(header, "Scheduled Task State")
	lastRunIdx := indexOf(header, "Last Run Time")
	authorIdx := indexOf(header, "Author")
	descIdx := indexOf(header, "Description")
	runAsIdx := indexOf(header, "Run As User")

	entries := make([]Entry, 0, len(records)-1)
	for _, record := range records[1:] {
		if nameIdx == -1 || nameIdx >= len(record) {
			continue
		}
		rawName := strings.TrimSpace(record[nameIdx])
		if rawName == "" || strings.EqualFold(rawName, "TaskName") {
			continue
		}
		command := ""
		if runIdx >= 0 && runIdx < len(record) {
			command = strings.TrimSpace(record[runIdx])
		}
		executable, arguments := splitCommand(command)
		state := ""
		if stateIdx >= 0 && stateIdx < len(record) {
			state = strings.TrimSpace(record[stateIdx])
		}
		enabled := !strings.Contains(strings.ToLower(state), "disabled")
		description := ""
		if descIdx >= 0 && descIdx < len(record) {
			description = strings.TrimSpace(record[descIdx])
		}
		author := ""
		if authorIdx >= 0 && authorIdx < len(record) {
			author = strings.TrimSpace(record[authorIdx])
		}
		runAs := ""
		if runAsIdx >= 0 && runAsIdx < len(record) {
			runAs = strings.TrimSpace(record[runAsIdx])
		}
		scope := ScopeScheduledTask
		if strings.Contains(strings.ToLower(runAs), "\\") {
			scope = ScopeMachine
		}
		location := fmt.Sprintf("Scheduled Task: %s", strings.TrimPrefix(rawName, "\\"))
		var lastRun *string
		if lastRunIdx >= 0 && lastRunIdx < len(record) {
			parsed := parseScheduledTime(record[lastRunIdx])
			if parsed != "" {
				lastRun = &parsed
			}
		}
		entryID := buildTaskID(rawName)
		entries = append(entries, Entry{
			ID:              entryID,
			Name:            strings.TrimPrefix(rawName, "\\"),
			Path:            executable,
			Arguments:       arguments,
			Enabled:         enabled,
			Scope:           scope,
			Source:          SourceScheduledTask,
			Impact:          ImpactNotMeasured,
			Publisher:       author,
			Description:     description,
			Location:        location,
			StartupTime:     0,
			LastEvaluatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			LastRunAt:       lastRun,
		})
	}
	return entries, nil
}

func indexOf(values []string, target string) int {
	for i, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return i
		}
	}
	return -1
}

func splitCommand(command string) (string, string) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "", ""
	}
	if strings.HasPrefix(trimmed, "\"") {
		if idx := strings.Index(trimmed[1:], "\""); idx >= 0 {
			closing := idx + 1
			inner := trimmed[1:closing]
			rest := strings.TrimSpace(trimmed[closing+1:])
			return inner, rest
		}
	}
	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return trimmed, ""
	}
	path := parts[0]
	args := strings.Join(parts[1:], " ")
	return path, args
}

func buildCommandString(path, args string) string {
	executable := strings.TrimSpace(path)
	if executable == "" {
		return ""
	}
	if strings.ContainsAny(executable, " \t") && !strings.HasPrefix(executable, "\"") {
		executable = fmt.Sprintf("\"%s\"", strings.Trim(executable, "\""))
	}
	arguments := strings.TrimSpace(args)
	if arguments == "" {
		return executable
	}
	return strings.TrimSpace(executable + " " + arguments)
}

func displayRegistryValueName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "(Default)"
	}
	return name
}

func normalizeRegistryValueName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "(Default)" {
		return ""
	}
	return trimmed
}

func registryLocation(scope StartupScope, path string) string {
	prefix := "HKCU"
	if scope == ScopeMachine {
		prefix = "HKLM"
	}
	return fmt.Sprintf("%s:%s", prefix, path)
}

func buildRegistryID(scope StartupScope, valueName string) string {
	encoded := encodeComponent(valueName)
	return fmt.Sprintf("registry:%s:%s", scope, encoded)
}

func parseRegistryID(id string) (StartupScope, string, error) {
	parts := strings.SplitN(id, ":", 3)
	if len(parts) != 3 {
		return "", "", fmt.Errorf("invalid registry entry id")
	}
	scope := StartupScope(parts[1])
	valueName, err := decodeComponent(parts[2])
	if err != nil {
		return "", "", err
	}
	return scope, valueName, nil
}

func buildTaskID(taskName string) string {
	encoded := encodeComponent(taskName)
	return fmt.Sprintf("task:%s", encoded)
}

func parseTaskID(id string) (string, error) {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid scheduled task entry id")
	}
	taskName, err := decodeComponent(parts[1])
	if err != nil {
		return "", err
	}
	return normalizeTaskName(taskName), nil
}

func normalizeTaskName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "\\") {
		trimmed = "\\" + trimmed
	}
	return trimmed
}

func encodeComponent(value string) string {
	if value == "" {
		return "_"
	}
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeComponent(value string) (string, error) {
	if value == "_" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func computeSummary(entries []Entry) TelemetrySummary {
	summary := TelemetrySummary{
		ImpactCounts: make(map[StartupImpact]int),
		ScopeCounts:  make(map[StartupScope]int),
	}
	summary.Total = len(entries)
	for _, entry := range entries {
		if entry.Enabled {
			summary.Enabled++
		} else {
			summary.Disabled++
		}
		summary.ImpactCounts[entry.Impact]++
		summary.ScopeCounts[entry.Scope]++
	}
	return summary
}

func parseScheduledTime(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.EqualFold(trimmed, "n/a") || strings.EqualFold(trimmed, "not yet run") {
		return ""
	}
	layouts := []string{
		"1/2/2006 3:04:05 PM",
		"1/2/2006 15:04:05",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if ts, err := time.ParseInLocation(layout, trimmed, time.Local); err == nil {
			return ts.UTC().Format(time.RFC3339Nano)
		}
	}
	return ""
}
