package tcpconnectionsengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	gnet "github.com/shirou/gopsutil/v3/net"
	gproc "github.com/shirou/gopsutil/v3/process"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type (
	Command       = protocol.Command
	CommandResult = protocol.CommandResult
)

type Logger interface {
	Printf(format string, args ...interface{})
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	AgentID   string
	BaseURL   string
	AuthKey   string
	Client    HTTPDoer
	Logger    Logger
	UserAgent string
}

type Manager struct {
	cfg atomic.Value
}

type TcpConnectionFamily string

type TcpConnectionState string

type TcpConnectionEndpoint struct {
	Address string              `json:"address"`
	Port    uint32              `json:"port"`
	Family  TcpConnectionFamily `json:"family"`
	Host    string              `json:"host,omitempty"`
	Label   string              `json:"label,omitempty"`
}

type TcpConnectionProcess struct {
	PID         int32  `json:"pid,omitempty"`
	Name        string `json:"name,omitempty"`
	Executable  string `json:"executable,omitempty"`
	CommandLine string `json:"commandLine,omitempty"`
	Username    string `json:"username,omitempty"`
}

type TcpConnectionEntry struct {
	ID        string                 `json:"id"`
	Local     TcpConnectionEndpoint  `json:"local"`
	Remote    *TcpConnectionEndpoint `json:"remote,omitempty"`
	State     TcpConnectionState     `json:"state"`
	Listening bool                   `json:"listening"`
	Process   *TcpConnectionProcess  `json:"process,omitempty"`
	Family    TcpConnectionFamily    `json:"family"`
	pid       int32                  `json:"-"`
}

