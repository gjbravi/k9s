// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config

// Crossplane tracks Crossplane integration options.
type Crossplane struct {
	// Enable activates the Crossplane trace feature (Shift-T).
	Enable bool `json:"enable" yaml:"enable"`
}

// NewCrossplane returns a new instance with defaults.
func NewCrossplane() Crossplane {
	return Crossplane{}
}
