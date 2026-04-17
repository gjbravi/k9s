// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
					"resourceRefs": []any{
						map[string]any{"name": "mr1"},
					},
				},
			},
			e: false,
		},
		"no_spec": {
			obj: map[string]any{
				"metadata": map[string]any{"name": "test"},
			},
			e: false,
		},
		"empty_spec": {
			obj: map[string]any{
				"spec": map[string]any{},
			},
			e: false,
		},
		"spec_not_map": {
			obj: map[string]any{
				"spec": "invalid",
			},
			e: false,
		},
		"empty_object": {
			obj: map[string]any{},
			e:   false,
		},
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
					"resourceRefs": []any{
						map[string]any{"name": "mr1"},
						map[string]any{"name": "mr2"},
					},
				},
			},
			e: true,
		},
		"claim_with_resourceRef": {
			obj: map[string]any{
				"spec": map[string]any{
					"resourceRef": map[string]any{
						"name": "xr1",
					},
				},
			},
			e: false,
		},
		"no_spec": {
			obj: map[string]any{
				"metadata": map[string]any{"name": "test"},
			},
			e: false,
		},
		"empty_spec": {
			obj: map[string]any{
				"spec": map[string]any{},
			},
			e: false,
		},
		"spec_not_map": {
			obj: map[string]any{
				"spec": 42,
			},
			e: false,
		},
		"empty_object": {
			obj: map[string]any{},
			e:   false,
		},
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
		obj                    map[string]any
		eSynced, eReady, eMsg string
	}{
		"both_true": {
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Synced", "status": "True"},
						map[string]any{"type": "Ready", "status": "True"},
					},
				},
			},
			eSynced: "True",
			eReady:  "True",
			eMsg:    "",
		},
		"synced_false": {
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Synced", "status": "False", "message": "sync error"},
						map[string]any{"type": "Ready", "status": "True"},
					},
				},
			},
			eSynced: "False",
			eReady:  "True",
			eMsg:    "sync error",
		},
		"ready_false": {
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Synced", "status": "True"},
						map[string]any{"type": "Ready", "status": "False", "message": "not ready yet"},
					},
				},
			},
			eSynced: "True",
			eReady:  "False",
			eMsg:    "not ready yet",
		},
		"both_false": {
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Synced", "status": "False", "message": "sync failed"},
						map[string]any{"type": "Ready", "status": "False", "message": "not ready"},
					},
				},
			},
			eSynced: "False",
			eReady:  "False",
			eMsg:    "sync failed",
		},
		"no_status": {
			obj:     map[string]any{},
			eSynced: "-",
			eReady:  "-",
			eMsg:    "",
		},
		"no_conditions": {
			obj: map[string]any{
				"status": map[string]any{},
			},
			eSynced: "-",
			eReady:  "-",
			eMsg:    "",
		},
		"only_synced": {
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Synced", "status": "True"},
					},
				},
			},
			eSynced: "True",
			eReady:  "-",
			eMsg:    "",
		},
		"only_ready": {
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "False", "message": "waiting"},
					},
				},
			},
			eSynced: "-",
			eReady:  "False",
			eMsg:    "waiting",
		},
		"false_without_message": {
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Synced", "status": "False"},
						map[string]any{"type": "Ready", "status": "True"},
					},
				},
			},
			eSynced: "False",
			eReady:  "True",
			eMsg:    "",
		},
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
		"core_v1": {
			apiVersion: "v1",
			kind:       "Secret",
			e:          "v1/secrets",
		},
		"group_v1alpha1": {
			apiVersion: "database.example.org/v1alpha1",
			kind:       "PostgreSQLInstance",
			e:          "database.example.org/v1alpha1/postgresqlinstances",
		},
		"group_v1": {
			apiVersion: "apiextensions.crossplane.io/v1",
			kind:       "Composition",
			e:          "apiextensions.crossplane.io/v1/compositions",
		},
		"group_v1beta1": {
			apiVersion: "s3.aws.crossplane.io/v1beta1",
			kind:       "Bucket",
			e:          "s3.aws.crossplane.io/v1beta1/buckets",
		},
	}

	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			gvr := resolveGVR(u.apiVersion, u.kind)
			assert.Equal(t, u.e, gvr.String())
		})
	}
}
