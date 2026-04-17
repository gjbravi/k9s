# Resource Tree View

## Overview

K9s ships a provider-driven **resource-tree view** that renders the child
resources of a selected root resource as a table with the canonical tree
drawing characters, using the same mechanism [ArgoCD][argo-resources] and
[`crossplane beta trace`][crossplane-beta-trace] use under the hood. It is the
generic umbrella that replaces (and subsumes) the original Crossplane trace
view, and is designed to be extended with additional providers over time.

Press `Shift-T` on any resource row. If a registered provider claims the
resource, a tree view opens rooted at that resource. Otherwise k9s emits a
warning in the flash bar.

| Provider | Root resources | Children |
|----------|----------------|----------|
| `crossplane` | Claims (XRC), Composite Resources (XR), Managed Resources (MR), any CRD whose group contains `crossplane.io`, or resources exposing `Synced`/`Ready` conditions | `spec.resourceRef` / `spec.resourceRefs` targets plus `spec.writeConnectionSecretToRef` secrets |
| `argocd` | `argoproj.io` `Application` and `ApplicationSet` | Each entry in `status.resources`; generated Applications of an ApplicationSet are recursively expanded into their own resources (cycle-safe, depth-capped) |
| `ownerref` | Generic fallback for any namespaced resource | Walks `metadata.ownerReferences` over common workload kinds (Deployments → ReplicaSets → Pods, CronJobs → Jobs → Pods, …) |

## Configuration

The feature is fully contained behind two layers of opt-in:

1. `k9s.resourceTree.enable` — umbrella toggle. When `false` the `Shift-T`
   binding is **never** installed, regardless of per-provider state.
2. `k9s.resourceTree.providers.<name>.enable` — independent toggle for each
   provider. Disabling all providers also hides the binding.

```yaml
k9s:
  resourceTree:
    enable: true
    providers:
      crossplane:
        enable: true
      argocd:
        enable: true
        expandChildApps: true   # recursively expand generated Applications
      ownerRef:
        enable: true            # generic owner-reference fallback
```

Both layers default to `false`, so the feature stays inert until explicitly
turned on.

## Navigation

Inside the tree view:

| Key | Action |
|-----|--------|
| `d` | Describe the selected child resource |
| `y` | View YAML of the selected child resource |
| `w` | Toggle wide mode (full-width `STATUS` column) |
| `p` | Pause reconciliation (Crossplane provider only; non-readonly mode) |
| `u` | Unpause reconciliation (Crossplane provider only; non-readonly mode) |
| `esc` / `q` | Go back to the previous view |

Describe/YAML open the standard k9s detail views, so you can continue to
navigate the selected child resource exactly as you would from a normal list
view.

## Columns

The right-hand columns are provider-defined. The last column always holds a
free-form `STATUS` message and is truncated to 64 characters unless wide mode
is toggled.

### Crossplane

`RESOURCE` · `SYNCED` · `READY` · `STATUS`

Mirrors the output of `crossplane beta trace`. `SYNCED`/`READY` values render
green for `True` and orange-red for `False`; `STATUS` carries the Ready reason
(e.g. `Available`, `Creating`, `ReconcileError`) or the first failing
condition's message when the resource is unhealthy.

Missing references are rendered as rows with `STATUS = MISSING`.

### ArgoCD

`KIND` · `SYNC` · `HEALTH` · `STATUS`

- For an `Application`, the root row shows the Application's
  `status.sync.status` and `status.health.status`; children are built from
  `status.resources`, preserving each resource's sync and health.
- For an `ApplicationSet`, the root row summarizes the ApplicationSet and its
  children are the generated Applications; when `expandChildApps: true` each
  child Application is walked recursively (depth-capped at 5 to avoid runaway
  work on large application-of-applications trees).

## How it works

The mechanism is the same one ArgoCD and Crossplane expose in their own
tooling: walk declarative references off a root object, match them against the
cluster's live state, and render the hierarchy as a table.

1. On `Shift-T`, the `ResourceTreeExtender` fetches the selected object as
   `*unstructured.Unstructured`.
2. It iterates the enabled providers in order and asks each one whether it
   `Applies(gvr, obj)`.
3. The first matching provider builds a `tree.Node` graph via
   `BuildRoot(ctx, factory, gvr, obj)`. Providers reach out to the Kubernetes
   API via the shared `dao.DirectGet` helper to resolve referenced resources.
4. The generic `ResourceTree` view flattens the node graph into rows with
   tree-drawing prefixes (`├──`, `└──`, `│`) and renders them with the
   provider-defined columns.

### Adding a provider

A new provider is ~100 lines of Go. Implement the `tree.Provider` interface in
`internal/view/tree`:

```go
type Provider interface {
    ID() string
    DisplayName() string
    Applies(gvr *client.GVR, obj *unstructured.Unstructured) bool
    Columns() []string
    BuildRoot(ctx context.Context, f dao.Factory, gvr *client.GVR, obj *unstructured.Unstructured) (*Node, error)
}
```

Optionally implement `tree.PausableProvider` to surface `p`/`u` actions, or
`tree.StatusProvider` to drive column coloring. Self-register from an `init()`
function via `tree.Register("<id>", factory)`; the extender discovers it
automatically and respects the umbrella `resourceTree.enable` gate.

## Limitations

- Providers rely on resources present in the cluster; missing CRDs or RBAC
  restrictions on children yield `MISSING` rows.
- The ArgoCD provider does not implement `Sync` / `Refresh` actions (the
  existing `plugins/argocd.yaml` plugin keeps covering that surface).
- Child GVR resolution prefers the cluster's discovery client and falls back
  to a small irregular-plural table plus standard English suffix rules; truly
  exotic CRD plurals may still need special-casing.
- The Crossplane provider renders one connection secret per resource, via
  `spec.writeConnectionSecretToRef`; alternative publishing mechanisms like
  `spec.publishConnectionDetailsTo` are not tracked.

[argo-resources]: https://argo-cd.readthedocs.io/en/stable/user-guide/commands/argocd_app_resources/
[crossplane-beta-trace]: https://docs.crossplane.io/latest/cli/command-reference/#beta-trace
