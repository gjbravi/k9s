// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package tree

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/config"
	"github.com/derailed/k9s/internal/dao"
	"github.com/derailed/k9s/internal/slogs"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	argoGroup         = "argoproj.io"
	argoKindApp       = "Application"
	argoKindAppSet    = "ApplicationSet"
	argoAppNamespace  = "argocd"
	argoSyncSynced    = "Synced"
	argoHealthHealthy = "Healthy"
)

// ArgoConfig tunes Argo provider behavior.
type ArgoConfig struct {
	// ExpandChildApps recursively resolves Application children discovered in
	// an ApplicationSet (or Application-of-Applications) into their own
	// resource subtrees. Cycles and depth are bounded to prevent runaway work.
	ExpandChildApps bool
	// MaxDepth caps recursive expansion of child Applications. Zero disables
	// the cap. Defaults to 5 when unset.
	MaxDepth int
}

// ArgoProvider walks ArgoCD Applications and ApplicationSets. Children come
// from status.resources on the Application (each managed k8s resource) and
// from status.resources on the ApplicationSet (each generated Application).
// When ExpandChildApps is true, child Applications are recursively walked.
type ArgoProvider struct {
	cfg ArgoConfig
}

// NewArgoProvider returns a new ArgoCD provider.
func NewArgoProvider(cfg ArgoConfig) *ArgoProvider {
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 5
	}
	return &ArgoProvider{cfg: cfg}
}

// ID implements Provider.
func (*ArgoProvider) ID() string { return "argocd" }

// DisplayName implements Provider.
func (*ArgoProvider) DisplayName() string { return "ArgoCD" }

// Columns implements Provider.
func (*ArgoProvider) Columns() []string {
	return []string{"KIND", "SYNC", "HEALTH", "STATUS"}
}

// Applies implements Provider.
func (*ArgoProvider) Applies(_ *client.GVR, obj *unstructured.Unstructured) bool {
	if obj == nil {
		return false
	}
	gvk := obj.GroupVersionKind()
	if gvk.Group != argoGroup {
		return false
	}
	return gvk.Kind == argoKindApp || gvk.Kind == argoKindAppSet
}

// BuildRoot implements Provider.
func (p *ArgoProvider) BuildRoot(ctx context.Context, f dao.Factory, gvr *client.GVR, obj *unstructured.Unstructured) (*Node, error) {
	if obj == nil {
		return nil, fmt.Errorf("argocd: nil root object")
	}
	visited := map[string]bool{nodeKey(obj): true}
	switch obj.GetKind() {
	case argoKindApp:
		return p.buildAppNode(ctx, f, gvr, obj, visited, 0), nil
	case argoKindAppSet:
		return p.buildAppSetNode(ctx, f, gvr, obj, visited, 0), nil
	default:
		return nil, fmt.Errorf("argocd: unsupported kind %q", obj.GetKind())
	}
}

// buildAppNode turns an Argo Application into a tree: the Application itself
// plus a child per entry in status.resources. When a child is itself an Argo
// Application (app-of-apps) and ExpandChildApps is set, that Application is
// recursively resolved to its own resources.
func (p *ArgoProvider) buildAppNode(ctx context.Context, f dao.Factory, gvr *client.GVR, obj *unstructured.Unstructured, visited map[string]bool, depth int) *Node {
	sync, health, message := argoAppStatus(obj.Object)
	root := &Node{
		Kind:      obj.GetKind(),
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		GVR:       gvr,
		Columns:   []string{argoKindApp, sync, health, message},
		IsOk:      sync == argoSyncSynced && health == argoHealthHealthy,
		Raw:       obj,
	}

	resources, ok := nestedSlice(obj.Object, "status", "resources")
	if !ok {
		return root
	}
	for _, r := range resources {
		entry, ok := r.(map[string]any)
		if !ok {
			continue
		}
		child := p.buildResourceNode(ctx, f, entry, visited, depth)
		if child != nil {
			root.Children = append(root.Children, child)
		}
	}
	return root
}

// buildResourceNode turns a single status.resources entry into a Node. If the
// entry references another Argo Application and expansion is enabled (and
// within depth/cycle limits), the referenced Application is fetched and
// expanded into its own subtree.
func (p *ArgoProvider) buildResourceNode(ctx context.Context, f dao.Factory, entry map[string]any, visited map[string]bool, depth int) *Node {
	group, _ := entry["group"].(string)
	version, _ := entry["version"].(string)
	kind, _ := entry["kind"].(string)
	name, _ := entry["name"].(string)
	namespace, _ := entry["namespace"].(string)
	if kind == "" || name == "" {
		return nil
	}

	syncStatus, _ := entry["status"].(string)
	if syncStatus == "" {
		syncStatus = "-"
	}
	healthStatus := "-"
	if h, ok := entry["health"].(map[string]any); ok {
		if s, _ := h["status"].(string); s != "" {
			healthStatus = s
		}
	}
	message, _ := entry["message"].(string)

	childGVR := argoChildGVR(f, group, version, kind)
	node := &Node{
		Kind:      kind,
		Name:      name,
		Namespace: namespace,
		GVR:       childGVR,
		Columns:   []string{kind, syncStatus, healthStatus, message},
		IsOk:      syncStatus == argoSyncSynced && (healthStatus == argoHealthHealthy || healthStatus == "-"),
	}

	if !p.cfg.ExpandChildApps {
		return node
	}
	if group != argoGroup || (kind != argoKindApp && kind != argoKindAppSet) {
		return node
	}
	if p.cfg.MaxDepth > 0 && depth+1 >= p.cfg.MaxDepth {
		return node
	}

	// Argo stores the app namespace in the resource entry when it differs
	// from the parent app's namespace; otherwise fall back to the standard
	// "argocd" namespace.
	lookupNs := namespace
	if lookupNs == "" {
		lookupNs = argoAppNamespace
	}

	child, err := dao.DirectGet(f, childGVR, lookupNs, name)
	if err != nil || child == nil {
		slog.Warn("Argo child app not found", slogs.GVR, childGVR, slogs.FQN, client.FQN(lookupNs, name), slogs.Error, err)
		return node
	}

	key := nodeKey(child)
	if visited[key] {
		return node
	}
	visited[key] = true

	var expanded *Node
	if kind == argoKindApp {
		expanded = p.buildAppNode(ctx, f, childGVR, child, visited, depth+1)
	} else {
		expanded = p.buildAppSetNode(ctx, f, childGVR, child, visited, depth+1)
	}
	// Merge the expanded subtree's columns back onto this node so the row
	// continues to reflect the parent app's reported status, but inherit
	// its children for recursive display.
	node.Children = expanded.Children
	node.Raw = expanded.Raw
	return node
}

