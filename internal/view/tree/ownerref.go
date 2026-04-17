// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package tree

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/config"
	"github.com/derailed/k9s/internal/dao"
	"github.com/derailed/k9s/internal/slogs"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/duration"
)

// Common condition / status / phase strings extracted as constants both for
// linter happiness (goconst) and to keep them in one place.
const (
	conditionTrue   = "True"
	conditionReady  = "Ready"
	statusFailed    = "Failed"
	statusComplete  = "Complete"
	statusRunning   = "Running"
	statusSuspended = "Suspended"
	statusActive    = "Active"
)

// defaultChildGVRs is the set of namespaced resource kinds the owner-reference
// walker scans to discover children. It covers the common Kubernetes workload
// graph (Deployment → ReplicaSet → Pod, StatefulSet → Pod, CronJob → Job →
// Pod, …) plus a few related resources users typically expect in a tree view.
//
// The list is intentionally conservative: each GVR in here is a List call
// against the apiserver per BuildRoot invocation, so adding rarely-useful
// kinds inflates the cost without much payoff.
var defaultChildGVRs = []*client.GVR{
	client.NewGVR("apps/v1/replicasets"),
	client.NewGVR("apps/v1/statefulsets"),
	client.NewGVR("apps/v1/daemonsets"),
	client.NewGVR("apps/v1/controllerrevisions"),
	client.NewGVR("v1/pods"),
	client.NewGVR("batch/v1/jobs"),
	client.NewGVR("batch/v1/cronjobs"),
	client.NewGVR("v1/services"),
	client.NewGVR("v1/endpoints"),
	client.NewGVR("discovery.k8s.io/v1/endpointslices"),
	client.NewGVR("v1/persistentvolumeclaims"),
	client.NewGVR("autoscaling/v2/horizontalpodautoscalers"),
	client.NewGVR("policy/v1/poddisruptionbudgets"),
}

// OwnerRefProvider walks the metadata.ownerReferences graph downward from a
// root resource. Children are discovered by listing a small, fixed set of
// commonly-owned namespaced resource kinds in the root's namespace, then
// indexing them by owner UID. This mirrors the way ArgoCD builds its
// resource tree from a per-cluster live state cache, but scoped to a single
// namespace per Shift-T invocation so it stays responsive.
//
// OwnerRefProvider is intended as the "matches anything" fallback registered
// after specialized providers like Crossplane and ArgoCD.
type OwnerRefProvider struct{}

// NewOwnerRefProvider returns a new owner-reference provider.
func NewOwnerRefProvider() *OwnerRefProvider {
	return &OwnerRefProvider{}
}

// ID implements Provider.
func (*OwnerRefProvider) ID() string { return "ownerref" }

// DisplayName implements Provider.
func (*OwnerRefProvider) DisplayName() string { return "Owner-Ref" }

// Columns implements Provider.
func (*OwnerRefProvider) Columns() []string {
	return []string{"KIND", "READY", "STATUS", "AGE"}
}

// Applies implements Provider. The owner-reference walker is the generic
// fallback: it claims any non-nil object so it produces a tree even when no
// specialized provider matches. The registry's order guarantees that
// specialized providers get first refusal.
func (*OwnerRefProvider) Applies(_ *client.GVR, obj *unstructured.Unstructured) bool {
	return obj != nil
}

// Status implements StatusProvider with a few extras over DefaultStatus that
// matter for raw workload trees (e.g. a Pod READY column of "0/3").
func (*OwnerRefProvider) Status(col int, val string) StatusKind {
	if val == "" || val == "-" {
		return StatusNeutral
	}
	if k := DefaultStatus(val); k != StatusNeutral {
		return k
	}
	if col == 1 { // READY column, e.g. "1/1", "0/3"
		if a, b, ok := splitRatio(val); ok {
			switch {
			case a == 0 && b > 0:
				return StatusError
			case a < b:
				return StatusWarn
			default:
				return StatusOk
			}
		}
	}
	return StatusNeutral
}

