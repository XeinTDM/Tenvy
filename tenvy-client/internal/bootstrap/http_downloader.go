package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

type HTTPDownloaderConfig struct {
	Client       *http.Client
	URL          string
	ArtifactType LoaderArtifactType
	Mode         fs.FileMode
}

type LoaderArtifactType string

const (
	LoaderArtifactTypeBinary  LoaderArtifactType = "binary"
	LoaderArtifactTypeArchive LoaderArtifactType = "zip"
)

func NewHTTPDownloader(cfg HTTPDownloaderConfig) (LoaderDownloader, error) {
	trimmedURL := strings.TrimSpace(cfg.URL)
	if trimmedURL == "" {
		return nil, errors.New("loader downloader requires url")
	}
	artifactType := cfg.ArtifactType
	if artifactType == "" {
		artifactType = LoaderArtifactTypeBinary
	}
	switch artifactType {
	case LoaderArtifactTypeBinary, LoaderArtifactTypeArchive:
		// supported
	default:
		return nil, fmt.Errorf("unsupported loader artifact type: %s", artifactType)
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	return LoaderDownloaderFunc(func(ctx context.Context, metadata LoaderMetadata) (LoaderPackage, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, trimmedURL, nil)
		if err != nil {
			return LoaderPackage{}, fmt.Errorf("build loader request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return LoaderPackage{}, fmt.Errorf("fetch loader: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return LoaderPackage{}, fmt.Errorf("fetch loader: unexpected status %d", resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return LoaderPackage{}, fmt.Errorf("read loader: %w", err)
		}
		pkg := LoaderPackage{}
		switch artifactType {
		case LoaderArtifactTypeArchive:
			pkg.Archive = data
		case LoaderArtifactTypeBinary:
			pkg.Binary = data
			pkg.Mode = cfg.Mode
		}
		return pkg, nil
	}), nil
}

func DefaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}
