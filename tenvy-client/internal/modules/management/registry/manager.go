package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type (
	Command       = protocol.Command
	CommandResult = protocol.CommandResult
)

type Logger interface {
	Printf(format string, args ...interface{})
}

type Manager struct {
	provider Provider
	logger   Logger
	caps     ProviderCapabilities
}

func NewManager(logger Logger) *Manager {
	provider := newNativeProvider()
	return &Manager{
		provider: provider,
		logger:   logger,
		caps:     provider.Capabilities(),
	}
}

func (m *Manager) UpdateLogger(logger Logger) {
	m.logger = logger
}

func (m *Manager) SetProvider(provider Provider) {
	if provider == nil {
		m.provider = newNativeProvider()
		m.caps = m.provider.Capabilities()
		return
	}
	m.provider = provider
	m.caps = provider.Capabilities()
}

func (m *Manager) logf(format string, args ...interface{}) {
	if m.logger == nil {
		return
	}
	m.logger.Printf(format, args...)
}

func (m *Manager) Capabilities() ProviderCapabilities {
	if m == nil {
		return ProviderCapabilities{}
	}
	return m.caps
}

func NativeCapabilities() ProviderCapabilities {
	provider := newNativeProvider()
	if provider == nil {
		return ProviderCapabilities{}
	}
	return provider.Capabilities()
}

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := CommandResult{CommandID: cmd.ID, CompletedAt: completedAt}

	if len(cmd.Payload) == 0 {
		result.Success = false
		result.Error = "registry payload required"
		return result
	}

	var payload RegistryCommandPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("invalid registry payload: %v", err)
		return result
	}

	request := payload.Request
	switch request.Operation {
	case "list":
		response, err := m.provider.List(ctx, ListRequest{
			Hive:  strings.TrimSpace(request.Hive),
			Path:  strings.TrimSpace(request.Path),
			Depth: request.Depth,
		})
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		if err := m.setSuccessResult(&result, "list", response); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("encode response: %v", err)
			return result
		}
		return result
	case "create":
		switch strings.ToLower(request.Target) {
		case "key":
			if strings.TrimSpace(request.Name) == "" {
				result.Success = false
				result.Error = "registry key name required"
				return result
			}
			response, err := m.provider.CreateKey(ctx, CreateKeyRequest{
				Hive:       strings.TrimSpace(request.Hive),
				ParentPath: strings.TrimSpace(request.ParentPath),
				Name:       strings.TrimSpace(request.Name),
			})
			if err != nil {
				result.Success = false
				result.Error = err.Error()
				return result
			}
			if err := m.setSuccessResult(&result, "create", response); err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("encode response: %v", err)
				return result
			}
			return result
		case "value":
			if request.Value == nil {
				result.Success = false
				result.Error = "registry value payload required"
				return result
			}
			if strings.TrimSpace(request.KeyPath) == "" {
				result.Success = false
				result.Error = "registry key path required"
				return result
			}
			response, err := m.provider.CreateValue(ctx, CreateValueRequest{
				Hive:    strings.TrimSpace(request.Hive),
				KeyPath: strings.TrimSpace(request.KeyPath),
				Value:   *request.Value,
			})
			if err != nil {
				result.Success = false
				result.Error = err.Error()
				return result
			}
			if err := m.setSuccessResult(&result, "create", response); err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("encode response: %v", err)
				return result
			}
			return result
		default:
			result.Success = false
			result.Error = fmt.Sprintf("unsupported registry create target %q", request.Target)
			return result
		}
	case "update":
		switch strings.ToLower(request.Target) {
		case "key":
			if strings.TrimSpace(request.Path) == "" {
				result.Success = false
				result.Error = "registry key path required"
				return result
			}
			if strings.TrimSpace(request.Name) == "" {
				result.Success = false
				result.Error = "registry key name required"
				return result
			}
			response, err := m.provider.UpdateKey(ctx, UpdateKeyRequest{
				Hive: strings.TrimSpace(request.Hive),
				Path: strings.TrimSpace(request.Path),
				Name: strings.TrimSpace(request.Name),
			})
			if err != nil {
				result.Success = false
				result.Error = err.Error()
				return result
			}
			if err := m.setSuccessResult(&result, "update", response); err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("encode response: %v", err)
				return result
			}
			return result
		case "value":
			if request.Value == nil {
				result.Success = false
				result.Error = "registry value payload required"
				return result
			}
			if strings.TrimSpace(request.KeyPath) == "" {
				result.Success = false
				result.Error = "registry key path required"
				return result
			}
			response, err := m.provider.UpdateValue(ctx, UpdateValueRequest{
				Hive:         strings.TrimSpace(request.Hive),
				KeyPath:      strings.TrimSpace(request.KeyPath),
				Value:        *request.Value,
				OriginalName: strings.TrimSpace(request.OriginalName),
			})
			if err != nil {
				result.Success = false
				result.Error = err.Error()
				return result
			}
			if err := m.setSuccessResult(&result, "update", response); err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("encode response: %v", err)
				return result
			}
			return result
		default:
			result.Success = false
			result.Error = fmt.Sprintf("unsupported registry update target %q", request.Target)
			return result
		}
	case "delete":
		switch strings.ToLower(request.Target) {
		case "key":
			if strings.TrimSpace(request.Path) == "" {
				result.Success = false
				result.Error = "registry key path required"
				return result
			}
			response, err := m.provider.DeleteKey(ctx, DeleteKeyRequest{
				Hive: strings.TrimSpace(request.Hive),
				Path: strings.TrimSpace(request.Path),
			})
			if err != nil {
				result.Success = false
				result.Error = err.Error()
				return result
			}
			if err := m.setSuccessResult(&result, "delete", response); err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("encode response: %v", err)
				return result
			}
			return result
		case "value":
			if strings.TrimSpace(request.KeyPath) == "" {
				result.Success = false
				result.Error = "registry key path required"
				return result
			}
			if strings.TrimSpace(request.Name) == "" {
				result.Success = false
				result.Error = "registry value name required"
				return result
			}
			response, err := m.provider.DeleteValue(ctx, DeleteValueRequest{
				Hive:    strings.TrimSpace(request.Hive),
				KeyPath: strings.TrimSpace(request.KeyPath),
				Name:    strings.TrimSpace(request.Name),
			})
			if err != nil {
				result.Success = false
				result.Error = err.Error()
				return result
			}
			if err := m.setSuccessResult(&result, "delete", response); err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("encode response: %v", err)
				return result
			}
			return result
		default:
			result.Success = false
			result.Error = fmt.Sprintf("unsupported registry delete target %q", request.Target)
			return result
		}
	default:
		result.Success = false
		result.Error = fmt.Sprintf("unsupported registry operation %q", request.Operation)
		return result
	}
}

