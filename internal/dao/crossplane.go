// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"fmt"

	"github.com/derailed/k9s/internal/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
)

// DirectGet fetches a resource directly from the API server using the dynamic
// client, bypassing the informer cache. This is necessary for child resources
// (MRs, XRs) whose informers may not be synced when the tree is first rendered.
// Falls back to factory.Get() if the dynamic client is not available (e.g., in tests).
func DirectGet(f Factory, gvr *client.GVR, ns, name string) (*unstructured.Unstructured, error) {
	conn := f.Client()
	if conn == nil {
		// Fallback to factory.Get() for tests or when no direct client is available.
		fqn := name
		if ns != "" {
			fqn = client.FQN(ns, name)
		}
		o, err := f.Get(gvr, fqn, true, labels.Everything())
		if err != nil {
			return nil, err
		}
		u, ok := o.(*unstructured.Unstructured)
		if !ok {
			return nil, fmt.Errorf("expected *unstructured.Unstructured but got %T", o)
		}
		return u, nil
	}
	dial, err := conn.DynDial()
	if err != nil {
		return nil, err
	}
	res := dial.Resource(gvr.GVR())
	if ns != "" {
		return res.Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	}
	return res.Get(context.Background(), name, metav1.GetOptions{})
}
