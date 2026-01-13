package geolocation

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rootbay/tenvy-client/internal/modules/misc/geolocation/providers"
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
	mu              sync.Mutex
	clock           clock
	httpClient      *http.Client
	baseURL         string
	authKey         string
	providers       []string
	defaultProvider string
	last            *lookupResult
	resolvers       map[string]providers.Resolver
	providerConfig  map[string]providers.Config
}

type commandPayload struct {
	Action          string `json:"action"`
	IP              string `json:"ip,omitempty"`
	Provider        string `json:"provider,omitempty"`
	IncludeTimezone bool   `json:"includeTimezone,omitempty"`
	IncludeMap      bool   `json:"includeMap,omitempty"`
}

type lookupResult struct {
	IP          string    `json:"ip"`
	Provider    string    `json:"provider"`
	City        string    `json:"city,omitempty"`
	Region      string    `json:"region,omitempty"`
	Country     string    `json:"country"`
	CountryCode string    `json:"countryCode,omitempty"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	NetworkType string    `json:"networkType"`
	ISP         string    `json:"isp,omitempty"`
	ASN         string    `json:"asn,omitempty"`
	Timezone    *timezone `json:"timezone,omitempty"`
	MapURL      string    `json:"mapUrl,omitempty"`
	RetrievedAt string    `json:"retrievedAt"`
}

type timezone struct {
	ID           string `json:"id"`
	Offset       string `json:"offset"`
	Abbreviation string `json:"abbreviation,omitempty"`
}

type statusResult struct {
	LastLookup      *lookupResult `json:"lastLookup"`
	Providers       []string      `json:"providers"`
	DefaultProvider string        `json:"defaultProvider"`
	GeneratedAt     string        `json:"generatedAt"`
}

func NewManager(cfg Config, client *http.Client, baseURL, authKey string) *Manager {
	if client == nil {
		client = http.DefaultClient
	}
	providerList, defaultProvider, resolvers, providerConfigs := buildProviderState(cfg, client, baseURL, authKey)
	return &Manager{
		clock:           systemClock{},
		httpClient:      client,
		baseURL:         baseURL,
		authKey:         authKey,
		providers:       providerList,
		defaultProvider: defaultProvider,
		resolvers:       resolvers,
		providerConfig:  providerConfigs,
	}
}

func (m *Manager) ApplyConfig(cfg Config, client *http.Client, baseURL, authKey string) {
	if m == nil {
		return
	}
	if client == nil {
		client = m.httpClient
	}
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = m.baseURL
	}
	if authKey == "" {
		authKey = m.authKey
	}
	m.httpClient = client
	m.baseURL = baseURL
	m.authKey = authKey
	providerList, defaultProvider, resolvers, providerConfigs := buildProviderState(cfg, client, baseURL, authKey)
	m.providers = providerList
	m.defaultProvider = defaultProvider
	m.resolvers = resolvers
	m.providerConfig = providerConfigs
}

func buildProviderState(cfg Config, client *http.Client, baseURL, authKey string) ([]string, string, map[string]providers.Resolver, map[string]providers.Config) {
	normalized := cfg.withDefaults()

	knownResolvers := map[string]providers.Resolver{
		"ipinfo":  providers.IPInfo(),
		"maxmind": providers.MaxMind(),
		"db-ip":   providers.DBIP(),
		"tenvy":   providers.Tenvy(),
	}

	activeResolvers := make(map[string]providers.Resolver)
	providerConfigs := make(map[string]providers.Config)
	providerList := make([]string, 0, len(normalized.Providers))

	for _, name := range defaultProviderOrder {
		if pcfg, ok := normalized.Providers[name]; ok {
			if resolver, ok := knownResolvers[name]; ok {
				providerList = append(providerList, name)
				pcfg.HTTPClient = client
				pcfg.BaseURL = baseURL
				pcfg.AuthKey = authKey
				providerConfigs[name] = pcfg
				activeResolvers[name] = resolver
			}
		}
	}

	for name, pcfg := range normalized.Providers {
		if _, seen := providerConfigs[name]; seen {
			continue
		}
		if resolver, ok := knownResolvers[name]; ok {
			providerList = append(providerList, name)
			pcfg.HTTPClient = client
			pcfg.BaseURL = baseURL
			pcfg.AuthKey = authKey
			providerConfigs[name] = pcfg
			activeResolvers[name] = resolver
		}
	}

	defaultProvider := normalized.DefaultProvider
	if _, ok := activeResolvers[defaultProvider]; !ok {
		if len(providerList) > 0 {
			defaultProvider = providerList[0]
		} else {
			defaultProvider = ""
		}
	}

	return providerList, defaultProvider, activeResolvers, providerConfigs
}

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := CommandResult{CommandID: cmd.ID, CompletedAt: completedAt}

	var payload commandPayload
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("invalid geolocation payload: %v", err)
			return result
		}
	}

	action := strings.TrimSpace(strings.ToLower(payload.Action))
	if action == "" {
		action = "status"
	}

	switch action {
	case "status":
		if err := m.writeStatus(&result); err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
	case "lookup":
		if err := m.performLookup(ctx, payload, &result); err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
	default:
		result.Success = false
		result.Error = fmt.Sprintf("unsupported geolocation action: %s", payload.Action)
		return result
	}

	result.Success = true
	return result
}

func (m *Manager) writeStatus(result *CommandResult) error {
	status := statusResult{
		LastLookup:      m.lastLookup(),
		Providers:       append([]string(nil), m.providers...),
		DefaultProvider: m.defaultProvider,
		GeneratedAt:     m.now().Format(time.RFC3339),
	}

	payload, err := json.Marshal(map[string]any{
		"action": "status",
		"status": "ok",
		"result": status,
	})
	if err != nil {
		return err
	}
	result.Output = string(payload)
	return nil
}

func (m *Manager) performLookup(ctx context.Context, payload commandPayload, result *CommandResult) error {
	ip := strings.TrimSpace(payload.IP)
	if ip == "" {
		return fmt.Errorf("ip address is required")
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("invalid ip address")
	}

	provider := strings.ToLower(strings.TrimSpace(payload.Provider))
	if provider == "" {
		provider = m.defaultProvider
	}
	if !m.isProviderSupported(provider) {
		return fmt.Errorf("unsupported provider: %s", payload.Provider)
	}

	resolver := m.resolvers[provider]
	cfg := m.providerConfig[provider]

	lookupCtx := ctx
	var cancel context.CancelFunc
	if cfg.Timeout > 0 {
		lookupCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
	}
	if cancel != nil {
		defer cancel()
	}

	providerResult, err := resolver.Lookup(lookupCtx, parsed, cfg)
	if err != nil {
		return fmt.Errorf("provider %s lookup failed: %w", provider, err)
	}

	location := lookupResult{
		Provider:    provider,
		IP:          ip,
		City:        providerResult.City,
		Region:      providerResult.Region,
		Country:     providerResult.Country,
		CountryCode: providerResult.CountryCode,
		Latitude:    providerResult.Latitude,
		Longitude:   providerResult.Longitude,
		NetworkType: classifyNetwork(parsed),
		ISP:         providerResult.ISP,
		ASN:         providerResult.ASN,
	}

	if providerResult.Timezone != nil {
		location.Timezone = &timezone{
			ID:           providerResult.Timezone.ID,
			Offset:       providerResult.Timezone.Offset,
			Abbreviation: providerResult.Timezone.Abbreviation,
		}
	}

	if !payload.IncludeTimezone {
		location.Timezone = nil
	}
	if payload.IncludeMap {
		location.MapURL = buildGeolocationMapURL(location.Latitude, location.Longitude)
	}
	location.RetrievedAt = m.now().Format(time.RFC3339)

	m.storeLookup(&location)

	payloadBytes, err := json.Marshal(map[string]any{
		"action": "lookup",
		"status": "ok",
		"result": location,
	})
	if err != nil {
		return err
	}
	result.Output = string(payloadBytes)
	return nil
}

func (m *Manager) isProviderSupported(provider string) bool {
	_, ok := m.resolvers[provider]
	return ok
}

func (m *Manager) storeLookup(result *lookupResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clone := *result
	m.last = &clone
}

func (m *Manager) lastLookup() *lookupResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.last == nil {
		return nil
	}
	clone := *m.last
	return &clone
}

func (m *Manager) now() time.Time {
	if m.clock == nil {
		m.clock = systemClock{}
	}
	return m.clock.Now()
}

func (m *Manager) setClock(c clock) {
	if c == nil {
		m.clock = systemClock{}
		return
	}
	m.clock = c
}

func classifyNetwork(ip net.IP) string {
	if ip.IsLoopback() {
		return "loopback"
	}
	if ip.IsPrivate() {
		return "private"
	}
	if ip.IsMulticast() {
		return "multicast"
	}
	return "public"
}

const geolocationMapZoom = 9

func buildGeolocationMapURL(latitude, longitude float64) string {
	return fmt.Sprintf(
		"https://www.openstreetmap.org/?mlat=%.4f&mlon=%.4f#map=%d/%.4f/%.4f",
		latitude,
		longitude,
		geolocationMapZoom,
		latitude,
		longitude,
	)
}
