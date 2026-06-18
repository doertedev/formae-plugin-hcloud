# Schema

The plugin's desired-state surface is defined in [Pkl](https://pkl-lang.org/)
under `schema/pkl/`. The formae agent evaluates a user's `forma` file against
this schema, the plugin's Go handlers in `pkg/` act on the resolved state,
and the schema's hint annotations tell formae how to treat each field and
resource.

## Layout

```
schema/pkl/
├── PklProject               Pkl project manifest
├── PklProject.deps.json     Pkl dependency lock
├── VERSION                  schema package version
├── hcloud.pkl               module root: Config, FieldHint, ResourceHint
├── compute/
│   ├── image.pkl            HETZNER::Compute::Image
│   ├── placement_group.pkl  HETZNER::Compute::PlacementGroup
│   └── server.pkl           HETZNER::Compute::Server
├── network/
│   ├── network.pkl          HETZNER::Network::Network
│   ├── load_balancer.pkl    HETZNER::Network::LoadBalancer
│   ├── floating_ip.pkl      HETZNER::Network::FloatingIP
│   └── primary_ip.pkl       HETZNER::Network::PrimaryIP
├── storage/
│   └── volume.pkl           HETZNER::Storage::Volume
└── security/
    ├── ssh_key.pkl          HETZNER::Security::SSHKey
    ├── firewall.pkl         HETZNER::Security::Firewall
    └── certificate.pkl      HETZNER::Security::Certificate
```

The plugin manifest at the repo root, `formae-plugin.pkl`, declares `name`,
`version`, `namespace` (`HETZNER`), `category`, `license`, and
`minFormaeVersion`. The Makefile reads `name`/`version` from it via `pkl
eval` so the install layout never drifts from the manifest.

## Resources

11 resource types are managed under the `HETZNER` namespace:

| Category | Type |
|----------|------|
| Compute | `HETZNER::Compute::Server`, `HETZNER::Compute::Image`, `HETZNER::Compute::PlacementGroup` |
| Network | `HETZNER::Network::Network`, `HETZNER::Network::LoadBalancer`, `HETZNER::Network::FloatingIP`, `HETZNER::Network::PrimaryIP` |
| Storage | `HETZNER::Storage::Volume` |
| Security | `HETZNER::Security::SSHKey`, `HETZNER::Security::Firewall`, `HETZNER::Security::Certificate` |

Each resource schema opens a `formae.Resource` subclass annotated with
`@hcloud.ResourceHint` and declares its fields with `@hcloud.FieldHint`s.

For copy-pasteable examples of every Pkl data shape used in this repo
(`String`, `Int`, `Float`, `Boolean`, enums, mappings, listings, nested
classes, hints, and full resource fragments), see
[schema-data-types.md](schema-data-types.md).

## Module root (`hcloud.pkl`)

```pkl
module hcloud

import "@formae/formae.pkl"

open class Config {
  fixed type: String = "HETZNER"
  @formae.ConfigFieldHint { createOnly = true }
  token: String
}

class FieldHint extends formae.FieldHint {}

class ResourceHint extends formae.ResourceHint {
    extractable = false
}
```

- **`Config`** is the target config schema. `token` is `createOnly` and is
  read by the plugin (see [development.md](development.md#environment-variables)
  for the resolution precedence).
- **`FieldHint`** extends `formae.FieldHint` so resource schemas can use the
  shorter `@hcloud.FieldHint` import alias.
- **`ResourceHint`** extends `formae.ResourceHint` and pins
  `extractable = false` (see [Extractability caveat](#extractability-caveat)).

## Field hints

`@hcloud.FieldHint { ... }` annotates individual fields. The attributes formae
honours:

| Attribute | Effect |
|-----------|--------|
| `required = true` | The field must be present in desired state. |
| `createOnly = true` | Settable only at create; ignored on update (treated as immutable). |
| `hasProviderDefault = true` | The field is a provider-computed output. formae treats a present-but-unexpected actual value as a provider default rather than a diff. |

Two important consequences (also in the top-level README caveats):

- **Optional string fields cannot be cleared to empty.** On update,
  empty/omitted string fields are treated as "no change", not "clear". Omit
  the field to leave it untouched; clear it out-of-band if required.
  `createOnly` fields are not updatable at all.
- **Reserved label key `managed_by`.** The plugin injects
  `managed_by=formae` on every resource it manages and strips it from
  observed state. `managed_by` is reserved — do not declare it in desired
  state; a user-supplied value is silently overwritten with `formae`.

## Resource hints

`@hcloud.ResourceHint { ... }` annotates each `Resource` subclass. Common
attributes:

| Attribute | Example | Effect |
|-----------|---------|--------|
| `type` | `"HETZNER::Compute::Server"` | The fully-qualified resource type the handler is registered under. Must match the `register(...)` call in `pkg/<type>.go`. |
| `identifier` | `"id"` | The field formae uses as the stable primary identifier. |
| `portable` | `true` | Whether the resource can be imported/extracted across stacks. |
| `discoverable` | `false` | Whether unmanaged resources of this type can be discovered into inventory. |
| `extractable` | `false` (default override) | Whether `formae extract` is enabled for this type. |

`HETZNER::Security::Certificate` sets `discoverable = false` because uploaded
certificates require `privateKey` for creation, but Hetzner never returns the
private key on read/list. Discovery would otherwise produce an unmanaged
resource that cannot satisfy the schema's required write-only input.

## Extractability caveat

`schema/pkl/hcloud.pkl` pins `extractable = false` on the base `ResourceHint`
because this plugin's schema package is **not yet published** to the formae
registry at `hub.platform.engineering`. The formae CLI's `extract` command
resolves the schema as a remote `package://` dependency, so until the package
is published it 404s during `pkl project resolve`. The conformance harness
honours `extractable = false` by skipping the extract phase.

Remove the override (or set `extractable = true` per resource) once the
schema package is published. Publishing the schema is part of the packaging
flow — see [packaging.md](packaging.md).

## Test fixtures

`testdata/*.pkl` are the conformance harness inputs — one fixture per
managed resource type, each declaring the cheapest possible resource so the
harness can exercise the full plugin contract (create / read / update /
delete / inventory / sync / extract). They source shared config (stack,
target, run-scoped IDs) from `testdata/config/`.

These are **conformance-only** inputs:

- They are **not** inputs to the live hcloud smoke tests — those build inputs
  inline in Go.
- They are **not** golden/canned responses for unit tests either — the mock
  unit tests construct inputs in Go as well.

See [`testdata/README.md`](../testdata/README.md) for the
fixture contract.

## Validating the schema locally

Requires the `pkl` CLI `0.30.0`+ (enforced by `make check-pkl`).

```
make pkl-eval      # pkl eval on formae-plugin.pkl and schema/pkl/**/*.pkl
make pkg-pkl       # pkl project package schema/pkl -> .out/
```

`make pkl-eval` skips `_*.pkl` files (dev/test scaffolding). Add a new module
under `schema/pkl/<category>/` and it is picked up automatically by both
targets.
