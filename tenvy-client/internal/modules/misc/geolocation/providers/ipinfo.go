package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

func IPInfo() Resolver {
	return ResolverFunc(func(ctx context.Context, ip net.IP, cfg Config) (Result, error) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		apiKey := strings.TrimSpace(cfg.APIKey)
		if apiKey == "" {
			return Result{}, ErrMissingAPIKey
		}

		url := fmt.Sprintf("https://ipinfo.io/%s/json?token=%s", ip.String(), apiKey)
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
			return Result{}, fmt.Errorf("ipinfo api returned status %d", resp.StatusCode)
		}

		var data struct {
			IP       string `json:"ip"`
			City     string `json:"city"`
			Region   string `json:"region"`
			Country  string `json:"country"`
			Loc      string `json:"loc"`
			Org      string `json:"org"`
			Postal   string `json:"postal"`
			Timezone string `json:"timezone"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			return Result{}, err
		}

		result := Result{
			City:        data.City,
			Region:      data.Region,
			CountryCode: data.Country,
			Country:     data.Country,
		}

		if data.Loc != "" {
			parts := strings.Split(data.Loc, ",")
			if len(parts) == 2 {
				lat, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
				lon, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
				result.Latitude = lat
				result.Longitude = lon
			}
		}

		if data.Org != "" {
			parts := strings.SplitN(data.Org, " ", 2)
			if len(parts) > 0 {
				result.ASN = parts[0]
			}
			if len(parts) > 1 {
				result.ISP = parts[1]
			}
		}

		if data.Timezone != "" {
			result.Timezone = &Timezone{ID: data.Timezone}
		}

		return result, nil
	})
}
