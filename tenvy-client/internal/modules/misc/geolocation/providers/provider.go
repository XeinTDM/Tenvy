package providers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

type Result struct {
	City        string
	Region      string
	Country     string
	CountryCode string
	Latitude    float64
	Longitude   float64
	ISP         string
	ASN         string
	Timezone    *Timezone
}

type Timezone struct {
	ID           string
	Offset       string
	Abbreviation string
}

type Config struct {
	APIKey     string
	Timeout    time.Duration
	HTTPClient *http.Client
	BaseURL    string
	AuthKey    string
}

func (c Config) Normalize() Config {
	copy := Config{
		APIKey:     strings.TrimSpace(c.APIKey),
		Timeout:    c.Timeout,
		HTTPClient: c.HTTPClient,
		BaseURL:    strings.TrimSpace(c.BaseURL),
		AuthKey:    strings.TrimSpace(c.AuthKey),
	}
	if copy.Timeout <= 0 {
		copy.Timeout = 5 * time.Second
	}
	if copy.HTTPClient == nil {
		copy.HTTPClient = http.DefaultClient
	}
	return copy
}

type Resolver interface {
	Lookup(ctx context.Context, ip net.IP, cfg Config) (Result, error)
}

type ResolverFunc func(context.Context, net.IP, Config) (Result, error)

func (f ResolverFunc) Lookup(ctx context.Context, ip net.IP, cfg Config) (Result, error) {
	if f == nil {
		return Result{}, errors.New("resolver not defined")
	}
	return f(ctx, ip, cfg)
}

var ErrMissingAPIKey = errors.New("provider api key required")
