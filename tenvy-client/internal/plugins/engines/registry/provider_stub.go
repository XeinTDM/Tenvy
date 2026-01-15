//go:build !windows && !linux && !darwin

package registryengine

import "context"

type nativeProvider struct{}

func newNativeProvider() Provider {
	return &nativeProvider{}
}

func (p *nativeProvider) List(ctx context.Context, req ListRequest) (RegistryListResult, error) {
	return RegistryListResult{}, ErrNotSupported
}

func (p *nativeProvider) CreateKey(ctx context.Context, req CreateKeyRequest) (RegistryMutationResult, error) {
	return RegistryMutationResult{}, ErrNotSupported
}

func (p *nativeProvider) CreateValue(ctx context.Context, req CreateValueRequest) (RegistryMutationResult, error) {
	return RegistryMutationResult{}, ErrNotSupported
}

func (p *nativeProvider) UpdateKey(ctx context.Context, req UpdateKeyRequest) (RegistryMutationResult, error) {
	return RegistryMutationResult{}, ErrNotSupported
}

func (p *nativeProvider) UpdateValue(ctx context.Context, req UpdateValueRequest) (RegistryMutationResult, error) {
	return RegistryMutationResult{}, ErrNotSupported
}

func (p *nativeProvider) DeleteKey(ctx context.Context, req DeleteKeyRequest) (RegistryMutationResult, error) {
	return RegistryMutationResult{}, ErrNotSupported
}

func (p *nativeProvider) DeleteValue(ctx context.Context, req DeleteValueRequest) (RegistryMutationResult, error) {
	return RegistryMutationResult{}, ErrNotSupported
}

func (p *nativeProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{}
}
