package agent

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
	"github.com/shirou/gopsutil/v3/host"
)

const (
	publicIPRequestTimeout = 3 * time.Second
	publicIPCacheTTL       = 30 * time.Minute
	publicIPRetryInterval  = 1 * time.Minute
	publicIPLookupEndpoint = "https://api.ipify.org?format=text"
)

var publicIPCache = newPublicIPResolver()

func CollectMetadata(buildVersion string) protocol.AgentMetadata {
	return CollectMetadataWithClient(buildVersion, nil)
}

func CollectMetadataWithClient(buildVersion string, client *http.Client) protocol.AgentMetadata {
	hostname, _ := os.Hostname()
	currentUser, err := user.Current()
	username := resolveUsername(currentUser, err)

	tags := parseTags(os.Getenv("TENVY_AGENT_TAGS"))

	return protocol.AgentMetadata{
		Hostname:        fallback(hostname, "unknown"),
		Username:        username,
		OS:              runtime.GOOS,
		Architecture:    runtime.GOARCH,
		IPAddress:       detectPrimaryIP(),
		PublicIPAddress: detectPublicIP(client),
		Tags:            tags,
		Version:         buildVersion,
		HardwareID:      detectHardwareID(),
	}
}

func detectHardwareID() string {
	if id := getMachineID(); id != "" {
		return id
	}

	if mac := getFirstMACAddress(); mac != "" {
		return fmt.Sprintf("mac-%s", mac)
	}

	hostname, _ := os.Hostname()
	return fmt.Sprintf("fallback-%s-%s-%s", runtime.GOOS, runtime.GOARCH, hostname)
}

func getMachineID() string {
	if info, err := host.Info(); err == nil && info.HostID != "" {
		return info.HostID
	}
	return ""
}

func getFirstMACAddress() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range interfaces {
		if (iface.Flags&net.FlagUp) != 0 && (iface.Flags&net.FlagLoopback) == 0 {
			addrs, _ := iface.Addrs()
			if len(addrs) > 0 {
				mac := iface.HardwareAddr.String()
				if mac != "" {
					return strings.ReplaceAll(mac, ":", "")
				}
			}
		}
	}
	return ""
}

func resolveUsername(currentUser *user.User, err error) string {
	if val := os.Getenv("USERNAME"); val != "" {
		return normalizeUsername(val)
	}
	if val := os.Getenv("USER"); val != "" {
		return normalizeUsername(val)
	}
	if err == nil && currentUser != nil {
		return normalizeUsername(currentUser.Username)
	}
	return "unknown"
}

func normalizeUsername(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	parts := strings.Split(trimmed, "/")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return "unknown"
	}
	return last
}

func detectPrimaryIP() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range interfaces {
		if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip := extractIP(addr)
			if ip == "" {
				continue
			}
			return ip
		}
	}
	return ""
}

func extractIP(addr net.Addr) string {
	switch v := addr.(type) {
	case *net.IPNet:
		if v.IP.IsLoopback() {
			return ""
		}
		ip := v.IP.To4()
		if ip != nil {
			return ip.String()
		}
		if v.IP.To16() != nil {
			return v.IP.String()
		}
	case *net.IPAddr:
		if v.IP.IsLoopback() {
			return ""
		}
		ip := v.IP.To4()
		if ip != nil {
			return ip.String()
		}
		if v.IP.To16() != nil {
			return v.IP.String()
		}
	}
	return ""
}

func parseTags(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}

func fallback(value, fallbackValue string) string {
	if strings.TrimSpace(value) == "" {
		return fallbackValue
	}
	return value
}

type publicIPResolver struct {
	mu         sync.Mutex
	value      string
	expiresAt  time.Time
	nextLookup time.Time
}

func newPublicIPResolver() *publicIPResolver {
	return &publicIPResolver{}
}

func (r *publicIPResolver) Resolve(client *http.Client) string {
	if r == nil {
		return ""
	}

	now := time.Now()

	r.mu.Lock()
	if r.value != "" && now.Before(r.expiresAt) {
		value := r.value
		r.mu.Unlock()
		return value
	}
	if now.Before(r.nextLookup) {
		r.mu.Unlock()
		return ""
	}
	r.nextLookup = now.Add(publicIPRetryInterval)
	r.mu.Unlock()

	ip := fetchPublicIP(client)
	if ip == "" {
		return ""
	}

	r.mu.Lock()
	r.value = ip
	r.expiresAt = time.Now().Add(publicIPCacheTTL)
	r.nextLookup = r.expiresAt
	r.mu.Unlock()
	return ip
}

func detectPublicIP(client *http.Client) string {
	return publicIPCache.Resolve(client)
}

func fetchPublicIP(client *http.Client) string {
	var httpClient *http.Client
	if client != nil {
		httpClient = client
	} else {
		httpClient = &http.Client{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), publicIPRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicIPLookupEndpoint, nil)
	if err != nil {
		return ""
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	ip := strings.TrimSpace(string(body))
	if ip == "" {
		return ""
	}
	return ip
}
