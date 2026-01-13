package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

func DBIP() Resolver {
	return ResolverFunc(func(ctx context.Context, ip net.IP, cfg Config) (Result, error) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		apiKey := strings.TrimSpace(cfg.APIKey)
		if apiKey == "" {
			apiKey = "free"
		}

		url := fmt.Sprintf("https://api.db-ip.com/v2/%s/%s", apiKey, ip.String())
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return Result{}, err
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
			return Result{}, fmt.Errorf("db-ip api returned status %d", resp.StatusCode)
		}

		var data struct {
			IPAddress     string `json:"ipAddress"`
			ContinentCode string `json:"continentCode"`
			ContinentName string `json:"continentName"`
			CountryCode   string `json:"countryCode"`
			CountryName   string `json:"countryName"`
			StateProv     string `json:"stateProv"`
			City          string `json:"city"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			return Result{}, err
		}

		result := Result{
			City:        data.City,
			Region:      data.StateProv,
			CountryCode: data.CountryCode,
			Country:     data.CountryName,
		}

		return result, nil
	})
}
