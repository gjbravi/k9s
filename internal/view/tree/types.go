// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

// Package tree provides a generic, provider-driven way to render child-resource
// trees for arbitrary Kubernetes resources (Crossplane Claims/XRs/MRs, ArgoCD
// Applications/ApplicationSets, owner-reference graphs, and future providers).
// Each provider knows how to recognize a root resource and how to walk its
// children into a uniform Node tree that the view layer renders as a table.
package tree

import (
	"context"

	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/dao"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Node represents a single row in the tree. Providers populate Columns with
// their own semantics (Crossplane: RESOURCE/SYNCED/READY/STATUS;
// Argo: KIND/SYNC/HEALTH/STATUS). The view is agnostic to the meaning.
type Node struct {
	// Kind is the Kubernetes kind used to render the "Kind/Name" label.
	Kind string
	// Name is the resource name.
	Name string
	// Namespace is the resource namespace (empty for cluster-scoped).
	Namespace string
	// GVR identifies the child resource so describe/yaml/navigation can
	// address it through the standard k9s accessors.
	GVR *client.GVR
	// Columns are the provider-specific right-hand columns (must match the
	// length of Provider.Columns()).
	Columns []string
	// IsOk drives row coloring: true → default fg, false → orange-red.
	IsOk bool
	// IsMissing marks dangling references (displayed as MISSING).
	IsMissing bool
	// Raw is an optional reference to the underlying resource for actions
	// that need to patch it (e.g. Crossplane pause/unpause).
	Raw *unstructured.Unstructured
	// Children are expanded tree children.
	Children []*Node
}

// FQN returns the "[namespace/]name" path used by k9s accessors.
func (n *Node) FQN() string {
	if n == nil {
		return ""
	}
	if n.Namespace != "" {
		return client.FQN(n.Namespace, n.Name)
	}
	return n.Name
}

// Provider walks a root resource into a Node tree. Each Provider is an umbrella
// for a given ecosystem (Crossplane, ArgoCD, owner-reference, …).
type Provider interface {
	// ID returns a short stable identifier (e.g. "crossplane", "argocd").
	ID() string
	// DisplayName returns a human-readable label used in the view title.
	DisplayName() string
	// Applies reports whether this provider can build a tree rooted at obj.
	Applies(gvr *client.GVR, obj *unstructured.Unstructured) bool
	// Columns returns the four right-hand column labels.
	Columns() []string
	// BuildRoot builds the root Node tree for obj.
	BuildRoot(ctx context.Context, f dao.Factory, gvr *client.GVR, obj *unstructured.Unstructured) (*Node, error)
}

// PausableProvider is an optional capability for providers that can pause or
// resume reconciliation of a given node.
type PausableProvider interface {
	Provider
	// SupportsPause reports whether pause/unpause applies to this node.
	SupportsPause(n *Node) bool
	// SetPaused toggles the paused state of the node.
	SetPaused(ctx context.Context, f dao.Factory, n *Node, paused bool) error
}

// StatusKind classifies a column value for cell coloring.
type StatusKind int

const (
	// StatusNeutral renders with the default foreground color.
	StatusNeutral StatusKind = iota
	// StatusOk renders as a healthy/positive value.
	StatusOk
	// StatusWarn renders as an in-progress or transitional value.
	StatusWarn
	// StatusError renders as a failed/degraded/missing value.
	StatusError
)

// StatusProvider is an optional capability for providers whose columns use
// non-standard status terms. Providers that don't implement this fall back to
// DefaultStatus.
type StatusProvider interface {
	Provider
	// Status maps a column value at column index col to a StatusKind.
	Status(col int, val string) StatusKind
}

// DefaultStatus maps the canonical Kubernetes / GitOps status terms to a
// StatusKind. Providers can call this from their own Status implementation as
// a sensible base layer.
func DefaultStatus(val string) StatusKind {
	switch val {
	case "":
		return StatusNeutral
	case conditionTrue, argoSyncSynced, argoHealthHealthy, "Available", conditionReady, statusRunning,
		statusActive, "Bound", "Succeeded", "Established", "Approved":
		return StatusOk
	case "False", "OutOfSync", "Degraded", "Missing", "MISSING", "Error",
		statusFailed, "Unknown", "CrashLoopBackOff", "ImagePullBackOff",
		"ErrImagePull", "Stalled", "Lost":
		return StatusError
	case "Reconciling", "Progressing", "Pending", "Updating", statusSuspended,
		"Terminating", "Unschedulable", "ContainerCreating", "Init":
		return StatusWarn
	}
	return StatusNeutral
}
