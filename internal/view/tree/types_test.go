// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package tree

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultStatus(t *testing.T) {
	uu := map[string]struct {
		val string
		e   StatusKind
	}{
		"empty":      {"", StatusNeutral},
		"true":       {"True", StatusOk},
		"healthy":    {"Healthy", StatusOk},
		"running":    {"Running", StatusOk},
		"false":      {"False", StatusError},
		"degraded":   {"Degraded", StatusError},
		"failed":     {"Failed", StatusError},
		"crashloop":  {"CrashLoopBackOff", StatusError},
		"recon":      {"Reconciling", StatusWarn},
		"pending":    {"Pending", StatusWarn},
		"unknownVal": {"SomeCustomThing", StatusNeutral},
		"dash":       {"-", StatusNeutral},
	}
	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			assert.Equal(t, u.e, DefaultStatus(u.val))
		})
	}
}

func TestNode_FQN(t *testing.T) {
	assert.Empty(t, (*Node)(nil).FQN())
	assert.Equal(t, "web", (&Node{Name: "web"}).FQN())
	assert.Equal(t, "default/web", (&Node{Namespace: "default", Name: "web"}).FQN())
}
