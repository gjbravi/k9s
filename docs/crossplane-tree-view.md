# Crossplane Tree View

## Overview

K9s includes native support for visualizing Crossplane resource hierarchies using the built-in Xray (tree) view. This feature lets you inspect the full Claim → Composite Resource → Managed Resource tree directly in the k9s terminal UI, without switching to the Crossplane CLI.

The tree view follows the standard Crossplane resource model:

```
Claim (XRC) ─── namespaced
  └─ Composite Resource (XR) ─── cluster-scoped
       ├─ Managed Resource (MR) ─── cluster-scoped
       ├─ Managed Resource (MR)
       └─ Connection Secret (optional)
```

Each node in the tree displays its Synced/Ready status, making it easy to spot issues at any level of the hierarchy.

## Usage

Launch the Crossplane tree view using the `:xray` command followed by a Crossplane resource type:

```
:xray compositions
:xray compositeresourcedefinitions
:xray providers
```

These three core Crossplane types are registered out of the box. The tree view also works with any CRD whose API group contains `crossplane.io`, so custom Claims, Composite Resources, and Managed Resources are supported automatically.

### Using custom CRD types

If you have custom Crossplane CRDs installed (e.g., `postgresqlinstances.database.example.org`), you can use their plural name or short-name with `:xray`:

```
:xray postgresqlinstances
:xray buckets
```

To discover the plural names and short-names of your CRDs, run:

```shell
kubectl api-resources | grep crossplane
```

This lists all Crossplane-related resources with their short-names, API group, and whether they are namespaced or cluster-scoped.

### Navigation

Once in the tree view, you can use standard k9s navigation:

| Key | Action |
|-----|--------|
| `d` | Describe the selected resource |
| `y` | View YAML |
| `e` | Edit resource |
| `ctrl-d` | Delete resource |
| `esc` | Go back to the previous view |
| `/` | Filter resources |

## How it works

The Crossplane tree view detects the type of each resource by inspecting its unstructured fields:

- **Claim (XRC)**: Has `spec.resourceRef` (singular reference to a Composite Resource)
- **Composite Resource (XR)**: Has `spec.resourceRefs` (list of references to Managed Resources)
- **Managed Resource (MR)**: Leaf node with Synced/Ready conditions but no resource references

The tree is built by recursively following these references:

1. A Claim's `spec.resourceRef` points to its XR
2. The XR's `spec.resourceRefs` lists all composed MRs
3. If `spec.writeConnectionSecretToRef` is present on any resource, the referenced Secret is shown as a child node

### Dynamic CRD support

Crossplane resources are CRDs whose GVRs (Group/Version/Resource) vary by environment. K9s handles this through two mechanisms:

1. **Core types** (Compositions, CompositeResourceDefinitions, Providers) are registered with fixed GVRs
2. **Custom CRDs** are detected dynamically — any resource with an API group containing `crossplane.io` automatically gets the Crossplane tree renderer

This means you don't need to recompile k9s or add configuration to view custom Crossplane resource trees.

## Example tree output

When you run `:xray compositeresourcedefinitions` or view a custom Claim type, the tree looks like this:

```
🔀 my-database (default)                          True/True
└── 🔀 my-database-xyz                            True/True
    ├── 🔀 my-database-rds                        True/True
    ├── 🔀 my-database-sg                         True/True
    ├── 🔀 my-database-subnet-group               True/True
    └── 🔗 my-database-conn (default)             OK
```

In this example:
- `my-database` is a Claim in the `default` namespace
- `my-database-xyz` is the Composite Resource (XR) it references
- `my-database-rds`, `my-database-sg`, and `my-database-subnet-group` are Managed Resources
- `my-database-conn` is a Connection Secret

## Emojis and status indicators

### Emoji

All Crossplane resources (any resource whose API group contains `crossplane.io`) are displayed with the 🔀 emoji. Connection Secrets use the standard k9s Secret emoji (🔗).

### Status

Each node shows a status derived from the resource's Crossplane conditions (`Synced` and `Ready`):

| Synced | Ready | Status indicator | Meaning |
|--------|-------|------------------|---------|
| True | True | **OK** (green) | Resource is fully synced and ready |
| True | False | **TOAST** (orange-red) | Resource is synced but not ready |
| False | True | **TOAST** (orange-red) | Resource is ready but not synced |
| False | False | **TOAST** (orange-red) | Resource is neither synced nor ready |
| - | - | **OK** (green) | No conditions present (default) |

When a referenced resource cannot be found, the node displays **TOAST_REF** (orange) to indicate a missing reference.

### Info format

The info column displays conditions in the format `Synced/Ready`. When a condition is not `True`, the error message is appended:

```
True/True                          # All good
False/True: connection failed      # Synced is False with error message
True/False: resource not available # Ready is False with error message
False/False: provider error        # Both conditions False
-/-                                # No conditions found
```

## Limitations

### CRDs must be installed

The tree view only works for Crossplane CRDs that are installed in the cluster. If a CRD is not present (e.g., the Crossplane provider hasn't been installed yet), k9s will report an unsupported resource error. Install the relevant Crossplane providers and configurations first.

### RBAC permissions required

Your kubeconfig user or service account needs `list` and `get` permissions for all Crossplane resources you want to view in the tree. This includes:

- The root resource type (e.g., Compositions, Claims)
- All child resources referenced by `spec.resourceRef` and `spec.resourceRefs`
- Secrets referenced by `spec.writeConnectionSecretToRef`

If permissions are missing, the tree may render partially or show errors in the k9s flash bar.

### Pluralization heuristic

The tree view resolves CRD kinds to their plural resource names using a simple heuristic: lowercase the kind and append `s`. For example, `PostgreSQLInstance` becomes `postgresqlinstances`. This works for most Crossplane CRDs but may not handle irregular plurals correctly (e.g., a kind ending in `s`, `x`, or `y`). If you encounter issues, verify the actual plural name with `kubectl api-resources`.

### Connection Secrets

Connection Secrets are only shown in the tree if the resource has `spec.writeConnectionSecretToRef` set. Resources that use alternative secret publishing mechanisms (e.g., external secret stores via `spec.publishConnectionDetailsTo`) will not have their secrets displayed in the tree.

### Native feature

The Crossplane tree view is a native k9s feature built into the k9s binary. It is not a plugin and does not require the Crossplane CLI (`crossplane beta trace`) to be installed. It works by directly querying the Kubernetes API for Crossplane resources and following their reference fields.