// BuildRoot implements Provider.
func (p *OwnerRefProvider) BuildRoot(ctx context.Context, f dao.Factory, gvr *client.GVR, obj *unstructured.Unstructured) (*Node, error) {
	if obj == nil {
		return nil, fmt.Errorf("ownerref: nil root object")
	}

	// Cluster-scoped roots aren't supported by the simple per-namespace
	// scan: returning the root alone is more useful than failing.
	ns := obj.GetNamespace()
	root := buildOwnerRefNode(obj, gvr)
	if ns == "" {
		return root, nil
	}

	idx, err := indexNamespace(ctx, f, ns)
	if err != nil {
		slog.Warn("ownerref: failed to index namespace", slogs.Namespace, ns, slogs.Error, err)
		return root, nil
	}

	visited := map[types.UID]bool{obj.GetUID(): true}
	p.attachChildren(root, obj.GetUID(), idx, visited)
	return root, nil
}

// childIndex maps an owner UID to the resources owned by it, paired with the
// GVR they were listed under.
type childIndex map[types.UID][]ownedResource

type ownedResource struct {
	obj *unstructured.Unstructured
	gvr *client.GVR
}

// indexNamespace lists each defaultChildGVR in ns and builds a reverse owner
// index. Errors on individual GVRs are logged and skipped so a missing CRD
// (e.g. EndpointSlice on very old clusters) doesn't fail the whole walk.
func indexNamespace(_ context.Context, f dao.Factory, ns string) (childIndex, error) {
	if f == nil {
		return nil, fmt.Errorf("ownerref: nil factory")
	}
	idx := childIndex{}
	for _, gvr := range defaultChildGVRs {
		objs, err := f.List(gvr, ns, true, labels.Everything())
		if err != nil {
			slog.Debug("ownerref: list failed", slogs.GVR, gvr, slogs.Namespace, ns, slogs.Error, err)
			continue
		}
		for _, o := range objs {
			u, ok := o.(*unstructured.Unstructured)
			if !ok {
				continue
			}
			for _, ref := range u.GetOwnerReferences() {
				idx[ref.UID] = append(idx[ref.UID], ownedResource{obj: u, gvr: gvr})
			}
		}
	}
	return idx, nil
}

// attachChildren recursively links children of parent into n, using uid as
// the key into idx and tracking visited UIDs to defend against any
// pathological owner-reference cycles.
func (p *OwnerRefProvider) attachChildren(n *Node, parentUID types.UID, idx childIndex, visited map[types.UID]bool) {
	for _, child := range idx[parentUID] {
		uid := child.obj.GetUID()
		if uid != "" && visited[uid] {
			continue
		}
		if uid != "" {
			visited[uid] = true
		}
		childNode := buildOwnerRefNode(child.obj, child.gvr)
		p.attachChildren(childNode, uid, idx, visited)
		n.Children = append(n.Children, childNode)
	}
}

// buildOwnerRefNode renders an unstructured into a Node with the owner-ref
// columns (KIND, READY, STATUS, AGE).
func buildOwnerRefNode(obj *unstructured.Unstructured, gvr *client.GVR) *Node {
	kind := obj.GetKind()
	ready := readyColumn(obj)
	status := statusColumn(obj)
	age := ageColumn(obj)

	n := &Node{
		Kind:      kind,
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		GVR:       gvr,
		Columns:   []string{kind, ready, status, age},
		IsOk:      isHealthy(obj, ready, status),
		Raw:       obj,
	}
	return n
}

// readyColumn returns a kind-aware READY string ("1/1", "True", "-").
func readyColumn(obj *unstructured.Unstructured) string {
	raw := obj.Object
	switch obj.GetKind() {
	case "Deployment", "ReplicaSet", "StatefulSet":
		desired := nestedInt(raw, "spec", "replicas")
		ready := nestedInt(raw, "status", "readyReplicas")
		return fmt.Sprintf("%d/%d", ready, desired)
	case "DaemonSet":
		desired := nestedInt(raw, "status", "desiredNumberScheduled")
		ready := nestedInt(raw, "status", "numberReady")
		return fmt.Sprintf("%d/%d", ready, desired)
	case "Job":
		desired := nestedInt(raw, "spec", "completions")
		if desired == 0 {
			desired = 1
		}
		succeeded := nestedInt(raw, "status", "succeeded")
		return fmt.Sprintf("%d/%d", succeeded, desired)
	case "Pod":
		desired := podContainerCount(raw)
		ready := podReadyContainerCount(raw)
		return fmt.Sprintf("%d/%d", ready, desired)
	}
	if v := readyConditionStatus(raw); v != "" {
		return v
	}
	return "-"
}

