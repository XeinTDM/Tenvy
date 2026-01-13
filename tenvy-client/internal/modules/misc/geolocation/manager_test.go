package geolocation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rootbay/tenvy-client/internal/modules/misc/geolocation/providers"
	"github.com/rootbay/tenvy-client/internal/protocol"
)

type stubClock struct {
	now time.Time
}

func (c *stubClock) Now() time.Time {
	if c.now.IsZero() {
		c.now = time.Date(2024, 6, 1, 8, 0, 0, 0, time.UTC)
	}
	return c.now
}

func TestManagerStatus(t *testing.T) {
	manager := NewManager(defaultTestConfig(), nil, "", "")
	manager.setClock(&stubClock{})

	result := manager.HandleCommand(context.Background(), protocol.Command{ID: "status"})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Error)
	}

	var payload struct {
		Action string       `json:"action"`
		Status string       `json:"status"`
		Result statusResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Action != "status" {
		t.Fatalf("expected status action, got %s", payload.Action)
	}
	if payload.Result.DefaultProvider != "ipinfo" {
		t.Fatalf("unexpected default provider %s", payload.Result.DefaultProvider)
	}
	if len(payload.Result.Providers) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(payload.Result.Providers))
	}
}

func TestManagerLookupSuccess(t *testing.T) {
	manager := NewManager(defaultTestConfig(), nil, "", "")
	manager.setClock(&stubClock{})

	payload, err := json.Marshal(commandPayload{Action: "lookup", IP: "203.0.113.10", Provider: "maxmind", IncludeTimezone: true, IncludeMap: true})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	result := manager.HandleCommand(context.Background(), protocol.Command{ID: "lookup", Payload: payload})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Error)
	}

	var payloadEnvelope struct {
		Action string       `json:"action"`
		Status string       `json:"status"`
		Result lookupResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(result.Output), &payloadEnvelope); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payloadEnvelope.Action != "lookup" {
		t.Fatalf("expected lookup action, got %s", payloadEnvelope.Action)
	}
	if payloadEnvelope.Result.Provider != "maxmind" {
		t.Fatalf("expected maxmind provider, got %s", payloadEnvelope.Result.Provider)
	}
	if payloadEnvelope.Result.Timezone == nil {
		t.Fatalf("expected timezone details")
	}
	if payloadEnvelope.Result.MapURL == "" {
		t.Fatalf("expected map url in result")
	}

	last := manager.lastLookup()
	if last == nil {
		t.Fatalf("expected cached lookup result")
	}
	if last.Provider != payloadEnvelope.Result.Provider {
		t.Fatalf("cache mismatch: %s vs %s", last.Provider, payloadEnvelope.Result.Provider)
	}
}

func TestManagerLookupProviderFailure(t *testing.T) {
	cfg := Config{
		DefaultProvider: "maxmind",
		Providers: map[string]providers.Config{
			"maxmind": {Timeout: time.Second},
		},
	}
	manager := NewManager(cfg, nil, "", "")
	manager.setClock(&stubClock{})

	payload, err := json.Marshal(commandPayload{Action: "lookup", IP: "198.51.100.10", Provider: "maxmind"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	result := manager.HandleCommand(context.Background(), protocol.Command{ID: "lookup", Payload: payload})
	if result.Success {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(result.Error, "lookup failed") {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if last := manager.lastLookup(); last != nil {
		t.Fatalf("expected no cached result on failure")
	}
}

func TestManagerLookupUnsupportedProvider(t *testing.T) {
	manager := NewManager(defaultTestConfig(), nil, "", "")
	manager.setClock(&stubClock{})

	payload, err := json.Marshal(commandPayload{Action: "lookup", IP: "192.0.2.30", Provider: "unknown"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	result := manager.HandleCommand(context.Background(), protocol.Command{ID: "lookup", Payload: payload})
	if result.Success {
		t.Fatalf("expected failure for unsupported provider")
	}
	if !strings.Contains(result.Error, "unsupported provider") {
		t.Fatalf("unexpected error message: %s", result.Error)
	}
}

func defaultTestConfig() Config {
	return Config{
		DefaultProvider: "ipinfo",
		Providers: map[string]providers.Config{
			"ipinfo":  {APIKey: "ipinfo-key", Timeout: time.Second},
			"maxmind": {APIKey: "maxmind-key", Timeout: time.Second},
			"db-ip":   {APIKey: "dbip-key", Timeout: time.Second},
		},
	}
}