// buildAppSetNode turns an Argo ApplicationSet into a tree: the ApplicationSet
// itself, plus a child per generated Application (read from status.resources).
// Each generated Application is then recursively expanded into its resources
// when ExpandChildApps is set.
func (p *ArgoProvider) buildAppSetNode(ctx context.Context, f dao.Factory, gvr *client.GVR, obj *unstructured.Unstructured, visited map[string]bool, depth int) *Node {
	message := argoAppSetMessage(obj.Object)
	root := &Node{
		Kind:      obj.GetKind(),
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		GVR:       gvr,
		Columns:   []string{argoKindAppSet, "-", "-", message},
		IsOk:      message == "",
		Raw:       obj,
	}

	resources, ok := nestedSlice(obj.Object, "status", "resources")
	if !ok {
		return root
	}
	for _, r := range resources {
		entry, ok := r.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := entry["kind"].(string)
		// An AppSet's status.resources is expected to list generated
		// Applications, but be defensive and render anything present.
		if kind == "" {
			entry["kind"] = argoKindApp
		}
		child := p.buildResourceNode(ctx, f, entry, visited, depth)
		if child != nil {
			root.Children = append(root.Children, child)
		}
	}
	return root
}

// argoAppStatus extracts sync.status, health.status, and an informative message
// from an Application's status sub-document.
func argoAppStatus(obj map[string]any) (sync, health, message string) {
	sync, health = "-", "-"
	status, ok := obj["status"].(map[string]any)
	if !ok {
		return
	}
	if s, ok := status["sync"].(map[string]any); ok {
		if v, _ := s["status"].(string); v != "" {
			sync = v
		}
	}
	if h, ok := status["health"].(map[string]any); ok {
		if v, _ := h["status"].(string); v != "" {
			health = v
		}
		if m, _ := h["message"].(string); m != "" {
			message = m
		}
	}
	if message == "" {
		if op, ok := status["operationState"].(map[string]any); ok {
			if m, _ := op["message"].(string); m != "" {
				message = m
			}
		}
	}
	if message == "" {
		if conds, ok := status["conditions"].([]any); ok {
			for _, c := range conds {
				cond, ok := c.(map[string]any)
				if !ok {
					continue
				}
				if t, _ := cond["type"].(string); strings.Contains(strings.ToLower(t), "error") {
					if m, _ := cond["message"].(string); m != "" {
						message = m
						break
					}
				}
			}
		}
	}
	return
}

// argoAppSetMessage returns the first error/warning message from the AppSet
// status.conditions, or empty when the ApplicationSet is healthy.
func argoAppSetMessage(obj map[string]any) string {
	status, ok := obj["status"].(map[string]any)
	if !ok {
		return ""
	}
	conds, ok := status["conditions"].([]any)
	if !ok {
		return ""
	}
	for _, c := range conds {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		s, _ := cond["status"].(string)
		if s == "True" {
			t, _ := cond["type"].(string)
			if strings.Contains(strings.ToLower(t), "error") || strings.Contains(strings.ToLower(t), "warning") {
				m, _ := cond["message"].(string)
				if m != "" {
					return m
				}
			}
		}
	}
	return ""
}

// argoChildGVR resolves the GVR of an Argo status.resources entry. Core
// resources ship with empty group; the discovery cache is preferred when a
// factory is available so non-English plurals (Ingress, EndpointSlice, …) and
// custom CRDs resolve correctly.
func argoChildGVR(f dao.Factory, group, version, kind string) *client.GVR {
	if version == "" {
		version = "v1"
	}
	apiVersion := version
	if group != "" {
		apiVersion = group + "/" + version
	}
	return ResolveGVRForFactory(f, apiVersion, kind)
}

func init() {
	Register("argocd", func(cfg *config.K9s) Provider {
		if cfg == nil {
			return nil
		}
		if cfg.ResourceTree.Providers.Argo.Enable {
			return NewArgoProvider(ArgoConfig{
				ExpandChildApps: cfg.ResourceTree.Providers.Argo.ExpandChildApps,
			})
		}
		return nil
	})
}

// nestedSlice extracts a []any at path keys from obj. Returns false when the
// path does not exist or the terminal value is not a slice.
func nestedSlice(obj map[string]any, keys ...string) ([]any, bool) {
	cur := obj
	for i, k := range keys {
		if i == len(keys)-1 {
			s, ok := cur[k].([]any)
			return s, ok
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return nil, false
}

// nodeKey returns a stable identifier for cycle detection.
func nodeKey(u *unstructured.Unstructured) string {
	if u == nil {
		return ""
	}
	return u.GroupVersionKind().String() + "|" + u.GetNamespace() + "/" + u.GetName()
}