type TcpConnectionQuery struct {
	LocalFilter  string `json:"localFilter,omitempty"`
	RemoteFilter string `json:"remoteFilter,omitempty"`
	State        string `json:"state,omitempty"`
	IncludeIPv6  bool   `json:"includeIpv6,omitempty"`
	ResolveDNS   bool   `json:"resolveDns,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

type TcpConnectionSnapshot struct {
	CapturedAt  string               `json:"capturedAt"`
	Total       int                  `json:"total"`
	Truncated   bool                 `json:"truncated,omitempty"`
	Connections []TcpConnectionEntry `json:"connections"`
	RequestID   string               `json:"requestId,omitempty"`
	Query       *TcpConnectionQuery  `json:"query,omitempty"`
}

type TcpConnectionSnapshotEnvelope struct {
	RequestID string                `json:"requestId,omitempty"`
	Snapshot  TcpConnectionSnapshot `json:"snapshot"`
}

type TcpConnectionsCommandPayload struct {
	Action    string              `json:"action"`
	RequestID string              `json:"requestId"`
	Query     *TcpConnectionQuery `json:"query,omitempty"`
}

const (
	requestTimeout   = 15 * time.Second
	maxSnapshotLimit = 2048
)

func NewManager(cfg Config) *Manager {
	manager := &Manager{}
	manager.updateConfig(cfg)
	return manager
}

func (m *Manager) UpdateConfig(cfg Config) {
	if m == nil {
		return
	}
	m.updateConfig(cfg)
}

func (m *Manager) updateConfig(cfg Config) {
	m.cfg.Store(cfg)
}

func (m *Manager) config() Config {
	if value := m.cfg.Load(); value != nil {
		if cfg, ok := value.(Config); ok {
			return cfg
		}
	}
	return Config{}
}

func (m *Manager) logf(format string, args ...interface{}) {
	cfg := m.config()
	if cfg.Logger == nil {
		return
	}
	cfg.Logger.Printf(format, args...)
}

func (m *Manager) userAgent() string {
	cfg := m.config()
	ua := strings.TrimSpace(cfg.UserAgent)
	if ua != "" {
		return ua
	}
	return "tenvy-client"
}

func (m *Manager) Shutdown() {}

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := CommandResult{
		CommandID:   cmd.ID,
		CompletedAt: completedAt,
	}

	var payload TcpConnectionsCommandPayload
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("invalid tcp-connections payload: %v", err)
			return result
		}
	}

	if strings.TrimSpace(payload.Action) != "enumerate" {
		result.Success = false
		result.Error = "unsupported tcp-connections action"
		return result
	}
	requestID := strings.TrimSpace(payload.RequestID)
	if requestID == "" {
		result.Success = false
		result.Error = "tcp-connections request id is required"
		return result
	}

	snapshot, err := m.collectSnapshot(ctx, requestID, payload.Query)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}

	dispatchCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	if err := m.sendSnapshot(dispatchCtx, requestID, snapshot); err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.Output = fmt.Sprintf("reported %d connections", len(snapshot.Connections))
	return result
}

func (m *Manager) collectSnapshot(ctx context.Context, requestID string, query *TcpConnectionQuery) (TcpConnectionSnapshot, error) {
	normalizedQuery := normalizeQuery(query)
	entries, total, truncated, err := m.enumerateConnections(ctx, normalizedQuery)
	if err != nil {
		return TcpConnectionSnapshot{}, err
	}
	snapshot := TcpConnectionSnapshot{
		CapturedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		Total:       total,
		Truncated:   truncated,
		Connections: entries,
		RequestID:   requestID,
	}
	if normalizedQuery != nil {
		snapshot.Query = normalizedQuery
	}
	return snapshot, nil
}

func (m *Manager) enumerateConnections(ctx context.Context, query *TcpConnectionQuery) ([]TcpConnectionEntry, int, bool, error) {
	includeIPv6 := false
	limit := maxSnapshotLimit
	var localFilter, remoteFilter, stateFilter string
	resolveDNS := false
	if query != nil {
		includeIPv6 = query.IncludeIPv6
		if query.Limit > 0 {
			limit = query.Limit
		}
		localFilter = strings.ToLower(strings.TrimSpace(query.LocalFilter))
		remoteFilter = strings.ToLower(strings.TrimSpace(query.RemoteFilter))
		stateFilter = strings.ToUpper(strings.TrimSpace(query.State))
		resolveDNS = query.ResolveDNS
	}

	kind := "tcp4"
	if includeIPv6 {
		kind = "tcp"
	}

	stats, err := gnet.ConnectionsWithContext(ctx, kind)
	if err != nil {
		return nil, 0, false, fmt.Errorf("tcp connections enumeration failed: %w", err)
	}

	entries := make([]TcpConnectionEntry, 0, len(stats))

	for _, stat := range stats {
		family := detectFamily(stat)
		state := normalizeState(stat.Status)
		listening := state == "LISTENING"

		localEndpoint := buildEndpoint(stat.Laddr.IP, uint32(stat.Laddr.Port), family)
		remoteEndpoint := endpointOrNil(stat.Raddr.IP, uint32(stat.Raddr.Port), family)

		if localFilter != "" && !strings.Contains(strings.ToLower(localEndpoint.Label), localFilter) {
			continue
		}
		remoteLabel := ""
		if remoteEndpoint != nil {
			remoteLabel = remoteEndpoint.Label
		}
		if remoteFilter != "" && !strings.Contains(strings.ToLower(remoteLabel), remoteFilter) {
			continue
		}
		if stateFilter != "" && stateFilter != "ALL" && state != TcpConnectionState(stateFilter) {
			continue
		}

		entry := TcpConnectionEntry{
			ID:        buildConnectionID(family, localEndpoint.Label, remoteLabel, stat.Pid, string(state)),
			Local:     localEndpoint,
			Remote:    remoteEndpoint,
			State:     state,
			Listening: listening,
			Family:    family,
			pid:       stat.Pid,
		}

		entries = append(entries, entry)
	}

	total := len(entries)
	truncated := false

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Local.Label != entries[j].Local.Label {
			return entries[i].Local.Label < entries[j].Local.Label
		}
		if entries[i].Remote == nil && entries[j].Remote != nil {
			return true
		}
		if entries[i].Remote != nil && entries[j].Remote == nil {
			return false
		}
		if entries[i].Remote != nil && entries[j].Remote != nil {
			if entries[i].Remote.Label != entries[j].Remote.Label {
				return entries[i].Remote.Label < entries[j].Remote.Label
			}
		}
		if entries[i].State != entries[j].State {
			return entries[i].State < entries[j].State
		}
		return entries[i].ID < entries[j].ID
	})

	if total > limit {
		entries = entries[:limit]
		truncated = true
	}

	pidSet := make(map[int32]struct{})
	for _, entry := range entries {
		if entry.pid > 0 {
			pidSet[entry.pid] = struct{}{}
		}
	}

	processInfo := m.lookupProcesses(ctx, pidSet)
	resolver := newHostResolver(resolveDNS)

	for index := range entries {
		entry := &entries[index]
		if entry.Remote != nil && resolver != nil {
			if host := resolver.lookup(ctx, entry.Remote.Address); host != "" {
				entry.Remote.Host = host
			}
		}
		if entry.pid > 0 {
			if proc, ok := processInfo[entry.pid]; ok {
				entry.Process = proc
			}
		}
	}

	return entries, total, truncated, nil
}

func (m *Manager) sendSnapshot(ctx context.Context, requestID string, snapshot TcpConnectionSnapshot) error {
	payload := TcpConnectionSnapshotEnvelope{RequestID: requestID, Snapshot: snapshot}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	cfg := m.config()
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("tcp connections: missing base URL")
	}
	if cfg.Client == nil {
		return fmt.Errorf("tcp connections: missing http client")
	}

	endpoint := fmt.Sprintf("%s/api/agents/%s/tcp-connections/state", baseURL, url.PathEscape(cfg.AgentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(m.userAgent()); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if key := strings.TrimSpace(cfg.AuthKey); key != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	}

	resp, err := cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("tcp connections upload failed: status %d", resp.StatusCode)
	}
	return nil
}

type hostResolver struct {
	enabled bool
	cache   map[string]string
	lookups int
	limit   int
}

func newHostResolver(enabled bool) *hostResolver {
	if !enabled {
		return nil
	}
	return &hostResolver{
		enabled: true,
		cache:   make(map[string]string),
		limit:   64,
	}
}

func (r *hostResolver) lookup(ctx context.Context, address string) string {
	if r == nil || !r.enabled {
		return ""
	}
	trimmed := strings.TrimSpace(address)
	if trimmed == "" || trimmed == "0.0.0.0" || trimmed == "::" {
		return ""
	}
	if idx := strings.Index(trimmed, "%"); idx != -1 {
		trimmed = trimmed[:idx]
	}
	if cached, ok := r.cache[trimmed]; ok {
		return cached
	}
	if r.limit > 0 && r.lookups >= r.limit {
		return ""
	}
	r.lookups++
	resolverCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(resolverCtx, trimmed)
	if err != nil || len(names) == 0 {
		r.cache[trimmed] = ""
		return ""
	}
	host := strings.TrimSuffix(names[0], ".")
	r.cache[trimmed] = host
	return host
}

func normalizeQuery(query *TcpConnectionQuery) *TcpConnectionQuery {
	if query == nil {
		return nil
	}
	normalized := &TcpConnectionQuery{}
	if trimmed := strings.TrimSpace(query.LocalFilter); trimmed != "" {
		normalized.LocalFilter = trimmed
	}
	if trimmed := strings.TrimSpace(query.RemoteFilter); trimmed != "" {
		normalized.RemoteFilter = trimmed
	}
	if trimmed := strings.TrimSpace(query.State); trimmed != "" {
		normalized.State = strings.ToUpper(trimmed)
	}
	normalized.IncludeIPv6 = query.IncludeIPv6
	normalized.ResolveDNS = query.ResolveDNS
	if query.Limit > 0 {
		if query.Limit > maxSnapshotLimit {
			normalized.Limit = maxSnapshotLimit
		} else {
			normalized.Limit = query.Limit
		}
	}
	if normalized.LocalFilter == "" && normalized.RemoteFilter == "" && normalized.State == "" && !normalized.IncludeIPv6 && !normalized.ResolveDNS && normalized.Limit == 0 {
		return nil
	}
	return normalized
}

func detectFamily(stat gnet.ConnectionStat) TcpConnectionFamily {
	if strings.Contains(stat.Laddr.IP, ":") || strings.Contains(stat.Raddr.IP, ":") {
		return TcpConnectionFamily("IPv6")
	}
	if stat.Family == 10 || stat.Family == 23 || stat.Family == 30 {
		return TcpConnectionFamily("IPv6")
	}
	return TcpConnectionFamily("IPv4")
}

func buildEndpoint(address string, port uint32, family TcpConnectionFamily) TcpConnectionEndpoint {
	label := formatEndpoint(address, port, family)
	return TcpConnectionEndpoint{
		Address: address,
		Port:    port,
		Family:  family,
		Label:   label,
	}
}

func endpointOrNil(address string, port uint32, family TcpConnectionFamily) *TcpConnectionEndpoint {
	if address == "" && port == 0 {
		return nil
	}
	endpoint := buildEndpoint(address, port, family)
	if endpoint.Label == "" {
		return nil
	}
	return &endpoint
}

func formatEndpoint(address string, port uint32, family TcpConnectionFamily) string {
	addr := strings.TrimSpace(address)
	if addr == "" {
		if port == 0 {
			return ""
		}
		return fmt.Sprintf(":%d", port)
	}
	if family == TcpConnectionFamily("IPv6") || strings.Contains(addr, ":") {
		return fmt.Sprintf("[%s]:%d", addr, port)
	}
	return fmt.Sprintf("%s:%d", addr, port)
}

func normalizeState(state string) TcpConnectionState {
	upper := strings.ToUpper(strings.TrimSpace(state))
	switch upper {
	case "LISTEN", "LISTENING":
		return TcpConnectionState("LISTENING")
	case "ESTABLISHED":
		return TcpConnectionState("ESTABLISHED")
	case "CLOSE_WAIT":
		return TcpConnectionState("CLOSE_WAIT")
	case "SYN_SENT":
		return TcpConnectionState("SYN_SENT")
	case "SYN_RECV", "SYN_RECEIVED":
		return TcpConnectionState("SYN_RECEIVED")
	case "FIN_WAIT1", "FIN_WAIT_1":
		return TcpConnectionState("FIN_WAIT_1")
	case "FIN_WAIT2", "FIN_WAIT_2":
		return TcpConnectionState("FIN_WAIT_2")
	case "TIME_WAIT":
		return TcpConnectionState("TIME_WAIT")
	case "LAST_ACK", "LAST-ACK":
		return TcpConnectionState("LAST_ACK")
	case "CLOSING":
		return TcpConnectionState("CLOSING")
	case "BOUND":
		return TcpConnectionState("BOUND")
	case "CLOSED":
		return TcpConnectionState("CLOSED")
	default:
		return TcpConnectionState("UNKNOWN")
	}
}

func buildConnectionID(family TcpConnectionFamily, local, remote string, pid int32, state string) string {
	parts := []string{string(family), local, remote, fmt.Sprintf("%d", pid), state}
	return strings.Join(parts, "|")
}

func (m *Manager) lookupProcesses(ctx context.Context, pids map[int32]struct{}) map[int32]*TcpConnectionProcess {
	if len(pids) == 0 {
		return nil
	}
	details := make(map[int32]*TcpConnectionProcess, len(pids))
	for pid := range pids {
		proc, err := gproc.NewProcess(pid)
		if err != nil {
			continue
		}
		name, _ := proc.NameWithContext(ctx)
		exe, _ := proc.ExeWithContext(ctx)
		cmdlineSlice, _ := proc.CmdlineSliceWithContext(ctx)
		username, _ := proc.UsernameWithContext(ctx)
		command := strings.Join(cmdlineSlice, " ")
		details[pid] = &TcpConnectionProcess{
			PID:         pid,
			Name:        name,
			Executable:  exe,
			CommandLine: strings.TrimSpace(command),
			Username:    username,
		}
	}
	return details
}
