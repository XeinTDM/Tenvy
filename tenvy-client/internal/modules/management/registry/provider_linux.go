//go:build linux

package registry

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	gsettingsHiveName = "gsettings"
)

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) > 0 {
			return output, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
		}
		return output, err
	}
	return output, nil
}

type nativeProvider struct {
	gsettingsPath string
	runner        commandRunner
	caps          ProviderCapabilities
}

func newNativeProvider() Provider {
	path, _ := exec.LookPath("gsettings")
	provider := &nativeProvider{
		gsettingsPath: path,
		runner:        execCommandRunner{},
	}
	if path != "" {
		provider.caps = ProviderCapabilities{Enumerate: true, Mutate: true}
	}
	return provider
}

func (p *nativeProvider) withRunner(r commandRunner) {
	if r != nil {
		p.runner = r
	}
}

func (p *nativeProvider) Capabilities() ProviderCapabilities {
	return p.caps
}

func (p *nativeProvider) ensureAvailable() error {
	if p.gsettingsPath == "" {
		return ErrNotSupported
	}
	if p.runner == nil {
		p.runner = execCommandRunner{}
	}
	return nil
}

func (p *nativeProvider) List(ctx context.Context, req ListRequest) (RegistryListResult, error) {
	if err := p.ensureAvailable(); err != nil {
		return RegistryListResult{}, err
	}

	snapshot := make(RegistrySnapshot)
	hive := make(RegistryHive)
	snapshot[gsettingsHiveName] = hive

	schemas := []string{}
	trimmedPath := strings.TrimSpace(req.Path)
	if trimmedPath != "" {
		schemas = append(schemas, trimmedPath)
	}

	if len(schemas) == 0 {
		output, err := p.runner.Run(ctx, p.gsettingsPath, "list-schemas")
		if err != nil {
			return RegistryListResult{}, err
		}
		scanner := bufio.NewScanner(bytes.NewReader(output))
		for scanner.Scan() {
			schema := strings.TrimSpace(scanner.Text())
			if schema != "" {
				schemas = append(schemas, schema)
			}
		}
		if err := scanner.Err(); err != nil {
			return RegistryListResult{}, fmt.Errorf("read schema list: %w", err)
		}
	}

	owner := strings.TrimSpace(os.Getenv("USER"))
	if owner == "" {
		owner = "unknown"
	}

	for _, schema := range schemas {
		key, err := p.enumerateSchema(ctx, schema, owner)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return RegistryListResult{}, err
			}
			continue
		}
		hive[schema] = key
	}

	return RegistryListResult{
		Snapshot:    snapshot,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (p *nativeProvider) enumerateSchema(ctx context.Context, schema, owner string) (RegistryKey, error) {
	if schema == "" {
		return RegistryKey{}, fmt.Errorf("missing schema")
	}
	output, err := p.runner.Run(ctx, p.gsettingsPath, "list-recursively", schema)
	if err != nil {
		return RegistryKey{}, err
	}

	values := make([]RegistryValue, 0, 8)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 3 {
			continue
		}
		keyName := strings.TrimSpace(parts[1])
		rawValue := strings.TrimSpace(parts[2])
		valueType := detectGSettingsType(rawValue)
		values = append(values, RegistryValue{
			Name:         keyName,
			Type:         valueType,
			Data:         rawValue,
			Size:         len(rawValue),
			LastModified: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	if err := scanner.Err(); err != nil {
		return RegistryKey{}, fmt.Errorf("parse schema listing: %w", err)
	}

	var parent *string
	if idx := strings.LastIndex(schema, "."); idx > 0 {
		parentPath := schema[:idx]
		parent = &parentPath
	}

	return RegistryKey{
		Hive:         gsettingsHiveName,
		Name:         schema,
		Path:         schema,
		ParentPath:   parent,
		Values:       values,
		SubKeys:      nil,
		LastModified: time.Now().UTC().Format(time.RFC3339Nano),
		Owner:        owner,
	}, nil
}

func detectGSettingsType(raw string) string {
	if raw == "" {
		return "string"
	}
	lowered := strings.ToLower(raw)
	if lowered == "true" || lowered == "false" {
		return "bool"
	}
	if _, err := fmt.Sscanf(raw, "%d", new(int)); err == nil {
		return "int"
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		return "array"
	}
	if strings.HasPrefix(raw, "(") && strings.HasSuffix(raw, ")") {
		return "tuple"
	}
	if strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") {
		return "string"
	}
	return "string"
}

func (p *nativeProvider) CreateKey(ctx context.Context, req CreateKeyRequest) (RegistryMutationResult, error) {
	return RegistryMutationResult{}, ErrNotSupported
}

func (p *nativeProvider) CreateValue(ctx context.Context, req CreateValueRequest) (RegistryMutationResult, error) {
	return p.writeValue(ctx, req.Hive, req.KeyPath, req.Value)
}

func (p *nativeProvider) UpdateKey(ctx context.Context, req UpdateKeyRequest) (RegistryMutationResult, error) {
	return RegistryMutationResult{}, ErrNotSupported
}

func (p *nativeProvider) UpdateValue(ctx context.Context, req UpdateValueRequest) (RegistryMutationResult, error) {
	return p.writeValue(ctx, req.Hive, req.KeyPath, req.Value)
}

func (p *nativeProvider) writeValue(ctx context.Context, hive, keyPath string, value RegistryValueInput) (RegistryMutationResult, error) {
	if err := p.ensureAvailable(); err != nil {
		return RegistryMutationResult{}, err
	}

	schema := strings.TrimSpace(keyPath)
	if schema == "" {
		schema = strings.TrimSpace(hive)
	}
	if schema == "" {
		return RegistryMutationResult{}, fmt.Errorf("gsettings schema required")
	}
	keyName := strings.TrimSpace(value.Name)
	if keyName == "" {
		return RegistryMutationResult{}, fmt.Errorf("gsettings key name required")
	}
	formatted, err := formatGSettingsValue(value.Type, value.Data)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	if _, err := p.runner.Run(ctx, p.gsettingsPath, append([]string{"set", schema, keyName}, formatted...)...); err != nil {
		return RegistryMutationResult{}, err
	}

	owner := strings.TrimSpace(os.Getenv("USER"))
	if owner == "" {
		owner = "unknown"
	}
	key, err := p.enumerateSchema(ctx, schema, owner)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	snapshot := RegistryHive{schema: key}
	return RegistryMutationResult{
		Hive:      snapshot,
		KeyPath:   schema,
		ValueName: stringPointer(keyName),
		MutatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func formatGSettingsValue(valueType, data string) ([]string, error) {
	trimmedType := strings.ToLower(strings.TrimSpace(valueType))
	trimmedData := strings.TrimSpace(data)
	switch trimmedType {
	case "", "string", "str", "reg_sz":
		if !strings.HasPrefix(trimmedData, "'") {
			trimmedData = fmt.Sprintf("'%s'", trimmedData)
		}
		return []string{trimmedData}, nil
	case "bool", "boolean":
		lowered := strings.ToLower(trimmedData)
		if lowered != "true" && lowered != "false" {
			return nil, fmt.Errorf("invalid boolean value: %s", data)
		}
		return []string{lowered}, nil
	case "int", "integer", "reg_dword":
		if _, err := fmt.Sscanf(trimmedData, "%d", new(int)); err != nil {
			return nil, fmt.Errorf("invalid integer value: %s", data)
		}
		return []string{trimmedData}, nil
	case "double", "float":
		if _, err := fmt.Sscanf(trimmedData, "%f", new(float64)); err != nil {
			return nil, fmt.Errorf("invalid float value: %s", data)
		}
		return []string{trimmedData}, nil
	case "array", "list":
		if !strings.HasPrefix(trimmedData, "[") {
			trimmedData = "[" + trimmedData
		}
		if !strings.HasSuffix(trimmedData, "]") {
			trimmedData = trimmedData + "]"
		}
		return []string{trimmedData}, nil
	default:
		return []string{trimmedData}, nil
	}
}

func (p *nativeProvider) DeleteKey(ctx context.Context, req DeleteKeyRequest) (RegistryMutationResult, error) {
	return RegistryMutationResult{}, ErrNotSupported
}

func (p *nativeProvider) DeleteValue(ctx context.Context, req DeleteValueRequest) (RegistryMutationResult, error) {
	if err := p.ensureAvailable(); err != nil {
		return RegistryMutationResult{}, err
	}
	schema := strings.TrimSpace(req.KeyPath)
	if schema == "" {
		schema = strings.TrimSpace(req.Hive)
	}
	if schema == "" {
		return RegistryMutationResult{}, fmt.Errorf("gsettings schema required")
	}
	keyName := strings.TrimSpace(req.Name)
	if keyName == "" {
		return RegistryMutationResult{}, fmt.Errorf("gsettings key name required")
	}
	if _, err := p.runner.Run(ctx, p.gsettingsPath, "reset", schema, keyName); err != nil {
		return RegistryMutationResult{}, err
	}
	owner := strings.TrimSpace(os.Getenv("USER"))
	if owner == "" {
		owner = "unknown"
	}
	key, err := p.enumerateSchema(ctx, schema, owner)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	snapshot := RegistryHive{schema: key}
	return RegistryMutationResult{
		Hive:      snapshot,
		KeyPath:   schema,
		ValueName: stringPointer(keyName),
		MutatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func newTestNativeProvider(r commandRunner) *nativeProvider {
	provider := newNativeProvider().(*nativeProvider)
	provider.withRunner(r)
	if provider.gsettingsPath == "" {
		provider.gsettingsPath = "gsettings"
	}
	provider.caps = ProviderCapabilities{Enumerate: true, Mutate: true}
	if provider.runner == nil {
		provider.runner = r
	}
	return provider
}
