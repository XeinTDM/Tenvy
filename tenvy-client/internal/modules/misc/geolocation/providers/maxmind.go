package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

func MaxMind() Resolver {
	return ResolverFunc(func(ctx context.Context, ip net.IP, cfg Config) (Result, error) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		apiKey := strings.TrimSpace(cfg.APIKey)
		if apiKey == "" {
			return Result{}, ErrMissingAPIKey
		}

		parts := strings.SplitN(apiKey, ":", 2)
		if len(parts) != 2 {
			return Result{}, fmt.Errorf("invalid maxmind api key format, expected AccountID:LicenseKey")
		}
		accountID, licenseKey := parts[0], parts[1]

		url := fmt.Sprintf("https://geoip.maxmind.com/geoip/v2.1/city/%s", ip.String())
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return Result{}, err
		}
		req.SetBasicAuth(accountID, licenseKey)

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
			return Result{}, fmt.Errorf("maxmind api returned status %d", resp.StatusCode)
		}

		var data struct {
			City struct {
				Names map[string]string `json:"names"`
			} `json:"city"`
			Subdivisions []struct {
				Names map[string]string `json:"names"`
			} `json:"subdivisions"`
			Country struct {
				IsoCode string            `json:"iso_code"`
				Names   map[string]string `json:"names"`
			} `json:"country"`
			Location struct {
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
				TimeZone  string  `json:"time_zone"`
			} `json:"location"`
			Traits struct {
				AutonomousSystemNumber       int    `json:"autonomous_system_number"`
				AutonomousSystemOrganization string `json:"autonomous_system_organization"`
				Isp                          string `json:"isp"`
			} `json:"traits"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			return Result{}, err
		}

		result := Result{
			City:        data.City.Names["en"],
			CountryCode: data.Country.IsoCode,
			Country:     data.Country.Names["en"],
			Latitude:    data.Location.Latitude,
			Longitude:   data.Location.Longitude,
			ISP:         data.Traits.Isp,
		}

		if len(data.Subdivisions) > 0 {
			result.Region = data.Subdivisions[0].Names["en"]
		}

		if data.Traits.AutonomousSystemNumber > 0 {
			result.ASN = fmt.Sprintf("AS%d", data.Traits.AutonomousSystemNumber)
		}

		if data.Location.TimeZone != "" {
			result.Timezone = &Timezone{ID: data.Location.TimeZone}
		}

		return result, nil
	})
}
