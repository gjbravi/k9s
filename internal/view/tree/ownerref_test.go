// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package tree

import (
	"context"
	"testing"

	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/dao"
	"github.com/derailed/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
)

// fakeFactory is a minimal dao.Factory implementation for owner-ref tests.
type fakeFactory struct {
	listings map[*client.GVR][]runtime.Object
}

func (*fakeFactory) Client() client.Connection { return nil }
func (f *fakeFactory) Get(*client.GVR, string, bool, labels.Selector) (runtime.Object, error) {
	return nil, nil
}
func (f *fakeFactory) List(gvr *client.GVR, _ string, _ bool, _ labels.Selector) ([]runtime.Object, error) {
	return f.listings[gvr], nil
}
func (*fakeFactory) ForResource(string, *client.GVR) (informers.GenericInformer, error) {
	return nil, nil
}
func (*fakeFactory) CanForResource(string, *client.GVR, []string) (informers.GenericInformer, error) {
	return nil, nil
}
func (*fakeFactory) WaitForCacheSync()            {}
func (*fakeFactory) Forwarders() watch.Forwarders { return nil }
func (*fakeFactory) DeleteForwarder(string)       {}

var _ dao.Factory = (*fakeFactory)(nil)

func mkOwnerRef(uid string) map[string]any {
	return map[string]any{"uid": uid}
}

func mkResource(kind, name, ns, uid string, ownerUIDs ...string) *unstructured.Unstructured {
	owners := make([]any, 0, len(ownerUIDs))
	for _, u := range ownerUIDs {
		owners = append(owners, mkOwnerRef(u))
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       kind,
		"metadata": map[string]any{
			"name":            name,
			"namespace":       ns,
			"uid":             uid,
			"ownerReferences": owners,
		},
	}}
}

func TestOwnerRef_BuildRoot_DeploymentTree(t *testing.T) {
	dep := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      "web",
			"namespace": "default",
			"uid":       "dep-1",
		},
		"spec":   map[string]any{"replicas": int64(2)},
		"status": map[string]any{"readyReplicas": int64(2)},
	}}

	rs := mkResource("ReplicaSet", "web-abc", "default", "rs-1", "dep-1")
	rs.Object["spec"] = map[string]any{"replicas": int64(2)}
	rs.Object["status"] = map[string]any{"readyReplicas": int64(2)}

	pod1 := mkResource("Pod", "web-abc-1", "default", "pod-1", "rs-1")
	pod1.Object["spec"] = map[string]any{"containers": []any{map[string]any{"name": "c"}}}
	pod1.Object["status"] = map[string]any{
		"phase":             "Running",
		"containerStatuses": []any{map[string]any{"ready": true}},
	}
	pod2 := mkResource("Pod", "web-abc-2", "default", "pod-2", "rs-1")
	pod2.Object["spec"] = map[string]any{"containers": []any{map[string]any{"name": "c"}}}
	pod2.Object["status"] = map[string]any{
		"phase":             "Running",
		"containerStatuses": []any{map[string]any{"ready": true}},
	}
	other := mkResource("Pod", "unrelated", "default", "pod-3")

	f := &fakeFactory{listings: map[*client.GVR][]runtime.Object{
		client.NewGVR("apps/v1/replicasets"): {rs},
		client.NewGVR("v1/pods"):             {pod1, pod2, other},
	}}

	p := NewOwnerRefProvider()
	root, err := p.BuildRoot(context.Background(), f, client.NewGVR("apps/v1/deployments"), dep)
	assert.NoError(t, err)
	if assert.NotNil(t, root) {
		assert.Equal(t, "Deployment", root.Kind)
		assert.Equal(t, "2/2", root.Columns[1])
		if assert.Len(t, root.Children, 1) {
			rsNode := root.Children[0]
			assert.Equal(t, "ReplicaSet", rsNode.Kind)
			assert.Len(t, rsNode.Children, 2)
		}
	}
}

func TestOwnerRef_Status_RatioColumn(t *testing.T) {
	p := NewOwnerRefProvider()
	assert.Equal(t, StatusError, p.Status(1, "0/3"))
	assert.Equal(t, StatusWarn, p.Status(1, "1/3"))
	assert.Equal(t, StatusOk, p.Status(1, "3/3"))
	assert.Equal(t, StatusOk, p.Status(1, "0/0"))

	// Ratio detection only applies to col 1 (READY).
	assert.Equal(t, StatusNeutral, p.Status(0, "0/3"))

	// Falls through to DefaultStatus.
	assert.Equal(t, StatusError, p.Status(2, "Failed"))
	assert.Equal(t, StatusOk, p.Status(2, "Running"))
	assert.Equal(t, StatusNeutral, p.Status(2, "-"))
}

func TestOwnerRef_Applies_AnyObject(t *testing.T) {
	p := NewOwnerRefProvider()
	assert.True(t, p.Applies(nil, &unstructured.Unstructured{}))
	assert.False(t, p.Applies(nil, nil))
}

func TestOwnerRef_BuildRoot_ClusterScopedReturnsRoot(t *testing.T) {
	cr := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRole",
		"metadata":   map[string]any{"name": "view"},
	}}
	p := NewOwnerRefProvider()
	root, err := p.BuildRoot(context.Background(), &fakeFactory{}, nil, cr)
	assert.NoError(t, err)
	if assert.NotNil(t, root) {
		assert.Equal(t, "ClusterRole", root.Kind)
		assert.Empty(t, root.Children)
	}
}
