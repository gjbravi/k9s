// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/derailed/k9s/internal/dao"
	"github.com/derailed/k9s/internal/slogs"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/k9s/internal/view/tree"
	"github.com/derailed/tcell/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// ResourceTreeExtender binds Shift-T on any resource view to a generic,
// provider-driven tree view. At keypress time it inspects the selected
// resource and picks the first registered provider (Crossplane, ArgoCD, ...)
// that claims it.
type ResourceTreeExtender struct {
	ResourceViewer
}

// NewResourceTreeExtender returns a new extender.
func NewResourceTreeExtender(r ResourceViewer) ResourceViewer {
	e := &ResourceTreeExtender{ResourceViewer: r}
	e.AddBindKeysFn(e.bindKeys)
	return e
}

var resourceTreePreviewOnce sync.Once

func (e *ResourceTreeExtender) bindKeys(aa *ui.KeyActions) {
	if !e.anyProviderEnabled() {
		return
	}
	resourceTreePreviewOnce.Do(func() {
		slog.Warn("[preview] Resource tree feature is enabled (resourceTree.enable: true)")
	})
	aa.Add(ui.KeyShiftT, ui.NewKeyAction("Tree", e.treeCmd, true))
}

func (e *ResourceTreeExtender) treeCmd(evt *tcell.EventKey) *tcell.EventKey {
	path := e.GetTable().GetSelectedItem()
	if path == "" {
		return evt
	}

	u, err := e.fetchSelected(path)
	if err != nil {
		slog.Debug("Tree: cannot fetch selected resource",
			slogs.FQN, path,
			slogs.Error, err,
		)
		e.App().Flash().Warnf("Tree: %s", err)
		return nil
	}

	p := e.findProvider(u)
	if p == nil {
		e.App().Flash().Warnf("Tree: no provider matches %s/%s", e.GVR(), path)
		return nil
	}

	view := NewResourceTree(e.App(), e.GVR(), path, p)
	if err := e.App().inject(view, false); err != nil {
		e.App().Flash().Err(err)
	}
	return nil
}

// fetchSelected loads the selected resource as *unstructured.Unstructured so
// providers can inspect its apiVersion/kind/spec/status.
func (e *ResourceTreeExtender) fetchSelected(path string) (*unstructured.Unstructured, error) {
	res, err := dao.AccessorFor(e.App().factory, e.GVR())
	if err != nil {
		return nil, fmt.Errorf("no accessor for %s: %w", e.GVR(), err)
	}
	o, err := res.Get(context.Background(), path)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s: %w", path, err)
	}
	u, ok := asUnstructured(o)
	if !ok {
		return nil, fmt.Errorf("unsupported object type: %T", o)
	}
	return u, nil
}

func (e *ResourceTreeExtender) findProvider(u *unstructured.Unstructured) tree.Provider {
	for _, p := range e.enabledProviders() {
		if p.Applies(e.GVR(), u) {
			return p
		}
	}
	return nil
}

// enabledProviders consults the tree.Register / tree.Enabled registry so new
// providers can be added by dropping a file under internal/view/tree/ that
// calls Register from its init() — no edits to this file required.
func (e *ResourceTreeExtender) enabledProviders() []tree.Provider {
	return tree.Enabled(e.App().Config.K9s)
}

func (e *ResourceTreeExtender) anyProviderEnabled() bool {
	return len(e.enabledProviders()) > 0
}

// asUnstructured narrows a runtime.Object to *unstructured.Unstructured.
func asUnstructured(o runtime.Object) (*unstructured.Unstructured, bool) {
	u, ok := o.(*unstructured.Unstructured)
	return u, ok
}
