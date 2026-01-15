package startupengine

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

type StartupScope string

type StartupSource string

type StartupImpact string

const (
	ScopeMachine       StartupScope = "machine"
	ScopeUser          StartupScope = "user"
	ScopeScheduledTask StartupScope = "scheduled-task"

	SourceRegistry      StartupSource = "registry"
	SourceStartupFolder StartupSource = "startup-folder"
	SourceScheduledTask StartupSource = "scheduled-task"
	SourceService       StartupSource = "service"
	SourceOther         StartupSource = "other"

	ImpactLow         StartupImpact = "low"
	ImpactMedium      StartupImpact = "medium"
	ImpactHigh        StartupImpact = "high"
	ImpactNotMeasured StartupImpact = "not-measured"
)

type Entry struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Path            string                 `json:"path"`
	Arguments       string                 `json:"arguments,omitempty"`
	Enabled         bool                   `json:"enabled"`
	Scope           StartupScope           `json:"scope"`
	Source          StartupSource          `json:"source"`
	Impact          StartupImpact          `json:"impact"`
	Publisher       string                 `json:"publisher,omitempty"`
	Description     string                 `json:"description,omitempty"`
	Location        string                 `json:"location"`
	StartupTime     int64                  `json:"startupTime"`
	LastEvaluatedAt string                 `json:"lastEvaluatedAt"`
	LastRunAt       *string                `json:"lastRunAt,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

type TelemetrySummary struct {
	Total        int                   `json:"total"`
	Enabled      int                   `json:"enabled"`
	Disabled     int                   `json:"disabled"`
	ImpactCounts map[StartupImpact]int `json:"impactCounts,omitempty"`
	ScopeCounts  map[StartupScope]int  `json:"scopeCounts,omitempty"`
}

type Inventory struct {
	Entries     []Entry           `json:"entries"`
	GeneratedAt string            `json:"generatedAt"`
	Summary     *TelemetrySummary `json:"summary,omitempty"`
}

type EntryDefinition struct {
	Name        string        `json:"name"`
	Path        string        `json:"path"`
	Arguments   string        `json:"arguments,omitempty"`
	Scope       StartupScope  `json:"scope"`
	Source      StartupSource `json:"source"`
	Location    string        `json:"location"`
	Enabled     bool          `json:"enabled"`
	Publisher   string        `json:"publisher,omitempty"`
	Description string        `json:"description,omitempty"`
}

type ListRequest struct {
	Refresh bool
}

type ToggleRequest struct {
	EntryID string
	Enabled bool
}

type CreateRequest struct {
	Definition EntryDefinition
}

type RemoveRequest struct {
	EntryID string
}

type RemoveResult struct {
	EntryID string `json:"entryId"`
}

type CommandRequest struct {
	Operation  string           `json:"operation"`
	EntryID    string           `json:"entryId,omitempty"`
	Enabled    *bool            `json:"enabled,omitempty"`
	Definition *EntryDefinition `json:"definition,omitempty"`
	Refresh    bool             `json:"refresh,omitempty"`
}

type CommandPayload struct {
	Request CommandRequest `json:"request"`
}

type CommandResponse struct {
	Operation string      `json:"operation"`
	Status    string      `json:"status"`
	Result    interface{} `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
	Code      string      `json:"code,omitempty"`
	Details   interface{} `json:"details,omitempty"`
}

type Provider interface {
	List(ctx context.Context, req ListRequest) (Inventory, error)
	Toggle(ctx context.Context, req ToggleRequest) (Entry, error)
	Create(ctx context.Context, req CreateRequest) (Entry, error)
	Remove(ctx context.Context, req RemoveRequest) (RemoveResult, error)
	Capabilities() ProviderCapabilities
}

var ErrNotSupported = errors.New("startup operations not supported on this platform")

type ProviderCapabilities struct {
	Enumerate bool
	Manage    bool
}

func NativeCapabilities() ProviderCapabilities {
	provider := newNativeProvider()
	if provider == nil {
		return ProviderCapabilities{}
	}
	return provider.Capabilities()
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

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := CommandResult{CommandID: cmd.ID, CompletedAt: completedAt}

	if len(cmd.Payload) == 0 {
		result.Success = false
		result.Error = "startup manager payload required"
		return result
	}

	var payload CommandPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("invalid startup manager payload: %v", err)
		return result
	}

	request := payload.Request
	operation := strings.ToLower(strings.TrimSpace(request.Operation))
	switch operation {
	case "list":
		inventory, err := m.provider.List(ctx, ListRequest{Refresh: request.Refresh})
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		if err := m.setSuccessResult(&result, operation, inventory); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("encode response: %v", err)
			return result
		}
		result.Success = true
		return result
	case "toggle":
		entryID := strings.TrimSpace(request.EntryID)
		if entryID == "" || request.Enabled == nil {
			result.Success = false
			result.Error = "startup toggle requires entry id and enabled flag"
			return result
		}
		updated, err := m.provider.Toggle(ctx, ToggleRequest{EntryID: entryID, Enabled: *request.Enabled})
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		if err := m.setSuccessResult(&result, operation, updated); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("encode response: %v", err)
			return result
		}
		result.Success = true
		return result
	case "create":
		if request.Definition == nil {
			result.Success = false
			result.Error = "startup entry definition required"
			return result
		}
		definition := normalizeDefinition(*request.Definition)
		created, err := m.provider.Create(ctx, CreateRequest{Definition: definition})
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		if err := m.setSuccessResult(&result, operation, created); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("encode response: %v", err)
			return result
		}
		result.Success = true
		return result
	case "remove":
		entryID := strings.TrimSpace(request.EntryID)
		if entryID == "" {
			result.Success = false
			result.Error = "startup entry id required"
			return result
		}
		removed, err := m.provider.Remove(ctx, RemoveRequest{EntryID: entryID})
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		if err := m.setSuccessResult(&result, operation, removed); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("encode response: %v", err)
			return result
		}
		result.Success = true
		return result
	default:
		result.Success = false
		result.Error = fmt.Sprintf("unsupported startup manager operation: %s", request.Operation)
		return result
	}
}

func (m *Manager) setSuccessResult(result *CommandResult, operation string, payload interface{}) error {
	response := CommandResponse{
		Operation: operation,
		Status:    "ok",
		Result:    payload,
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	result.Success = true
	result.Output = string(data)
	return nil
}

func normalizeDefinition(def EntryDefinition) EntryDefinition {
	def.Name = strings.TrimSpace(def.Name)
	def.Path = strings.TrimSpace(def.Path)
	def.Arguments = strings.TrimSpace(def.Arguments)
	def.Location = strings.TrimSpace(def.Location)
	def.Publisher = strings.TrimSpace(def.Publisher)
	def.Description = strings.TrimSpace(def.Description)

	scope := strings.ToLower(strings.TrimSpace(string(def.Scope)))
	switch scope {
	case string(ScopeMachine):
		def.Scope = ScopeMachine
	case string(ScopeScheduledTask):
		def.Scope = ScopeScheduledTask
	default:
		def.Scope = ScopeUser
	}

	source := strings.ToLower(strings.TrimSpace(string(def.Source)))
	switch source {
	case string(SourceRegistry), string(SourceStartupFolder), string(SourceScheduledTask), string(SourceService), string(SourceOther):
		def.Source = StartupSource(source)
	default:
		def.Source = SourceRegistry
	}

	return def
}
