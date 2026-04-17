// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package tree

import (
	"sync"

	"github.com/derailed/k9s/internal/config"
)

// ProviderFactory builds a Provider from the active K9s configuration. It
// returns nil when the provider is disabled for the given config so callers
// can simply iterate the registry to discover the active set.
type ProviderFactory func(cfg *config.K9s) Provider

var (
	registryMu  sync.RWMutex
	registryMap = map[string]ProviderFactory{}
	registryOrd []string
)

// Register adds (or replaces) a provider factory under the given id. The
// registration order is preserved and used by Enabled to dispatch Applies
// checks: earlier registrations win when multiple providers claim the same
// resource. Specialized providers should register before generic fallbacks
// (e.g. crossplane/argocd before ownerref).
func Register(id string, f ProviderFactory) {
	if id == "" || f == nil {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registryMap[id]; !exists {
		registryOrd = append(registryOrd, id)
	}
	registryMap[id] = f
}

// Enabled returns the active providers in registration order, skipping
// factories that report nil for the current config. The umbrella
// ResourceTree.Enable flag short-circuits the lookup so the Shift-T binding
// is fully contained behind a single top-level toggle.
func Enabled(cfg *config.K9s) []Provider {
	if cfg == nil || !cfg.ResourceTree.Enable {
		return nil
	}
	registryMu.RLock()
	defer registryMu.RUnlock()

	out := make([]Provider, 0, len(registryOrd))
	for _, id := range registryOrd {
		factory := registryMap[id]
		if factory == nil {
			continue
		}
		if p := factory(cfg); p != nil {
			out = append(out, p)
		}
	}
	return out
}

// RegisteredIDs returns the registered provider ids in registration order.
// Primarily useful for tests and diagnostics.
func RegisteredIDs() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, len(registryOrd))
	copy(out, registryOrd)
	return out
}
