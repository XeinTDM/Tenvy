package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	gnet "github.com/shirou/gopsutil/v3/net"
	gproc "github.com/shirou/gopsutil/v3/process"

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

const (
	maxStoredEvents = 64
	hostResolveTTL  = 5 * time.Minute
)

type Manager struct {
	mu    sync.Mutex
	clock clock

	started time.Time
	config  monitorConfig

	processes   processEnumerator
	connections connectionEnumerator
	resolver    hostResolver

	states   map[string]*entryState
	events   []alertEvent
	sequence int64
}

type monitorConfig struct {
	Feed               string       `json:"feed"`
	RefreshSeconds     int          `json:"refreshSeconds"`
	IncludeScreenshots bool         `json:"includeScreenshots"`
	IncludeCommands    bool         `json:"includeCommands"`
	Watchlist          []watchEntry `json:"watchlist"`
	LastUpdatedAt      string       `json:"lastUpdatedAt"`
}

type commandPayload struct {
	Action string         `json:"action"`
	Config monitorCommand `json:"config,omitempty"`
}

type monitorCommand struct {
	Feed               string       `json:"feed"`
	RefreshSeconds     int          `json:"refreshSeconds"`
	IncludeScreenshots bool         `json:"includeScreenshots"`
	IncludeCommands    bool         `json:"includeCommands"`
	Watchlist          []watchEntry `json:"watchlist"`
}

type watchEntry struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	DisplayName  string `json:"displayName"`
	AlertOnOpen  bool   `json:"alertOnOpen"`
	AlertOnClose bool   `json:"alertOnClose"`
}

type metric struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value string `json:"value"`
}

type alertEvent struct {
	ID          string `json:"id"`
	EntryID     string `json:"entryId"`
	EntryKind   string `json:"entryKind"`
	DisplayName string `json:"displayName"`
	Event       string `json:"event"`
	ObservedAt  string `json:"observedAt"`
	Detail      string `json:"detail,omitempty"`
}

type statusResult struct {
	Config      monitorConfig `json:"config"`
	Metrics     []metric      `json:"metrics"`
	Events      []alertEvent  `json:"events"`
	GeneratedAt string        `json:"generatedAt"`
}

type telemetrySnapshot struct {
	config    monitorConfig
	metrics   []metric
	events    []alertEvent
	generated time.Time
}

type processEnumerator interface {
	Processes(ctx context.Context) ([]processSample, error)
}

type connectionEnumerator interface {
	Connections(ctx context.Context) ([]connectionSample, error)
}

type hostResolver interface {
	LookupIP(ctx context.Context, host string) ([]string, error)
}

type processSample struct {
	PID         int32
	Name        string
	Executable  string
	CommandLine string
}

type connectionSample struct {
	PID        int32
	RemoteIP   string
	RemotePort uint32
	Status     string
}

type processInstance struct {
	PID           int32
	Name          string
	Executable    string
	CommandLine   string
	FirstObserved time.Time
}

type connectionInstance struct {
	Key           string
	PID           int32
	RemoteIP      string
	RemotePort    uint32
	Status        string
	FirstObserved time.Time
}

type entryState struct {
	Entry watchEntry

	ProcessPIDs map[int32]processInstance
	Connections map[string]connectionInstance

	TotalOpens  int
	TotalCloses int

	LastEventKind   string
	LastEventAt     time.Time
	LastEventDetail string

	Host             string
	ResolvedIPs      []string
	LastResolved     time.Time
	LastResolveError string
}

