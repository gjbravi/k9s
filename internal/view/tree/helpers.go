// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package tree

import (
	"fmt"
	"strings"
	"sync"

	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/dao"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/restmapper"
)

// gvrCache memoizes "apiVersion|kind" → *client.GVR resolutions, both
// heuristic and discovery-derived, to keep tree builds cheap.
var (
	gvrCacheMu sync.RWMutex
	gvrCache   = map[string]*client.GVR{}
)

// irregularPlurals captures the K8s kinds whose plural is not derivable from
// the standard English suffix rules below. The right-hand side is the
// lowercase resource (plural) name as registered with the API server.
var irregularPlurals = map[string]string{
	"endpoints":               "endpoints",
	"endpointslice":           "endpointslices",
	"ingress":                 "ingresses",
	"ingressclass":            "ingressclasses",
	"storageclass":            "storageclasses",
	"priorityclass":           "priorityclasses",
	"resourcequota":           "resourcequotas",
	"networkpolicy":           "networkpolicies",
	"podsecuritypolicy":       "podsecuritypolicies",
	"poddisruptionbudget":     "poddisruptionbudgets",
	"podmonitor":              "podmonitors",
	"servicemonitor":          "servicemonitors",
	"prometheusrule":          "prometheusrules",
	"alertmanagerconfig":      "alertmanagerconfigs",
	"horizontalpodautoscaler": "horizontalpodautoscalers",
	"verticalpodautoscaler":   "verticalpodautoscalers",
}

// Pluralize converts a Kubernetes kind into the lower-case API resource
// (plural) name. It honors a small irregular-plurals table for the few K8s
// kinds whose plural is not derivable from English suffix rules, then falls
// back to standard rules:
//   - ends in s/x/z/ch/sh → +es
//   - ends in consonant+y → strip y, +ies
//   - default            → +s
//
// CRDs that follow non-English plural conventions should be resolved via
// ResolveGVRForFactory (which prefers the API discovery cache) instead.
func Pluralize(kind string) string {
	if kind == "" {
		return ""
	}
	lower := strings.ToLower(kind)
	if v, ok := irregularPlurals[lower]; ok {
		return v
	}
	switch {
	case hasAnySuffix(lower, "s", "x", "z", "ch", "sh"):
		return lower + "es"
	case len(lower) > 1 && strings.HasSuffix(lower, "y") && !isVowel(lower[len(lower)-2]):
		return lower[:len(lower)-1] + "ies"
	default:
		return lower + "s"
	}
}

// ResolveGVR converts an apiVersion + kind into a *client.GVR using the
// improved pluralization heuristic. Use ResolveGVRForFactory whenever a
// dao.Factory is available — it consults the API discovery cache first and
// only falls back to the heuristic for unknown kinds.
func ResolveGVR(apiVersion, kind string) *client.GVR {
	plural := Pluralize(kind)

	parts := strings.SplitN(apiVersion, "/", 2)
	if len(parts) == 1 {
		return client.NewGVR(fmt.Sprintf("%s/%s", apiVersion, plural))
	}
	group, version := parts[0], parts[1]
	return client.NewGVR(fmt.Sprintf("%s/%s/%s", group, version, plural))
}

// ResolveGVRForFactory resolves an (apiVersion, kind) pair to a *client.GVR
// using API server discovery when a Connection is available, falling back to
// the heuristic ResolveGVR otherwise. Results are memoized per-process so the
// resolution cost is paid at most once per (apiVersion, kind).
func ResolveGVRForFactory(f dao.Factory, apiVersion, kind string) *client.GVR {
	if apiVersion == "" || kind == "" {
		return ResolveGVR(apiVersion, kind)
	}
	key := apiVersion + "|" + kind
	gvrCacheMu.RLock()
	if hit, ok := gvrCache[key]; ok {
		gvrCacheMu.RUnlock()
		return hit
	}
	gvrCacheMu.RUnlock()

	resolved := resolveViaDiscovery(f, apiVersion, kind)
	if resolved == nil {
		resolved = ResolveGVR(apiVersion, kind)
	}

	gvrCacheMu.Lock()
	gvrCache[key] = resolved
	gvrCacheMu.Unlock()
	return resolved
}

// resolveViaDiscovery asks the API server's discovery cache for the canonical
// resource name. Returns nil when the factory has no live connection or
// discovery returns no mapping.
func resolveViaDiscovery(f dao.Factory, apiVersion, kind string) *client.GVR {
	if f == nil {
		return nil
	}
	conn := f.Client()
	if conn == nil {
		return nil
	}
	disc, err := conn.CachedDiscovery()
	if err != nil || disc == nil {
		return nil
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil
	}
	groups, err := restmapper.GetAPIGroupResources(disc)
	if err != nil {
		return nil
	}
	mapper := restmapper.NewDiscoveryRESTMapper(groups)
	mapping, err := mapper.RESTMapping(schema.GroupKind{Group: gv.Group, Kind: kind}, gv.Version)
	if err != nil {
		return nil
	}
	return client.NewGVR(formatGVR(mapping.Resource))
}

// formatGVR formats a schema.GroupVersionResource as the k9s GVR string.
func formatGVR(r schema.GroupVersionResource) string {
	if r.Group == "" {
		return fmt.Sprintf("%s/%s", r.Version, r.Resource)
	}
	return fmt.Sprintf("%s/%s/%s", r.Group, r.Version, r.Resource)
}

// AsUnstructured narrows a runtime.Object to *unstructured.Unstructured.
func AsUnstructured(o runtime.Object) (*unstructured.Unstructured, bool) {
	u, ok := o.(*unstructured.Unstructured)
	return u, ok
}

// apiVersionGroup returns the group portion of an apiVersion string
// ("group/version" → "group", "v1" → "").
func apiVersionGroup(apiVersion string) string {
	parts := strings.SplitN(apiVersion, "/", 2)
	if len(parts) == 1 {
		return ""
	}
	return parts[0]
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	default:
		return false
	}
}
