// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package tree

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIsCrossplaneClaim(t *testing.T) {
	uu := map[string]struct {
		obj map[string]any
		e   bool
	}{
		"claim_with_resourceRef": {
			obj: map[string]any{
				"spec": map[string]any{
					"resourceRef": map[string]any{
						"apiVersion": "database.example.org/v1alpha1",
						"kind":       "XPostgreSQLInstance",
						"name":       "my-db-xyz",
					},
				},
			},
			e: true,
		},
		"xr_with_resourceRefs": {
			obj: map[string]any{
				"spec": map[string]any{
					"resourceRefs": []any{map[string]any{"name": "mr1"}},
				},
			},
			e: false,
		},
		"no_spec":      {obj: map[string]any{"metadata": map[string]any{"name": "test"}}, e: false},
		"empty_spec":   {obj: map[string]any{"spec": map[string]any{}}, e: false},
		"spec_not_map": {obj: map[string]any{"spec": "invalid"}, e: false},
		"empty_object": {obj: map[string]any{}, e: false},
	}
	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			assert.Equal(t, u.e, isCrossplaneClaim(u.obj))
		})
	}
}

func TestIsCrossplaneXR(t *testing.T) {
	uu := map[string]struct {
		obj map[string]any
		e   bool
	}{
		"xr_with_resourceRefs": {
			obj: map[string]any{
				"spec": map[string]any{
					"resourceRefs": []any{map[string]any{"name": "mr1"}, map[string]any{"name": "mr2"}},
				},
			},
			e: true,
		},
		"claim_with_resourceRef": {
			obj: map[string]any{"spec": map[string]any{"resourceRef": map[string]any{"name": "xr1"}}},
			e:   false,
		},
		"empty_object": {obj: map[string]any{}, e: false},
	}
	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			assert.Equal(t, u.e, isCrossplaneXR(u.obj))
		})
	}
}

func TestCrossplaneConditions(t *testing.T) {
	uu := map[string]struct {
		obj                   map[string]any
		eSynced, eReady, eMsg string
	}{
		"both_true": {
			obj: map[string]any{"status": map[string]any{"conditions": []any{
				map[string]any{"type": "Synced", "status": "True"},
				map[string]any{"type": "Ready", "status": "True"},
			}}},
			eSynced: "True", eReady: "True",
		},
		"synced_false": {
			obj: map[string]any{"status": map[string]any{"conditions": []any{
				map[string]any{"type": "Synced", "status": "False", "message": "sync error"},
				map[string]any{"type": "Ready", "status": "True"},
			}}},
			eSynced: "False", eReady: "True", eMsg: "sync error",
		},
		"no_status":     {obj: map[string]any{}, eSynced: "-", eReady: "-"},
		"no_conditions": {obj: map[string]any{"status": map[string]any{}}, eSynced: "-", eReady: "-"},
	}
	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			synced, ready, msg := crossplaneConditions(u.obj)
			assert.Equal(t, u.eSynced, synced)
			assert.Equal(t, u.eReady, ready)
			assert.Equal(t, u.eMsg, msg)
		})
	}
}

func TestResolveGVR(t *testing.T) {
	uu := map[string]struct {
		apiVersion, kind string
		e                string
	}{
		"core_v1":        {"v1", "Secret", "v1/secrets"},
		"group_v1alpha1": {"database.example.org/v1alpha1", "PostgreSQLInstance", "database.example.org/v1alpha1/postgresqlinstances"},
		"group_v1":       {"apiextensions.crossplane.io/v1", "Composition", "apiextensions.crossplane.io/v1/compositions"},
		"group_v1beta1":  {"s3.aws.crossplane.io/v1beta1", "Bucket", "s3.aws.crossplane.io/v1beta1/buckets"},
		"ingress":        {"networking.k8s.io/v1", "Ingress", "networking.k8s.io/v1/ingresses"},
		"endpoints":      {"v1", "Endpoints", "v1/endpoints"},
		"networkpolicy":  {"networking.k8s.io/v1", "NetworkPolicy", "networking.k8s.io/v1/networkpolicies"},
		"storageclass":   {"storage.k8s.io/v1", "StorageClass", "storage.k8s.io/v1/storageclasses"},
		"y_after_vowel":  {"v1", "Gateway", "v1/gateways"},
	}
	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			assert.Equal(t, u.e, ResolveGVR(u.apiVersion, u.kind).String())
		})
	}
}

func TestCrossplaneApplies(t *testing.T) {
	p := NewCrossplaneProvider()

	claim := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"resourceRef": map[string]any{"name": "xr"}},
	}}
	assert.True(t, p.Applies(nil, claim))

	xr := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"resourceRefs": []any{}},
	}}
	assert.True(t, p.Applies(nil, xr))

	byGroup := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "s3.aws.crossplane.io/v1beta1",
		"kind":       "Bucket",
	}}
	assert.True(t, p.Applies(nil, byGroup))

	byCondition := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Synced", "status": "True"},
		}},
	}}
	assert.True(t, p.Applies(nil, byCondition))

	unrelated := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
	}}
	assert.False(t, p.Applies(nil, unrelated))
}

func TestCrossplaneSupportsPause(t *testing.T) {
	p := NewCrossplaneProvider()

	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "s3.aws.crossplane.io/v1beta1",
		"kind":       "Bucket",
	}}
	assert.True(t, p.SupportsPause(&Node{Raw: u}))

	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
	}}
	assert.False(t, p.SupportsPause(&Node{Raw: secret}))

	assert.False(t, p.SupportsPause(nil))
	assert.False(t, p.SupportsPause(&Node{}))
}