func NewManager() *Manager {
	now := time.Now().UTC()
	return &Manager{
		clock:   systemClock{},
		started: now,
		config: monitorConfig{
			Feed:               "live",
			RefreshSeconds:     5,
			IncludeScreenshots: false,
			IncludeCommands:    true,
			Watchlist:          []watchEntry{},
			LastUpdatedAt:      now.Format(time.RFC3339),
		},
		processes:   gopsutilProcessEnumerator{},
		connections: gopsutilConnectionEnumerator{},
		resolver:    defaultHostResolver{},
		states:      make(map[string]*entryState),
	}
}

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := CommandResult{CommandID: cmd.ID, CompletedAt: completedAt}

	var payload commandPayload
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("invalid trigger monitor payload: %v", err)
			return result
		}
	}

	action := strings.TrimSpace(strings.ToLower(payload.Action))
	if action == "" {
		action = "status"
	}

	switch action {
	case "status":
		status, err := m.buildStatus(ctx)
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		payload, err := json.Marshal(map[string]any{
			"action": "status",
			"status": "ok",
			"result": status,
		})
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		result.Output = string(payload)
	case "configure":
		status, err := m.applyConfig(ctx, payload.Config)
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		payload, err := json.Marshal(map[string]any{
			"action": "configure",
			"status": "ok",
			"result": status,
		})
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		result.Output = string(payload)
	default:
		result.Success = false
		result.Error = fmt.Sprintf("unsupported trigger monitor action: %s", payload.Action)
		return result
	}

	result.Success = true
	return result
}

func (m *Manager) buildStatus(ctx context.Context) (statusResult, error) {
	snapshot, err := m.collectTelemetry(ctx)
	if err != nil {
		return statusResult{}, err
	}
	return statusResult{
		Config:      snapshot.config,
		Metrics:     snapshot.metrics,
		Events:      snapshot.events,
		GeneratedAt: snapshot.generated.Format(time.RFC3339),
	}, nil
}

func (m *Manager) applyConfig(ctx context.Context, cfg monitorCommand) (statusResult, error) {
	feed := strings.ToLower(strings.TrimSpace(cfg.Feed))
	if feed != "batch" {
		feed = "live"
	}
	refresh := cfg.RefreshSeconds
	if refresh <= 0 {
		refresh = 5
	}
	if feed == "live" && refresh < 2 {
		refresh = 2
	}
	if feed == "batch" && refresh < 30 {
		refresh = 30
	}

	updated := monitorConfig{
		Feed:               feed,
		RefreshSeconds:     refresh,
		IncludeScreenshots: cfg.IncludeScreenshots,
		IncludeCommands:    cfg.IncludeCommands,
		Watchlist:          sanitizeWatchlist(cfg.Watchlist),
		LastUpdatedAt:      m.now().Format(time.RFC3339),
	}

	m.mu.Lock()
	m.config = updated
	m.resetStatesLocked(updated.Watchlist)
	m.mu.Unlock()

	return m.buildStatus(ctx)
}

func (m *Manager) collectTelemetry(ctx context.Context) (telemetrySnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()

	appMetrics := m.observeAppEntriesLocked(ctx, now)
	urlMetrics := m.observeURLEntriesLocked(ctx, now)

	opens, closes := m.totalAlertCountsLocked()

	metrics := make([]metric, 0, 2+len(appMetrics)+len(urlMetrics))
	metrics = append(metrics, metric{
		ID:    "watchlist-total",
		Label: "Watchlist entries",
		Value: fmt.Sprintf("%d", len(m.config.Watchlist)),
	})
	metrics = append(metrics, metric{
		ID:    "alerts-total",
		Label: "Alerts generated",
		Value: fmt.Sprintf("Open %d · Close %d", opens, closes),
	})
	metrics = append(metrics, appMetrics...)
	metrics = append(metrics, urlMetrics...)

	cfg := m.config
	cfg.Watchlist = cloneWatchlist(cfg.Watchlist)

	snapshot := telemetrySnapshot{
		config:    cfg,
		metrics:   metrics,
		events:    cloneEvents(m.events),
		generated: now,
	}
	return snapshot, nil
}

