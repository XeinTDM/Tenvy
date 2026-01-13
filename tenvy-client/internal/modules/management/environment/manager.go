package environment

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type (
	Command       = protocol.Command
	CommandResult = protocol.CommandResult
)

type clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type Manager struct {
	mu       sync.Mutex
	history  map[string]time.Time
	scope    string
	timebase clock
}

type commandPayload struct {
	Action           string `json:"action"`
	Key              string `json:"key,omitempty"`
	Value            string `json:"value,omitempty"`
	Scope            string `json:"scope,omitempty"`
	RestartProcesses bool   `json:"restartProcesses,omitempty"`
}

type mutationResult struct {
	Key              string `json:"key"`
	Scope            string `json:"scope"`
	Value            string `json:"value,omitempty"`
	PreviousValue    string `json:"previousValue,omitempty"`
	Operation        string `json:"operation"`
	MutatedAt        string `json:"mutatedAt"`
	RestartRequested bool   `json:"restartRequested,omitempty"`
}

type snapshotResult struct {
	Variables  []variable `json:"variables"`
	Count      int        `json:"count"`
	CapturedAt string     `json:"capturedAt"`
}

type variable struct {
	Key            string `json:"key"`
	Value          string `json:"value"`
	Scope          string `json:"scope"`
	Length         int    `json:"length"`
	LastModifiedAt string `json:"lastModifiedAt,omitempty"`
}

func NewManager() *Manager {
	return &Manager{
		history:  make(map[string]time.Time),
		scope:    "user",
		timebase: systemClock{},
	}
}

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := CommandResult{CommandID: cmd.ID, CompletedAt: completedAt}

	var payload commandPayload
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("invalid environment payload: %v", err)
			return result
		}
	}

	action := strings.TrimSpace(strings.ToLower(payload.Action))
	switch action {
	case "list", "":
		if err := m.handleList(&result); err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
	case "set":
		if err := m.handleSet(&result, payload); err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
	case "remove":
		if err := m.handleRemove(&result, payload); err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
	default:
		result.Success = false
		result.Error = fmt.Sprintf("unsupported environment action: %s", payload.Action)
		return result
	}

	result.Success = true
	return result
}

func (m *Manager) handleList(result *CommandResult) error {
	snapshot := snapshotResult{CapturedAt: m.now().Format(time.RFC3339)}

	env := os.Environ()
	variables := make([]variable, 0, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		item := variable{Key: strings.ToUpper(key), Value: value, Scope: m.scope, Length: len(value)}
		if ts := m.lastMutated(item.Key); !ts.IsZero() {
			item.LastModifiedAt = ts.Format(time.RFC3339)
		}
		variables = append(variables, item)
	}

	sort.SliceStable(variables, func(i, j int) bool { return variables[i].Key < variables[j].Key })

	snapshot.Variables = variables
	snapshot.Count = len(variables)

	payload, err := json.Marshal(map[string]any{
		"action": "list",
		"status": "ok",
		"result": snapshot,
	})
	if err != nil {
		return err
	}
	result.Output = string(payload)
	return nil
}

func (m *Manager) handleSet(result *CommandResult, payload commandPayload) error {
	key := strings.TrimSpace(payload.Key)
	if key == "" {
		return fmt.Errorf("environment variable key is required")
	}

	scope := m.normalizeScope(payload.Scope)
	previous, hadPrevious := os.LookupEnv(key)
	if err := os.Setenv(key, payload.Value); err != nil {
		return fmt.Errorf("failed to set environment variable: %w", err)
	}
	m.markMutated(key)

	mutation := mutationResult{
		Key:              strings.ToUpper(key),
		Scope:            scope,
		Value:            payload.Value,
		Operation:        "set",
		MutatedAt:        m.now().Format(time.RFC3339),
		RestartRequested: payload.RestartProcesses,
	}
	if hadPrevious {
		mutation.PreviousValue = previous
	}

	output, err := json.Marshal(map[string]any{
		"action": "set",
		"status": "ok",
		"result": mutation,
	})
	if err != nil {
		return err
	}
	result.Output = string(output)
	return nil
}

func (m *Manager) handleRemove(result *CommandResult, payload commandPayload) error {
	key := strings.TrimSpace(payload.Key)
	if key == "" {
		return fmt.Errorf("environment variable key is required")
	}

	scope := m.normalizeScope(payload.Scope)
	previous, hadPrevious := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		return fmt.Errorf("failed to remove environment variable: %w", err)
	}
	m.markMutated(key)

	mutation := mutationResult{
		Key:       strings.ToUpper(key),
		Scope:     scope,
		Operation: "remove",
		MutatedAt: m.now().Format(time.RFC3339),
	}
	if hadPrevious {
		mutation.PreviousValue = previous
	}

	output, err := json.Marshal(map[string]any{
		"action": "remove",
		"status": "ok",
		"result": mutation,
	})
	if err != nil {
		return err
	}
	result.Output = string(output)
	return nil
}

func (m *Manager) normalizeScope(scope string) string {
	trimmed := strings.TrimSpace(strings.ToLower(scope))
	if trimmed == "machine" || trimmed == "system" {
		return "machine"
	}
	return "user"
}

func (m *Manager) markMutated(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history[strings.ToUpper(key)] = m.now()
}

func (m *Manager) lastMutated(key string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts, ok := m.history[strings.ToUpper(key)]
	if !ok {
		return time.Time{}
	}
	return ts
}

func (m *Manager) now() time.Time {
	if m.timebase == nil {
		m.timebase = systemClock{}
	}
	return m.timebase.Now()
}

func (m *Manager) setClock(c clock) {
	if c == nil {
		m.timebase = systemClock{}
		return
	}
	m.timebase = c
}
