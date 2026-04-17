// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package tree

import (
	"slices"
	"testing"

	"github.com/derailed/k9s/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestRegistry_RegisteredIDs(t *testing.T) {
	ids := RegisteredIDs()
	assert.Contains(t, ids, "crossplane")
	assert.Contains(t, ids, "argocd")
	assert.Contains(t, ids, "ownerref")

	// ownerref is the generic fallback and must come AFTER specialized
	// providers so its always-true Applies doesn't shadow them.
	idxOwnerRef := slices.Index(ids, "ownerref")
	idxCrossplane := slices.Index(ids, "crossplane")
	idxArgo := slices.Index(ids, "argocd")
	assert.Greater(t, idxOwnerRef, idxCrossplane)
	assert.Greater(t, idxOwnerRef, idxArgo)
}

func TestEnabled_RespectsConfig(t *testing.T) {
	cfg := &config.K9s{}

	assert.Empty(t, Enabled(cfg), "no providers should be enabled by default")

	// Per-provider toggles are gated by the umbrella ResourceTree.Enable.
	cfg.ResourceTree.Providers.Crossplane.Enable = true
	cfg.ResourceTree.Providers.Argo.Enable = true
	cfg.ResourceTree.Providers.OwnerRef.Enable = true
	assert.Empty(t, Enabled(cfg), "providers must stay off until ResourceTree.Enable=true")

	cfg.ResourceTree.Enable = true
	pp := Enabled(cfg)
	if assert.Len(t, pp, 3) {
		// Specialized providers come first; ownerref always last so its
		// always-true Applies stays a fallback. The relative order of
		// argocd vs crossplane mirrors Go init() order (file alphabetical).
		ids := []string{pp[0].ID(), pp[1].ID(), pp[2].ID()}
		assert.Contains(t, ids[:2], "crossplane")
		assert.Contains(t, ids[:2], "argocd")
		assert.Equal(t, "ownerref", ids[2])
	}

	cfg.ResourceTree.Providers.Argo.Enable = false
	cfg.ResourceTree.Providers.OwnerRef.Enable = false
	pp = Enabled(cfg)
	if assert.Len(t, pp, 1) {
		assert.Equal(t, "crossplane", pp[0].ID())
	}
}

func TestEnabled_NilConfig(t *testing.T) {
	assert.Empty(t, Enabled(nil))
}

func TestRegister_NoOpsOnInvalidInputs(t *testing.T) {
	before := RegisteredIDs()
	Register("", func(*config.K9s) Provider { return nil })
	Register("ignored", nil)
	after := RegisteredIDs()
	assert.Equal(t, before, after)
}