// statusColumn returns a kind-aware STATUS string (Pod phase, condition reason, …).
func statusColumn(obj *unstructured.Unstructured) string {
	raw := obj.Object
	switch obj.GetKind() {
	case "Pod":
		if phase, ok := nestedString(raw, "status", "phase"); ok && phase != "" {
			if reason := waitingReason(raw); reason != "" {
				return reason
			}
			return phase
		}
	case "Job":
		if v := nestedInt(raw, "status", "succeeded"); v > 0 {
			return statusComplete
		}
		if v := nestedInt(raw, "status", "failed"); v > 0 {
			return statusFailed
		}
		return statusRunning
	case "CronJob":
		if susp, _ := nestedBool(raw, "spec", "suspend"); susp {
			return statusSuspended
		}
		return statusActive
	}
	if reason := readyConditionReason(raw); reason != "" {
		return reason
	}
	return "-"
}

func ageColumn(obj *unstructured.Unstructured) string {
	t := obj.GetCreationTimestamp().Time
	if t.IsZero() {
		return "-"
	}
	return duration.HumanDuration(time.Since(t))
}

// isHealthy decides the row color: any column flagged StatusError makes the
// row orange. Pods/workloads with READY a/b where a==b and b>0 are healthy.
func isHealthy(obj *unstructured.Unstructured, ready, status string) bool {
	switch status {
	case statusFailed, "Error", "CrashLoopBackOff", "ImagePullBackOff":
		return false
	}
	if a, b, ok := splitRatio(ready); ok {
		if b == 0 {
			return true
		}
		return a == b
	}
	if v := readyConditionStatus(obj.Object); v != "" {
		return v == conditionTrue
	}
	return true
}

// ---- helpers ----

func nestedInt(obj map[string]any, keys ...string) int64 {
	v, ok := nested(obj, keys...)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

func nestedBool(obj map[string]any, keys ...string) (value, ok bool) {
	v, found := nested(obj, keys...)
	if !found {
		return false, false
	}
	b, isBool := v.(bool)
	return b, isBool
}

func nestedString(obj map[string]any, keys ...string) (string, bool) {
	v, ok := nested(obj, keys...)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func nested(obj map[string]any, keys ...string) (any, bool) {
	cur := any(obj)
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[k]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func podContainerCount(obj map[string]any) int64 {
	cs, ok := nested(obj, "spec", "containers")
	if !ok {
		return 0
	}
	arr, ok := cs.([]any)
	if !ok {
		return 0
	}
	return int64(len(arr))
}

func podReadyContainerCount(obj map[string]any) int64 {
	cs, ok := nested(obj, "status", "containerStatuses")
	if !ok {
		return 0
	}
	arr, ok := cs.([]any)
	if !ok {
		return 0
	}
	var ready int64
	for _, c := range arr {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if r, _ := m["ready"].(bool); r {
			ready++
		}
	}
	return ready
}

func waitingReason(obj map[string]any) string {
	cs, ok := nested(obj, "status", "containerStatuses")
	if !ok {
		return ""
	}
	arr, ok := cs.([]any)
	if !ok {
		return ""
	}
	for _, c := range arr {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		state, ok := m["state"].(map[string]any)
		if !ok {
			continue
		}
		waiting, ok := state["waiting"].(map[string]any)
		if !ok {
			continue
		}
		if reason, _ := waiting["reason"].(string); reason != "" {
			return reason
		}
	}
	return ""
}

func readyConditionStatus(obj map[string]any) string {
	cs, ok := nested(obj, "status", "conditions")
	if !ok {
		return ""
	}
	arr, ok := cs.([]any)
	if !ok {
		return ""
	}
	for _, c := range arr {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == conditionReady {
			s, _ := m["status"].(string)
			return s
		}
	}
	return ""
}

func readyConditionReason(obj map[string]any) string {
	cs, ok := nested(obj, "status", "conditions")
	if !ok {
		return ""
	}
	arr, ok := cs.([]any)
	if !ok {
		return ""
	}
	for _, c := range arr {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == conditionReady {
			r, _ := m["reason"].(string)
			return r
		}
	}
	return ""
}

func splitRatio(s string) (a, b int64, ok bool) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[0], "%d", &a); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &b); err != nil {
		return 0, 0, false
	}
	return a, b, true
}

func init() {
	Register("ownerref", func(cfg *config.K9s) Provider {
		if cfg == nil {
			return nil
		}
		if cfg.ResourceTree.Providers.OwnerRef.Enable {
			return NewOwnerRefProvider()
		}
		return nil
	})
}
