//go:build darwin

package registryengine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	plist "howett.net/plist"
)

const defaultsHiveName = "defaults"

type nativeProvider struct {
	defaultsPath string
	caps         ProviderCapabilities
}

func newNativeProvider() Provider {
	path, _ := exec.LookPath("defaults")
	caps := ProviderCapabilities{}
	if path != "" {
		caps = ProviderCapabilities{Enumerate: true, Mutate: true}
	}
	return &nativeProvider{defaultsPath: path, caps: caps}
}

type plistDictionary map[string]interface{}

type defaultsCommandRunner interface {
	run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execDefaultsRunner struct{}

func (execDefaultsRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
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

func (p *nativeProvider) Capabilities() ProviderCapabilities {
	return p.caps
}

func (p *nativeProvider) ensureAvailable() error {
	if p.defaultsPath == "" {
		return ErrNotSupported
	}
	return nil
}

func (p *nativeProvider) runner() defaultsCommandRunner {
	return execDefaultsRunner{}
}

func (p *nativeProvider) List(ctx context.Context, req ListRequest) (RegistryListResult, error) {
	if err := p.ensureAvailable(); err != nil {
		return RegistryListResult{}, err
	}

	runner := p.runner()
	domains := []string{}
	trimmedPath := strings.TrimSpace(req.Path)
	if trimmedPath != "" {
		domains = append(domains, trimmedPath)
	}

	if len(domains) == 0 {
		output, err := runner.run(ctx, p.defaultsPath, "domains")
		if err != nil {
			return RegistryListResult{}, err
		}
		for _, domain := range strings.Fields(string(output)) {
			domain = strings.TrimSpace(domain)
			if domain != "" {
				domains = append(domains, domain)
			}
		}
	}

	snapshot := make(RegistrySnapshot)
	hive := make(RegistryHive)
	snapshot[defaultsHiveName] = hive

	owner := strings.TrimSpace(os.Getenv("USER"))
	if owner == "" {
		owner = "unknown"
	}

	for _, domain := range domains {
		values, err := p.readDomain(ctx, runner, domain)
		if err != nil {
			continue
		}
		hive[domain] = RegistryKey{
			Hive:         defaultsHiveName,
			Name:         domain,
			Path:         domain,
			ParentPath:   nil,
			Values:       values,
			SubKeys:      nil,
			LastModified: time.Now().UTC().Format(time.RFC3339Nano),
			Owner:        owner,
		}
	}

	return RegistryListResult{
		Snapshot:    snapshot,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (p *nativeProvider) readDomain(ctx context.Context, runner defaultsCommandRunner, domain string) ([]RegistryValue, error) {
	output, err := runner.run(ctx, p.defaultsPath, "export", domain, "-")
	if err != nil {
		return nil, err
	}
	decoder := plist.NewDecoder(bytes.NewReader(output))
	var dict plistDictionary
	if err := decoder.Decode(&dict); err != nil {
		return nil, err
	}
	values := make([]RegistryValue, 0, len(dict))
	for key, raw := range dict {
		data, valueType := formatDefaultsValue(raw)
		values = append(values, RegistryValue{
			Name:         key,
			Type:         valueType,
			Data:         data,
			Size:         len(data),
			LastModified: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	return values, nil
}

func formatDefaultsValue(value interface{}) (string, string) {
	switch v := value.(type) {
	case string:
		return v, "string"
	case bool:
		if v {
			return "true", "bool"
		}
		return "false", "bool"
	case int, int32, int64, uint, uint32, uint64:
		return fmt.Sprintf("%v", v), "int"
	case float32, float64:
		return fmt.Sprintf("%v", v), "float"
	case []byte:
		return fmt.Sprintf("%x", v), "data"
	default:
		return fmt.Sprintf("%v", v), "string"
	}
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
	domain := strings.TrimSpace(keyPath)
	if domain == "" {
		domain = strings.TrimSpace(hive)
	}
	if domain == "" {
		return RegistryMutationResult{}, fmt.Errorf("defaults domain required")
	}
	keyName := strings.TrimSpace(value.Name)
	if keyName == "" {
		return RegistryMutationResult{}, fmt.Errorf("defaults key name required")
	}

	args, err := buildDefaultsWriteArgs(domain, keyName, value)
	if err != nil {
		return RegistryMutationResult{}, err
	}

	runner := p.runner()
	if _, err := runner.run(ctx, p.defaultsPath, args...); err != nil {
		return RegistryMutationResult{}, err
	}
	values, err := p.readDomain(ctx, runner, domain)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	owner := strings.TrimSpace(os.Getenv("USER"))
	if owner == "" {
		owner = "unknown"
	}
	snapshot := RegistryHive{domain: {
		Hive:         defaultsHiveName,
		Name:         domain,
		Path:         domain,
		ParentPath:   nil,
		Values:       values,
		SubKeys:      nil,
		LastModified: time.Now().UTC().Format(time.RFC3339Nano),
		Owner:        owner,
	}}
	return RegistryMutationResult{
		Hive:      snapshot,
		KeyPath:   domain,
		ValueName: stringPointer(keyName),
		MutatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func buildDefaultsWriteArgs(domain, keyName string, value RegistryValueInput) ([]string, error) {
	valueType := strings.ToLower(strings.TrimSpace(value.Type))
	data := strings.TrimSpace(value.Data)
	switch valueType {
	case "", "string":
		return []string{"write", domain, keyName, data}, nil
	case "bool", "boolean":
		lower := strings.ToLower(data)
		if lower != "true" && lower != "false" {
			return nil, fmt.Errorf("invalid boolean value: %s", data)
		}
		return []string{"write", domain, keyName, "-bool", lower}, nil
	case "int", "integer":
		return []string{"write", domain, keyName, "-int", data}, nil
	case "float", "double":
		return []string{"write", domain, keyName, "-float", data}, nil
	case "data":
		return []string{"write", domain, keyName, "-data", data}, nil
	default:
		return []string{"write", domain, keyName, data}, nil
	}
}

func (p *nativeProvider) DeleteKey(ctx context.Context, req DeleteKeyRequest) (RegistryMutationResult, error) {
	return RegistryMutationResult{}, ErrNotSupported
}

func (p *nativeProvider) DeleteValue(ctx context.Context, req DeleteValueRequest) (RegistryMutationResult, error) {
	if err := p.ensureAvailable(); err != nil {
		return RegistryMutationResult{}, err
	}
	domain := strings.TrimSpace(req.KeyPath)
	if domain == "" {
		domain = strings.TrimSpace(req.Hive)
	}
	if domain == "" {
		return RegistryMutationResult{}, fmt.Errorf("defaults domain required")
	}
	keyName := strings.TrimSpace(req.Name)
	if keyName == "" {
		return RegistryMutationResult{}, fmt.Errorf("defaults key name required")
	}
	runner := p.runner()
	if _, err := runner.run(ctx, p.defaultsPath, "delete", domain, keyName); err != nil {
		return RegistryMutationResult{}, err
	}
	values, err := p.readDomain(ctx, runner, domain)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	owner := strings.TrimSpace(os.Getenv("USER"))
	if owner == "" {
		owner = "unknown"
	}
	snapshot := RegistryHive{domain: {
		Hive:         defaultsHiveName,
		Name:         domain,
		Path:         domain,
		ParentPath:   nil,
		Values:       values,
		SubKeys:      nil,
		LastModified: time.Now().UTC().Format(time.RFC3339Nano),
		Owner:        owner,
	}}
	return RegistryMutationResult{
		Hive:      snapshot,
		KeyPath:   domain,
		ValueName: stringPointer(keyName),
		MutatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}
