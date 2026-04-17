// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package tree

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/config"
	"github.com/derailed/k9s/internal/dao"
	"github.com/derailed/k9s/internal/slogs"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

const (
	crossplaneGroupSuffix      = "crossplane.io"
	crossplanePausedAnnotation = "crossplane.io/paused"
	crossplaneCompAnnotation   = "crossplane.io/composition-resource-name"
)

// CrossplaneProvider walks Claim → XR → MR hierarchies and optional connection
// secrets. It matches the semantics of `crossplane beta trace`.
type CrossplaneProvider struct{}

// NewCrossplaneProvider returns a new Crossplane provider.
func NewCrossplaneProvider() *CrossplaneProvider {
	return &CrossplaneProvider{}
}

// ID implements Provider.
func (*CrossplaneProvider) ID() string { return "crossplane" }

// DisplayName implements Provider.
func (*CrossplaneProvider) DisplayName() string { return "Crossplane" }

// Columns implements Provider.
func (*CrossplaneProvider) Columns() []string {
	return []string{"RESOURCE", "SYNCED", "READY", "STATUS"}
}

// Applies implements Provider. A resource is considered a Crossplane root when
// it has either spec.resourceRef / spec.resourceRefs, or it carries Synced/Ready
// conditions typical of Crossplane managed resources.
func (*CrossplaneProvider) Applies(_ *client.GVR, obj *unstructured.Unstructured) bool {
	if obj == nil {
		return false
	}
	raw := obj.Object
	if isCrossplaneClaim(raw) || isCrossplaneXR(raw) {
		return true
	}
	if strings.Contains(obj.GroupVersionKind().Group, crossplaneGroupSuffix) {
		return true
	}
	return hasCrossplaneConditions(raw)
}

// BuildRoot implements Provider.
func (p *CrossplaneProvider) BuildRoot(_ context.Context, f dao.Factory, gvr *client.GVR, obj *unstructured.Unstructured) (*Node, error) {
	if obj == nil {
		return nil, fmt.Errorf("crossplane: nil root object")
	}
	return p.buildEntry(f, obj, gvr), nil
}

func (p *CrossplaneProvider) buildEntry(f dao.Factory, obj *unstructured.Unstructured, gvr *client.GVR) *Node {
	synced, ready, message := crossplaneConditions(obj.Object)
	readyReason := crossplaneReadyReason(obj.Object)
	compResource := crossplaneCompositionResource(obj.Object)

	statusText := readyReason
	if message != "" && (synced != "True" || ready != "True") {
		statusText = message
	}

	n := &Node{
		Kind:      obj.GetKind(),
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		GVR:       gvr,
		Columns:   []string{compResource, synced, ready, statusText},
		IsOk:      synced == "True" && ready == "True",
		Raw:       obj,
	}

	raw := obj.Object
	if isCrossplaneClaim(raw) {
		n.Children = append(n.Children, p.resolveRef(f, raw)...)
	}
	if isCrossplaneXR(raw) {
		n.Children = append(n.Children, p.resolveRefs(f, raw)...)
	}
	n.Children = append(n.Children, p.resolveSecret(f, raw)...)
	return n
}

func (p *CrossplaneProvider) resolveRef(f dao.Factory, obj map[string]any) []*Node {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return nil
	}
	ref, ok := spec["resourceRef"].(map[string]any)
	if !ok {
		return nil
	}
	apiVersion, _ := ref["apiVersion"].(string)
	kind, _ := ref["kind"].(string)
	refName, _ := ref["name"].(string)
	if apiVersion == "" || kind == "" || refName == "" {
		return nil
	}
	childGVR := ResolveGVRForFactory(f, apiVersion, kind)
	child, err := dao.DirectGet(f, childGVR, "", refName)
	if err != nil || child == nil {
		slog.Warn("Missing resourceRef", slogs.GVR, childGVR, slogs.FQN, refName, slogs.Error, err)
		return []*Node{missingNode(kind, refName, "", childGVR, 4)}
	}
	return []*Node{p.buildEntry(f, child, childGVR)}
}

func (p *CrossplaneProvider) resolveRefs(f dao.Factory, obj map[string]any) []*Node {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return nil
	}
	refs, ok := spec["resourceRefs"].([]any)
	if !ok {
		return nil
	}
	var out []*Node
	for _, r := range refs {
		ref, ok := r.(map[string]any)
		if !ok {
			continue
		}
		apiVersion, _ := ref["apiVersion"].(string)
		kind, _ := ref["kind"].(string)
		refName, _ := ref["name"].(string)
		refNs, _ := ref["namespace"].(string)
		if apiVersion == "" || kind == "" || refName == "" {
			continue
		}
		childGVR := ResolveGVRForFactory(f, apiVersion, kind)
		child, err := dao.DirectGet(f, childGVR, refNs, refName)
		if err != nil || child == nil {
			slog.Warn("Missing resourceRefs target", slogs.GVR, childGVR, slogs.FQN, refName, slogs.Error, err)
			out = append(out, missingNode(kind, refName, refNs, childGVR, 4))
			continue
		}
		out = append(out, p.buildEntry(f, child, childGVR))
	}
	return out
}

