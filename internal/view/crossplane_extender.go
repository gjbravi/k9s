// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/derailed/k9s/internal/dao"
	"github.com/derailed/k9s/internal/slogs"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/tcell/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// CrossplaneTraceExtender adds a Crossplane trace key binding to a resource viewer.
type CrossplaneTraceExtender struct {
	ResourceViewer
}

// NewCrossplaneTraceExtender returns a new extender.
func NewCrossplaneTraceExtender(r ResourceViewer) ResourceViewer {
	e := &CrossplaneTraceExtender{ResourceViewer: r}
	e.AddBindKeysFn(e.bindKeys)
	return e
}

func (e *CrossplaneTraceExtender) bindKeys(aa *ui.KeyActions) {
	if !e.App().Config.K9s.Crossplane.Enable {
		return
	}
	aa.Add(ui.KeyShiftT, ui.NewKeyAction("Trace", e.traceCmd, true))
}

func (e *CrossplaneTraceExtender) traceCmd(evt *tcell.EventKey) *tcell.EventKey {
	path := e.GetTable().GetSelectedItem()
	if path == "" {
		return evt
	}

	// Validate that the selected resource is a Crossplane resource before opening trace.
	if err := e.validateCrossplaneResource(path); err != nil {
		slog.Debug("Trace not applicable for resource",
			slogs.FQN, path,
			slogs.Error, err,
		)
		e.App().Flash().Warnf("Trace: %s", err)
		return nil
	}

	trace := NewCrossplaneTrace(e.App(), e.GVR(), path)
	if err := e.App().inject(trace, false); err != nil {
		e.App().Flash().Err(err)
	}

	return nil
}

// validateCrossplaneResource checks if the resource has Crossplane characteristics
// (resourceRef, resourceRefs, or Crossplane conditions).
func (e *CrossplaneTraceExtender) validateCrossplaneResource(path string) error {
	res, err := dao.AccessorFor(e.App().factory, e.GVR())
	if err != nil {
		return fmt.Errorf("no accessor for %s: %w", e.GVR(), err)
	}

	o, err := res.Get(context.Background(), path)
	if err != nil {
		return fmt.Errorf("failed to get %s: %w", path, err)
	}

	u, ok := asUnstructured(o)
	if !ok {
		return fmt.Errorf("unsupported object type: %T", o)
	}

	obj := u.Object

	// Check for Crossplane resource indicators.
	if isCrossplaneClaim(obj) || isCrossplaneXR(obj) {
		return nil
	}

	// Check for Crossplane conditions (Synced/Ready).
	if hasCrossplaneConditions(obj) {
		return nil
	}

	return fmt.Errorf("not a Crossplane resource (no resourceRef, resourceRefs, or Crossplane conditions)")
}

// hasCrossplaneConditions checks if the resource has Synced or Ready conditions
// typical of Crossplane managed resources.
func hasCrossplaneConditions(obj map[string]any) bool {
	status, ok := obj["status"].(map[string]any)
	if !ok {
		return false
	}
	conditions, ok := status["conditions"].([]any)
	if !ok {
		return false
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		condType, _ := cond["type"].(string)
		if condType == "Synced" || condType == "Ready" {
			return true
		}
	}
	return false
}

func asUnstructured(o runtime.Object) (*unstructured.Unstructured, bool) {
	switch v := o.(type) {
	case *unstructured.Unstructured:
		return v, true
	default:
		return nil, false
	}
}
