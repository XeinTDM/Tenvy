package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

func Tenvy() Resolver {
	return ResolverFunc(func(ctx context.Context, ip net.IP, cfg Config) (Result, error) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}

		baseURL := strings.TrimRight(cfg.BaseURL, "/")
		if baseURL == "" {
			return Result{}, fmt.Errorf("tenvy server base url not configured")
		}

		url := fmt.Sprintf("%s/api/geo/%s", baseURL, ip.String())
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return Result{}, err
		}

		if cfg.AuthKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.AuthKey)
		}

		client := cfg.HTTPClient
		if client == nil {
			client = http.DefaultClient
		}

		resp, err := client.Do(req)
		if err != nil {
			return Result{}, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return Result{}, fmt.Errorf("tenvy server geo api returned status %d", resp.StatusCode)
		}

		var data struct {
			CountryName string `json:"countryName"`
			CountryCode string `json:"countryCode"`
			IsProxy     bool   `json:"isProxy"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			return Result{}, err
		}

		return Result{
			Country:     data.CountryName,
			CountryCode: data.CountryCode,
			ISP:         fmt.Sprintf("Proxy via Tenvy (IsProxy: %v)", data.IsProxy),
		}, nil
	})
}