func (p *CrossplaneProvider) resolveSecret(f dao.Factory, obj map[string]any) []*Node {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return nil
	}
	secretRef, ok := spec["writeConnectionSecretToRef"].(map[string]any)
	if !ok {
		return nil
	}
	secretName, _ := secretRef["name"].(string)
	secretNs, _ := secretRef["namespace"].(string)
	if secretName == "" {
		return nil
	}
	fqn := secretName
	if secretNs != "" {
		fqn = client.FQN(secretNs, secretName)
	}
	_, err := f.Get(client.SecGVR, fqn, true, labels.Everything())
	n := &Node{
		Kind:      "Secret",
		Name:      secretName,
		Namespace: secretNs,
		GVR:       client.SecGVR,
		Columns:   []string{"", "True", "True", "Available"},
		IsOk:      true,
	}
	if err != nil {
		n.Columns = []string{"", "", "", "MISSING"}
		n.IsOk = false
		n.IsMissing = true
	}
	return []*Node{n}
}

// SupportsPause implements PausableProvider. Only Crossplane managed/composite
// resources (not Secret leaves) can be paused.
func (*CrossplaneProvider) SupportsPause(n *Node) bool {
	if n == nil || n.Raw == nil {
		return false
	}
	return strings.Contains(n.Raw.GroupVersionKind().Group, crossplaneGroupSuffix)
}

// SetPaused implements PausableProvider by patching the
// crossplane.io/paused annotation on the referenced resource.
func (*CrossplaneProvider) SetPaused(ctx context.Context, f dao.Factory, n *Node, paused bool) error {
	if n == nil || n.GVR == nil {
		return fmt.Errorf("crossplane: invalid node")
	}
	conn := f.Client()
	if conn == nil {
		return fmt.Errorf("no client connection")
	}
	dial, err := conn.DynDial()
	if err != nil {
		return err
	}

	ns := n.Namespace
	if client.IsClusterScoped(ns) {
		ns = ""
	}
	res := dial.Resource(n.GVR.GVR())

	val := any("true")
	if !paused {
		val = nil
	}
	patch, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				crossplanePausedAnnotation: val,
			},
		},
	})

	if ns != "" {
		_, err = res.Namespace(ns).Patch(ctx, n.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	} else {
		_, err = res.Patch(ctx, n.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	}
	return err
}

// ---- Crossplane unstructured helpers ----

func isCrossplaneClaim(obj map[string]any) bool {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = spec["resourceRef"]
	return ok
}

func isCrossplaneXR(obj map[string]any) bool {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = spec["resourceRefs"]
	return ok
}

func hasCrossplaneConditions(obj map[string]any) bool {
	status, ok := obj["status"].(map[string]any)
	if !ok {
		return false
	}
	conds, ok := status["conditions"].([]any)
	if !ok {
		return false
	}
	for _, c := range conds {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		t, _ := cond["type"].(string)
		if t == "Synced" || t == "Ready" {
			return true
		}
	}
	return false
}

func crossplaneConditions(obj map[string]any) (synced, ready, message string) {
	synced, ready = "-", "-"
	status, ok := obj["status"].(map[string]any)
	if !ok {
		return
	}
	conds, ok := status["conditions"].([]any)
	if !ok {
		return
	}
	for _, c := range conds {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		t, _ := cond["type"].(string)
		s, _ := cond["status"].(string)
		m, _ := cond["message"].(string)
		switch t {
		case "Synced":
			synced = s
			if s != "True" && message == "" && m != "" {
				message = m
			}
		case "Ready":
			ready = s
			if s != "True" && message == "" && m != "" {
				message = m
			}
		}
	}
	return
}

func crossplaneReadyReason(obj map[string]any) string {
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
		if t, _ := cond["type"].(string); t == "Ready" {
			reason, _ := cond["reason"].(string)
			return reason
		}
	}
	return ""
}

func crossplaneCompositionResource(obj map[string]any) string {
	metadata, ok := obj["metadata"].(map[string]any)
	if !ok {
		return ""
	}
	annotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := annotations[crossplaneCompAnnotation].(string)
	return name
}

func init() {
	Register("crossplane", func(cfg *config.K9s) Provider {
		if cfg == nil || !cfg.ResourceTree.Providers.Crossplane.Enable {
			return nil
		}
		return NewCrossplaneProvider()
	})
}

// missingNode builds a placeholder Node representing a dangling reference.
// colCount lets each provider control the column layout.
func missingNode(kind, name, ns string, gvr *client.GVR, colCount int) *Node {
	cols := make([]string, colCount)
	if colCount > 0 {
		cols[colCount-1] = "MISSING"
	}
	return &Node{
		Kind:      kind,
		Name:      name,
		Namespace: ns,
		GVR:       gvr,
		Columns:   cols,
		IsOk:      false,
		IsMissing: true,
	}
}