func (m *Manager) observeAppEntriesLocked(ctx context.Context, now time.Time) []metric {
	entries := filterWatchlistByKind(m.config.Watchlist, "app")
	if len(entries) == 0 {
		m.clearAppStatesLocked()
		return nil
	}

	sort.Slice(entries, func(i, j int) bool {
		return entryKey(entries[i]) < entryKey(entries[j])
	})

	enumerator := m.processes
	if enumerator == nil {
		enumerator = gopsutilProcessEnumerator{}
		m.processes = enumerator
	}

	samples, err := enumerator.Processes(ctx)
	if err != nil {
		metrics := make([]metric, 0, len(entries))
		value := fmt.Sprintf("Observation failed: %v", err)
		for _, entry := range entries {
			state := m.ensureStateLocked(entry)
			state.ProcessPIDs = make(map[int32]processInstance)
			metrics = append(metrics, metric{
				ID:    entryKey(entry),
				Label: fmt.Sprintf("%s (App)", entry.DisplayName),
				Value: value,
			})
		}
		return metrics
	}

	metrics := make([]metric, 0, len(entries))
	for _, entry := range entries {
		state := m.ensureStateLocked(entry)
		newSet := make(map[int32]processInstance)

		for _, sample := range samples {
			if !matchAppEntry(entry, sample) {
				continue
			}
			instance := processInstance{
				PID:           sample.PID,
				Name:          sample.Name,
				Executable:    sample.Executable,
				CommandLine:   sample.CommandLine,
				FirstObserved: now,
			}
			if previous, ok := state.ProcessPIDs[sample.PID]; ok {
				instance.FirstObserved = previous.FirstObserved
			}
			newSet[sample.PID] = instance
		}

		for pid, instance := range newSet {
			if _, ok := state.ProcessPIDs[pid]; ok {
				continue
			}
			state.TotalOpens++
			m.recordAlertLocked(state, "open", formatProcessDetail(instance), now, entry.AlertOnOpen)
		}
		for pid, instance := range state.ProcessPIDs {
			if _, ok := newSet[pid]; ok {
				continue
			}
			state.TotalCloses++
			m.recordAlertLocked(state, "close", formatProcessDetail(instance), now, entry.AlertOnClose)
		}

		state.ProcessPIDs = newSet

		metrics = append(metrics, metric{
			ID:    entryKey(entry),
			Label: fmt.Sprintf("%s (App)", entry.DisplayName),
			Value: formatEntryMetric(len(state.ProcessPIDs), state.TotalOpens, state.TotalCloses, state.LastEventKind, state.LastEventAt, state.LastEventDetail),
		})
	}

	return metrics
}

