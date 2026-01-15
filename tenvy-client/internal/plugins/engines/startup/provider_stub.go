//go:build !windows && !linux && !darwin

package startupengine

import "context"

type nativeProvider struct{}

func newNativeProvider() Provider {
	return &nativeProvider{}
}

func (p *nativeProvider) List(ctx context.Context, req ListRequest) (Inventory, error) {
	return Inventory{}, ErrNotSupported
}

func (p *nativeProvider) Toggle(ctx context.Context, req ToggleRequest) (Entry, error) {
	return Entry{}, ErrNotSupported
}

func (p *nativeProvider) Create(ctx context.Context, req CreateRequest) (Entry, error) {
	return Entry{}, ErrNotSupported
}

func (p *nativeProvider) Remove(ctx context.Context, req RemoveRequest) (RemoveResult, error) {
	return RemoveResult{}, ErrNotSupported
}

func (p *nativeProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{}
}
