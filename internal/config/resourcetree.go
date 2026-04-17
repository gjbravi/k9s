// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config

// ResourceTree is the umbrella configuration for the generic resource-tree
// view (Shift-T). The feature is gated by a top-level Enable flag and each
// provider is independently gated under Providers.
//
//	k9s:
//	  resourceTree:
//	    enable: true
//	    providers:
//	      crossplane:
//	        enable: true
//	      argocd:
//	        enable: true
//	        expandChildApps: true
//	      ownerRef:
//	        enable: true
//
// When ResourceTree.Enable is false the Shift-T binding is not installed,
// even when individual providers are enabled.
type ResourceTree struct {
	// Enable activates the entire resource-tree feature. When false the
	// Shift-T action is not bound on any view.
	Enable    bool                  `json:"enable" yaml:"enable"`
	Providers ResourceTreeProviders `json:"providers" yaml:"providers"`
}

// ResourceTreeProviders groups per-provider toggles.
type ResourceTreeProviders struct {
	Crossplane Crossplane `json:"crossplane" yaml:"crossplane"`
	Argo       Argo       `json:"argocd" yaml:"argocd"`
	OwnerRef   OwnerRef   `json:"ownerRef" yaml:"ownerRef"`
}

// Crossplane tracks the Crossplane resource-tree provider. It walks
// Claim → XR → MR hierarchies and connection secrets.
type Crossplane struct {
	// Enable activates the Crossplane provider under Shift-T.
	Enable bool `json:"enable" yaml:"enable"`
}

// Argo tracks ArgoCD resource-tree provider options.
type Argo struct {
	// Enable activates the ArgoCD provider under Shift-T.
	Enable bool `json:"enable" yaml:"enable"`
	// ExpandChildApps recursively resolves Application children of an
	// ApplicationSet (or app-of-apps) into their own resources.
	ExpandChildApps bool `json:"expandChildApps" yaml:"expandChildApps"`
}

// OwnerRef tracks the generic owner-reference resource-tree provider. It
// scans common namespaced workload kinds (Deployments → ReplicaSets → Pods,
// CronJobs → Jobs → Pods, …) and links them by metadata.ownerReferences.
type OwnerRef struct {
	// Enable activates the owner-reference provider as a fallback under
	// Shift-T for resources not claimed by a specialized provider.
	Enable bool `json:"enable" yaml:"enable"`
}

// NewCrossplane returns a new Crossplane provider config with defaults.
// The provider is opt-in: callers must set Enable: true to activate it.
func NewCrossplane() Crossplane {
	return Crossplane{}
}

// NewArgo returns a new Argo provider config with defaults.
// ExpandChildApps is on so app-of-apps and ApplicationSets render their
// child Applications inline once Argo is enabled.
func NewArgo() Argo {
	return Argo{ExpandChildApps: true}
}

// NewOwnerRef returns a new OwnerRef provider config with defaults.
func NewOwnerRef() OwnerRef {
	return OwnerRef{}
}

// NewResourceTree returns a new ResourceTree config with defaults. The
// feature is opt-in: ResourceTree.Enable defaults to false and every
// provider also defaults to disabled, so the Shift-T binding only appears
// after the user explicitly enables both.
func NewResourceTree() ResourceTree {
	return ResourceTree{
		Providers: ResourceTreeProviders{
			Crossplane: NewCrossplane(),
			Argo:       NewArgo(),
			OwnerRef:   NewOwnerRef(),
		},
	}
}