func (m *Manager) observeURLEntriesLocked(ctx context.Context, now time.Time) []metric {
	entries := filterWatchlistByKind(m.config.Watchlist, "url")
	if len(entries) == 0 {
		m.clearURLStatesLocked()
		return nil
	}

	sort.Slice(entries, func(i, j int) bool {
		return entryKey(entries[i]) < entryKey(entries[j])
	})

	enumerator := m.connections
	if enumerator == nil {
		enumerator = gopsutilConnectionEnumerator{}
		m.connections = enumerator
	}

	samples, err := enumerator.Connections(ctx)
	if err != nil {
		metrics := make([]metric, 0, len(entries))
		value := fmt.Sprintf("Observation failed: %v", err)
		for _, entry := range entries {
			state := m.ensureStateLocked(entry)
			state.Connections = make(map[string]connectionInstance)
			metrics = append(metrics, metric{
				ID:    entryKey(entry),
				Label: fmt.Sprintf("%s (URL)", entry.DisplayName),
				Value: value,
			})
		}
		return metrics
	}

	index := make(map[string][]connectionSample)
	for _, sample := range samples {
		remote := strings.ToLower(strings.TrimSpace(sample.RemoteIP))
		if remote == "" {
			continue
		}
		index[remote] = append(index[remote], sample)
	}

	metrics := make([]metric, 0, len(entries))
	for _, entry := range entries {
		state := m.ensureStateLocked(entry)
		host := extractHost(entry.ID)
		if host == "" {
			state.Connections = make(map[string]connectionInstance)
			metrics = append(metrics, metric{
				ID:    fmt.Sprintf("url:%s", entryKey(entry)),
				Label: fmt.Sprintf("%s (URL)", entry.DisplayName),
				Value: "Invalid URL identifier",
			})
			continue
		}

		ips, resolveErr := m.lookupHostLocked(ctx, state, host, now)
		if resolveErr != nil {
			state.Connections = make(map[string]connectionInstance)
			metrics = append(metrics, metric{
				ID:    entryKey(entry),
				Label: fmt.Sprintf("%s (URL)", entry.DisplayName),
				Value: fmt.Sprintf("Resolution failed: %v", resolveErr),
			})
			continue
		}

		newConnections := make(map[string]connectionInstance)
		for _, ip := range ips {
			for _, sample := range index[ip] {
				if !isActiveConnection(sample.Status) {
					continue
				}
				key := connectionKey(sample)
				instance := connectionInstance{
					Key:           key,
					PID:           sample.PID,
					RemoteIP:      sample.RemoteIP,
					RemotePort:    sample.RemotePort,
					Status:        sample.Status,
					FirstObserved: now,
				}
				if previous, ok := state.Connections[key]; ok {
					instance.FirstObserved = previous.FirstObserved
				}
				newConnections[key] = instance
			}
		}

		for key, instance := range newConnections {
			if _, ok := state.Connections[key]; ok {
				continue
			}
			state.TotalOpens++
			detail := formatConnectionDetail(instance)
			m.recordAlertLocked(state, "open", detail, now, entry.AlertOnOpen)
		}
		for key, instance := range state.Connections {
			if _, ok := newConnections[key]; ok {
				continue
			}
			state.TotalCloses++
			detail := formatConnectionDetail(instance)
			m.recordAlertLocked(state, "close", detail, now, entry.AlertOnClose)
		}

		state.Connections = newConnections

		metrics = append(metrics, metric{
			ID:    entryKey(entry),
			Label: fmt.Sprintf("%s (URL)", entry.DisplayName),
			Value: formatEntryMetric(len(state.Connections), state.TotalOpens, state.TotalCloses, state.LastEventKind, state.LastEventAt, state.LastEventDetail),
		})
	}

	return metrics
}

func (m *Manager) lookupHostLocked(ctx context.Context, state *entryState, host string, now time.Time) ([]string, error) {
	if host == "" {
		return nil, fmt.Errorf("empty host")
	}
	if state.Host == host && len(state.ResolvedIPs) > 0 && now.Sub(state.LastResolved) < hostResolveTTL && state.LastResolveError == "" {
		return state.ResolvedIPs, nil
	}

	resolver := m.resolver
	if resolver == nil {
		resolver = defaultHostResolver{}
		m.resolver = resolver
	}

	ips, err := resolver.LookupIP(ctx, host)
	state.Host = host
	state.LastResolved = now
	if err != nil {
		state.ResolvedIPs = nil
		state.LastResolveError = err.Error()
		return nil, err
	}

	normalized := make([]string, 0, len(ips))
	for _, ip := range ips {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(ip)))
	}

	state.ResolvedIPs = normalized
	state.LastResolveError = ""
	return normalized, nil
}

func (m *Manager) recordAlertLocked(state *entryState, event string, detail string, now time.Time, shouldAlert bool) {
	state.LastEventKind = event
	state.LastEventAt = now
	state.LastEventDetail = detail
	if !shouldAlert {
		return
	}

	m.sequence++
	eventID := fmt.Sprintf("%s:%d", entryKey(state.Entry), m.sequence)
	alert := alertEvent{
		ID:          eventID,
		EntryID:     state.Entry.ID,
		EntryKind:   state.Entry.Kind,
		DisplayName: state.Entry.DisplayName,
		Event:       event,
		ObservedAt:  now.Format(time.RFC3339),
		Detail:      detail,
	}
	m.events = append([]alertEvent{alert}, m.events...)
	if len(m.events) > maxStoredEvents {
		m.events = m.events[:maxStoredEvents]
	}
}