func (m *Manager) setSuccessResult(result *CommandResult, operation string, payload interface{}) error {
	encoded, err := json.Marshal(RegistryCommandResponse{
		Operation: operation,
		Status:    "ok",
		Result:    payload,
	})
	if err != nil {
		return err
	}
	result.Success = true
	result.Output = string(encoded)
	return nil
}

type Provider interface {
	List(ctx context.Context, req ListRequest) (RegistryListResult, error)
	CreateKey(ctx context.Context, req CreateKeyRequest) (RegistryMutationResult, error)
	CreateValue(ctx context.Context, req CreateValueRequest) (RegistryMutationResult, error)
	UpdateKey(ctx context.Context, req UpdateKeyRequest) (RegistryMutationResult, error)
	UpdateValue(ctx context.Context, req UpdateValueRequest) (RegistryMutationResult, error)
	DeleteKey(ctx context.Context, req DeleteKeyRequest) (RegistryMutationResult, error)
	DeleteValue(ctx context.Context, req DeleteValueRequest) (RegistryMutationResult, error)
	Capabilities() ProviderCapabilities
}

var ErrNotSupported = errors.New("registry operations not supported on this platform")

type ProviderCapabilities struct {
	Enumerate bool
	Mutate    bool
}

type RegistryCommandPayload struct {
	Request RegistryCommandRequest `json:"request"`
}

type RegistryCommandRequest struct {
	Operation    string              `json:"operation"`
	Target       string              `json:"target,omitempty"`
	Hive         string              `json:"hive,omitempty"`
	Path         string              `json:"path,omitempty"`
	ParentPath   string              `json:"parentPath,omitempty"`
	Name         string              `json:"name,omitempty"`
	KeyPath      string              `json:"keyPath,omitempty"`
	Value        *RegistryValueInput `json:"value,omitempty"`
	OriginalName string              `json:"originalName,omitempty"`
	Depth        int                 `json:"depth,omitempty"`
}

type RegistryListResult struct {
	Snapshot    RegistrySnapshot `json:"snapshot"`
	GeneratedAt string           `json:"generatedAt"`
}

type RegistryMutationResult struct {
	Hive      RegistryHive `json:"hive"`
	KeyPath   string       `json:"keyPath"`
	ValueName *string      `json:"valueName,omitempty"`
	MutatedAt string       `json:"mutatedAt"`
}

type RegistryCommandResponse struct {
	Operation string      `json:"operation"`
	Status    string      `json:"status"`
	Result    interface{} `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
	Code      string      `json:"code,omitempty"`
	Details   interface{} `json:"details,omitempty"`
}

type RegistryValueInput struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Data        string  `json:"data"`
	Description *string `json:"description,omitempty"`
}

type RegistryValue struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Data         string  `json:"data"`
	Size         int     `json:"size"`
	LastModified string  `json:"lastModified"`
	Description  *string `json:"description,omitempty"`
}

type RegistryKey struct {
	Hive          string          `json:"hive"`
	Name          string          `json:"name"`
	Path          string          `json:"path"`
	ParentPath    *string         `json:"parentPath"`
	Values        []RegistryValue `json:"values"`
	SubKeys       []string        `json:"subKeys"`
	LastModified  string          `json:"lastModified"`
	Wow64Mirrored bool            `json:"wow64Mirrored"`
	Owner         string          `json:"owner"`
	Description   *string         `json:"description,omitempty"`
}

type RegistryHive map[string]RegistryKey

type RegistrySnapshot map[string]RegistryHive

type ListRequest struct {
	Hive  string
	Path  string
	Depth int
}

type CreateKeyRequest struct {
	Hive       string
	ParentPath string
	Name       string
}

type CreateValueRequest struct {
	Hive    string
	KeyPath string
	Value   RegistryValueInput
}

type UpdateKeyRequest struct {
	Hive string
	Path string
	Name string
}

type UpdateValueRequest struct {
	Hive         string
	KeyPath      string
	Value        RegistryValueInput
	OriginalName string
}

type DeleteKeyRequest struct {
	Hive string
	Path string
}

type DeleteValueRequest struct {
	Hive    string
	KeyPath string
	Name    string
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
