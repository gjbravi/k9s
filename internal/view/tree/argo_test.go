// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package tree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func argoApp(name, ns, sync, health, message string, resources []any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
		"status": map[string]any{
			"sync":      map[string]any{"status": sync},
			"health":    map[string]any{"status": health, "message": message},
			"resources": resources,
		},
	}}
}

func TestArgoApplies(t *testing.T) {
	p := NewArgoProvider(ArgoConfig{})

	app := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
	}}
	assert.True(t, p.Applies(nil, app))

	appSet := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "ApplicationSet",
	}}
	assert.True(t, p.Applies(nil, appSet))

	unrelated := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
	}}
	assert.False(t, p.Applies(nil, unrelated))

	assert.False(t, p.Applies(nil, nil))
}

func TestArgoBuildRootApp_NoChildren(t *testing.T) {
	p := NewArgoProvider(ArgoConfig{})
	app := argoApp("my-app", "argocd", "Synced", "Healthy", "", nil)

	n, err := p.BuildRoot(context.Background(), nil, nil, app)
	assert.NoError(t, err)
	assert.NotNil(t, n)
	assert.Equal(t, "Application", n.Kind)
	assert.Equal(t, "my-app", n.Name)
	assert.True(t, n.IsOk)
	assert.Equal(t, []string{"Application", "Synced", "Healthy", ""}, n.Columns)
}

func TestArgoBuildRootApp_WithResources(t *testing.T) {
	p := NewArgoProvider(ArgoConfig{ExpandChildApps: false})
	resources := []any{
		map[string]any{
			"group":     "apps",
			"version":   "v1",
			"kind":      "Deployment",
			"namespace": "ns1",
			"name":      "dep1",
			"status":    "Synced",
			"health":    map[string]any{"status": "Healthy"},
		},
		map[string]any{
			"group":     "",
			"version":   "v1",
			"kind":      "Service",
			"namespace": "ns1",
			"name":      "svc1",
			"status":    "OutOfSync",
			"health":    map[string]any{"status": "Degraded"},
			"message":   "probe failed",
		},
	}
	app := argoApp("my-app", "argocd", "Synced", "Healthy", "", resources)

	n, err := p.BuildRoot(context.Background(), nil, nil, app)
	assert.NoError(t, err)
	assert.Len(t, n.Children, 2)

	dep := n.Children[0]
	assert.Equal(t, "Deployment", dep.Kind)
	assert.Equal(t, "dep1", dep.Name)
	assert.Equal(t, "ns1", dep.Namespace)
	assert.Equal(t, "apps/v1/deployments", dep.GVR.String())
	assert.True(t, dep.IsOk)

	svc := n.Children[1]
	assert.Equal(t, "Service", svc.Kind)
	assert.Equal(t, "v1/services", svc.GVR.String())
	assert.False(t, svc.IsOk)
	assert.Equal(t, "probe failed", svc.Columns[3])
}

func TestArgoBuildRootAppSet_EmptyStatus(t *testing.T) {
	p := NewArgoProvider(ArgoConfig{})
	appSet := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "ApplicationSet",
		"metadata":   map[string]any{"name": "infra", "namespace": "argocd"},
		"status":     map[string]any{"conditions": []any{}},
	}}

	n, err := p.BuildRoot(context.Background(), nil, nil, appSet)
	assert.NoError(t, err)
	assert.Equal(t, "ApplicationSet", n.Kind)
	assert.True(t, n.IsOk)
	assert.Empty(t, n.Children)
}

func TestArgoChildGVR(t *testing.T) {
	uu := map[string]struct {
		group, version, kind string
		want                 string
	}{
		"core":        {"", "v1", "Service", "v1/services"},
		"apps":        {"apps", "v1", "Deployment", "apps/v1/deployments"},
		"no_version":  {"apps", "", "Deployment", "apps/v1/deployments"},
		"ingress":     {"networking.k8s.io", "v1", "Ingress", "networking.k8s.io/v1/ingresses"},
		"endpoints":   {"", "v1", "Endpoints", "v1/endpoints"},
		"networkpol":  {"networking.k8s.io", "v1", "NetworkPolicy", "networking.k8s.io/v1/networkpolicies"},
		"endpointslc": {"discovery.k8s.io", "v1", "EndpointSlice", "discovery.k8s.io/v1/endpointslices"},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			assert.Equal(t, u.want, argoChildGVR(nil, u.group, u.version, u.kind).String())
		})
	}
}