func (m *Manager) totalAlertCountsLocked() (int, int) {
	var opens, closes int
	for _, state := range m.states {
		opens += state.TotalOpens
		closes += state.TotalCloses
	}
	return opens, closes
}

func (m *Manager) ensureStateLocked(entry watchEntry) *entryState {
	key := entryKey(entry)
	state, ok := m.states[key]
	if !ok {
		state = &entryState{
			Entry:       entry,
			ProcessPIDs: make(map[int32]processInstance),
			Connections: make(map[string]connectionInstance),
		}
		m.states[key] = state
	}
	state.Entry = entry
	if state.ProcessPIDs == nil {
		state.ProcessPIDs = make(map[int32]processInstance)
	}
	if state.Connections == nil {
		state.Connections = make(map[string]connectionInstance)
	}
	return state
}

func (m *Manager) resetStatesLocked(watchlist []watchEntry) {
	if len(m.states) == 0 {
		return
	}
	allowed := make(map[string]struct{}, len(watchlist))
	for _, entry := range watchlist {
		allowed[entryKey(entry)] = struct{}{}
	}
	for key := range m.states {
		if _, ok := allowed[key]; !ok {
			delete(m.states, key)
		}
	}
}

func (m *Manager) clearAppStatesLocked() {
	for _, state := range m.states {
		if state.Entry.Kind != "app" {
			continue
		}
		state.ProcessPIDs = make(map[int32]processInstance)
	}
}

func (m *Manager) clearURLStatesLocked() {
	for _, state := range m.states {
		if state.Entry.Kind != "url" {
			continue
		}
		state.Connections = make(map[string]connectionInstance)
	}
}

func (m *Manager) currentConfig() monitorConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg := m.config
	cfg.Watchlist = cloneWatchlist(cfg.Watchlist)
	return cfg
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

func (m *Manager) setProcessEnumerator(e processEnumerator) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processes = e
}

func (m *Manager) setConnectionEnumerator(e connectionEnumerator) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connections = e
}

func (m *Manager) setResolver(r hostResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolver = r
}

func sanitizeWatchlist(entries []watchEntry) []watchEntry {
	if len(entries) == 0 {
		return []watchEntry{}
	}

	sanitized := make([]watchEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		kind := strings.ToLower(strings.TrimSpace(entry.Kind))
		if kind != "app" && kind != "url" {
			continue
		}

		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}

		name := strings.TrimSpace(entry.DisplayName)
		if name == "" {
			continue
		}

		key := fmt.Sprintf("%s:%s", kind, strings.ToLower(id))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		sanitized = append(sanitized, watchEntry{
			Kind:         kind,
			ID:           id,
			DisplayName:  name,
			AlertOnOpen:  entry.AlertOnOpen,
			AlertOnClose: entry.AlertOnClose,
		})
	}

	if len(sanitized) == 0 {
		return []watchEntry{}
	}

	sort.Slice(sanitized, func(i, j int) bool {
		return entryKey(sanitized[i]) < entryKey(sanitized[j])
	})

	return sanitized
}

func cloneWatchlist(entries []watchEntry) []watchEntry {
	if len(entries) == 0 {
		return []watchEntry{}
	}

	clone := make([]watchEntry, len(entries))
	copy(clone, entries)
	return clone
}

func cloneEvents(events []alertEvent) []alertEvent {
	if len(events) == 0 {
		return []alertEvent{}
	}
	cloned := make([]alertEvent, len(events))
	copy(cloned, events)
	return cloned
}

func entryKey(entry watchEntry) string {
	return fmt.Sprintf("%s:%s", strings.ToLower(strings.TrimSpace(entry.Kind)), strings.ToLower(strings.TrimSpace(entry.ID)))
}

