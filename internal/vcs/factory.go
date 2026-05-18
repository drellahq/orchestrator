package vcs

import "fmt"

// ProviderConstructor is a function that creates a new Provider instance.
type ProviderConstructor func() Provider

var registry = map[string]ProviderConstructor{}

// Register adds a provider constructor to the registry.
// It is intended to be called from provider packages' init() functions.
func Register(name string, ctor ProviderConstructor) {
	registry[name] = ctor
}

// NewProvider creates a VCS provider based on the provider name.
// Currently only "github" is supported. Empty string defaults to "github".
func NewProvider(name string) (Provider, error) {
	if name == "" {
		name = "github"
	}
	ctor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unsupported VCS provider: %q", name)
	}
	return ctor(), nil
}
