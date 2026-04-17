// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"fmt"
	"strings"

	"github.com/derailed/k9s/internal/client"
)

// isCrossplaneClaim returns true if the object has spec.resourceRef (singular),
// indicating it is a Crossplane Claim (XRC).
func isCrossplaneClaim(obj map[string]any) bool {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = spec["resourceRef"]
	return ok
}

// isCrossplaneXR returns true if the object has spec.resourceRefs (plural),
// indicating it is a Crossplane Composite Resource (XR).
func isCrossplaneXR(obj map[string]any) bool {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = spec["resourceRefs"]
	return ok
}

// crossplaneConditions extracts Synced and Ready condition statuses from
// status.conditions of the unstructured object. Returns the status string
// for each condition and the message from the first False condition found.
func crossplaneConditions(obj map[string]any) (synced, ready string, message string) {
	synced, ready = "-", "-"

	status, ok := obj["status"].(map[string]any)
	if !ok {
		return
	}
	conditions, ok := status["conditions"].([]any)
	if !ok {
		return
	}

	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		condType, _ := cond["type"].(string)
		condStatus, _ := cond["status"].(string)
		condMessage, _ := cond["message"].(string)

		switch condType {
		case "Synced":
			synced = condStatus
			if condStatus != "True" && message == "" && condMessage != "" {
				message = condMessage
			}
		case "Ready":
			ready = condStatus
			if condStatus != "True" && message == "" && condMessage != "" {
				message = condMessage
			}
		}
	}

	return
}

// crossplaneReadyReason extracts the reason from the Ready condition.
// Returns values like "Available", "Creating", "ReconcileError", etc.
func crossplaneReadyReason(obj map[string]any) string {
	status, ok := obj["status"].(map[string]any)
	if !ok {
		return ""
	}
	conditions, ok := status["conditions"].([]any)
	if !ok {
		return ""
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if condType, _ := cond["type"].(string); condType == "Ready" {
			reason, _ := cond["reason"].(string)
			return reason
		}
	}
	return ""
}

// crossplaneCompositionResource extracts the composition-resource-name annotation.
func crossplaneCompositionResource(obj map[string]any) string {
	metadata, ok := obj["metadata"].(map[string]any)
	if !ok {
		return ""
	}
	annotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := annotations["crossplane.io/composition-resource-name"].(string)
	return name
}

// resolveGVR converts an apiVersion + kind into a *client.GVR.
// If apiVersion has no group (e.g. "v1"), the GVR is "v1/<plural-kind>".
// If apiVersion has a group (e.g. "database.example.org/v1alpha1"),
// the GVR is "<group>/<version>/<plural-kind>".
// Plural-kind uses a simple lowercase + "s" suffix heuristic.
func resolveGVR(apiVersion, kind string) *client.GVR {
	plural := strings.ToLower(kind) + "s"

	parts := strings.SplitN(apiVersion, "/", 2)
	if len(parts) == 1 {
		// No group, e.g. "v1"
		return client.NewGVR(fmt.Sprintf("%s/%s", apiVersion, plural))
	}

	// Has group, e.g. "database.example.org/v1alpha1"
	group, version := parts[0], parts[1]
	return client.NewGVR(fmt.Sprintf("%s/%s/%s", group, version, plural))
}