func filterWatchlistByKind(entries []watchEntry, kind string) []watchEntry {
	filtered := make([]watchEntry, 0, len(entries))
	normalized := strings.ToLower(strings.TrimSpace(kind))
	for _, entry := range entries {
		if strings.ToLower(strings.TrimSpace(entry.Kind)) == normalized {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func matchAppEntry(entry watchEntry, sample processSample) bool {
	id := normalize(entry.ID)
	if id == "" {
		return false
	}

	candidates := []string{
		sample.Name,
		filepath.Base(sample.Executable),
		sample.Executable,
		sample.CommandLine,
	}

	for _, candidate := range candidates {
		normalized := normalize(candidate)
		if normalized == "" {
			continue
		}
		if normalized == id || strings.Contains(normalized, id) {
			return true
		}
	}

	return false
}

func extractHost(identifier string) string {
	trimmed := strings.TrimSpace(identifier)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(parsed.Hostname())
	return strings.ToLower(host)
}

func isActiveConnection(status string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	switch normalized {
	case "ESTABLISHED", "SYN_SENT", "SYN_RECV", "CLOSE_WAIT":
		return true
	default:
		return false
	}
}

func connectionKey(sample connectionSample) string {
	remote := strings.ToLower(strings.TrimSpace(sample.RemoteIP))
	return fmt.Sprintf("%d@%s:%d", sample.PID, remote, sample.RemotePort)
}

func formatProcessDetail(instance processInstance) string {
	name := strings.TrimSpace(instance.Name)
	if name == "" {
		name = filepath.Base(strings.TrimSpace(instance.Executable))
	}
	if name == "" {
		name = "unknown"
	}
	return fmt.Sprintf("PID %d (%s)", instance.PID, name)
}

func formatConnectionDetail(instance connectionInstance) string {
	remote := strings.TrimSpace(instance.RemoteIP)
	if remote == "" {
		remote = "unknown"
	}
	return fmt.Sprintf("PID %d → %s:%d", instance.PID, remote, instance.RemotePort)
}

func formatEntryMetric(active, opened, closed int, lastKind string, lastAt time.Time, detail string) string {
	parts := []string{fmt.Sprintf("Active: %d", active), fmt.Sprintf("Opened: %d", opened), fmt.Sprintf("Closed: %d", closed)}
	if !lastAt.IsZero() && lastKind != "" {
		if detail != "" {
			parts = append(parts, fmt.Sprintf("Last %s: %s", lastKind, detail))
		} else {
			parts = append(parts, fmt.Sprintf("Last %s at %s", lastKind, lastAt.Format(time.RFC3339)))
		}
	}
	return strings.Join(parts, " · ")
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

type gopsutilProcessEnumerator struct{}

type gopsutilConnectionEnumerator struct{}

type defaultHostResolver struct{}

func (gopsutilProcessEnumerator) Processes(ctx context.Context) ([]processSample, error) {
	procs, err := gproc.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	samples := make([]processSample, 0, len(procs))
	for _, proc := range procs {
		if proc == nil {
			continue
		}
		name, _ := proc.NameWithContext(ctx)
		exe, _ := proc.ExeWithContext(ctx)
		cmd, _ := proc.CmdlineWithContext(ctx)
		samples = append(samples, processSample{
			PID:         proc.Pid,
			Name:        name,
			Executable:  exe,
			CommandLine: cmd,
		})
	}
	return samples, nil
}

func (gopsutilConnectionEnumerator) Connections(ctx context.Context) ([]connectionSample, error) {
	conns, err := gnet.ConnectionsWithContext(ctx, "inet")
	if err != nil {
		return nil, err
	}
	samples := make([]connectionSample, 0, len(conns))
	for _, conn := range conns {
		samples = append(samples, connectionSample{
			PID:        conn.Pid,
			RemoteIP:   conn.Raddr.IP,
			RemotePort: conn.Raddr.Port,
			Status:     conn.Status,
		})
	}
	return samples, nil
}

func (defaultHostResolver) LookupIP(ctx context.Context, host string) ([]string, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		ips = append(ips, addr.IP.String())
	}
	return ips, nil
}
