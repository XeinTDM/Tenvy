package geolocation

import (
	"strings"
	"time"

	"github.com/rootbay/tenvy-client/internal/modules/misc/geolocation/providers"
)

var defaultProviderOrder = []string{"tenvy", "ipinfo", "maxmind", "db-ip"}

type Config struct {
	DefaultProvider string
	Providers       map[string]providers.Config
}

func (c Config) withDefaults() Config {
	normalized := Config{Providers: make(map[string]providers.Config)}

	if len(c.Providers) == 0 {
		for _, name := range defaultProviderOrder {
			normalized.Providers[name] = providers.Config{}
		}
	} else {
		for name, cfg := range c.Providers {
			providerID := strings.ToLower(strings.TrimSpace(name))
			if providerID == "" {
				continue
			}
			normalized.Providers[providerID] = cfg.Normalize()
		}
	}

	defaultProvider := strings.ToLower(strings.TrimSpace(c.DefaultProvider))
	if defaultProvider != "" {
		if _, ok := normalized.Providers[defaultProvider]; ok {
			normalized.DefaultProvider = defaultProvider
		}
	}

	if normalized.DefaultProvider == "" {
		for _, candidate := range defaultProviderOrder {
			if _, ok := normalized.Providers[candidate]; ok {
				normalized.DefaultProvider = candidate
				break
			}
		}
	}

	if normalized.DefaultProvider == "" {
		for candidate := range normalized.Providers {
			normalized.DefaultProvider = candidate
			break
		}
	}

	if normalized.DefaultProvider == "" {
		normalized.DefaultProvider = defaultProviderOrder[0]
		if _, ok := normalized.Providers[normalized.DefaultProvider]; !ok {
			normalized.Providers[normalized.DefaultProvider] = providers.Config{}
		}
	}

	for name, cfg := range normalized.Providers {
		if cfg.Timeout <= 0 {
			cfg.Timeout = 5 * time.Second
		}
		normalized.Providers[name] = cfg
	}

	return normalized
}
