# formae-plugin-hcloud

A [formae](https://github.com/platform-engineering-labs/formae) resource
plugin for [Hetzner Cloud](https://www.hetzner.com/cloud) (hcloud).

Status: `v0.1.0`. Targets formae `>= 0.86.0` (the `minFormaeVersion` declared
in [`formae-plugin.pkl`](formae-plugin.pkl)).

## What this is

A single Go binary that implements the formae resource-plugin contract for
Hetzner Cloud under the `HETZNER` namespace. The formae agent discovers the
installed binary from `~/.pel/formae/plugins/` and dispatches CRUD, inventory,
sync, extract, and discovery operations to it; the plugin translates those
into hcloud API calls via
[`hcloud-go`](https://github.com/hetznercloud/hcloud-go).

## Supported resources

11 resource types across four categories:

- **Compute** — `HETZNER::Compute::Server`, `HETZNER::Compute::Image`,
  `HETZNER::Compute::PlacementGroup`
- **Network** — `HETZNER::Network::Network`, `HETZNER::Network::LoadBalancer`,
  `HETZNER::Network::FloatingIP`, `HETZNER::Network::PrimaryIP`
- **Storage** — `HETZNER::Storage::Volume`
- **Security** — `HETZNER::Security::SSHKey`, `HETZNER::Security::Firewall`,
  `HETZNER::Security::Certificate`

> **`HETZNER::Compute::Image`** — formae only owns *snapshots*: a snapshot is
> created from an existing server (`Server.CreateImage`) and is otherwise
> read/deleted. Image creation is therefore excluded from the conformance
> fixtures; the unit tests cover it against mocks. See
> [docs/testing.md](docs/testing.md#hetznercomputeimage-caveat).

## Prerequisites

- Go `1.26.0` or newer (see [`go.mod`](go.mod)).
- The `pkl` CLI, `0.30.0` or newer — used by the Makefile to read
  `formae-plugin.pkl` and validate the schema. The Makefile's `check-pkl`
  target (a prerequisite of `install`, `pkl-eval`, and `pkg-pkl`) enforces
  this.
- An hcloud API token (for anything that hits the real API).

## Local setup

```
cp .env-sample .env
# edit .env: HCLOUD_TOKEN="<your hcloud project token>"
```

`.env` is gitignored. The live/conformance Make targets source it
automatically. **Never use a production project token.**

## Configuration & token behaviour

The plugin resolves its hcloud API token via `resolveToken` in
[`pkg/plugin.go`](pkg/plugin.go):

1. A `token` field in the formae target config JSON (`{"token":"..."}`) wins.
2. Otherwise the `HCLOUD_TOKEN` environment variable is consulted.

Empty/null config (`{"token":""}`) falls back to the env var. A **malformed**
config (genuine JSON parse error) is surfaced as an error rather than
silently falling through to the env var — a typo'd token config should not
authenticate as a different credential drawn from the environment.

## Build & install

```
make build     # compiles ./bin/formae-plugin-hcloud
make install   # installs into ~/.pel/formae/plugins/<name>/v<version>/
```

`make install` honours the formae agent's versioned-layout discovery contract;
without it the agent never sees the plugin and apply/conformance fail with
`timeout waiting for plugin <NS> to register`. Override the base dir with
`make install PLUGINS_DIR=/tmp/plugins`.

## Testing

Three tiers — full details in [docs/testing.md](docs/testing.md):

```
make test                       # mock-only unit tests; never hits the API
make test-live-hcloud           # direct live hcloud API smoke tests (opt-in, real API)
make conformance-test           # full plugin contract via a real formae agent (real API)
make conformance-test-crud TEST=ssh-key   # single-resource conformance filter
```

| Tier | Hits real API? | Build tag |
|------|----------------|-----------|
| `make test` | No | (none) |
| `make test-live-hcloud` | Yes | `integration` |
| `make conformance-test` | Yes | `conformance` |

The live and conformance suites are mutually exclusive by build tag, and each
has its own safety gates (double opt-in, labelled resource sweeps). The
credential check and labelled-resource sweeps are implemented inline in the
Makefile — no shell scripts ship with this repo.

## Schema

The Pkl schema lives under [`schema/pkl/`](schema/pkl/) and defines the
desired-state surface, field hints (`required`, `createOnly`,
`hasProviderDefault`), and resource hints (`identifier`, `portable`,
`extractable`). Full layout and conventions in
[docs/schema.md](docs/schema.md).

```
make pkl-eval    # validate formae-plugin.pkl and schema/pkl/**/*.pkl
make pkg-pkl     # pkl project package schema/pkl -> .out/
```

## Caveats

- **Reserved label key `managed_by`.** The plugin injects
  `managed_by=formae` on every resource it manages and strips it from
  observed state. `managed_by` is reserved — do not declare it in desired
  state; a user-supplied value is silently overwritten with `formae`.
- **Optional string fields cannot be cleared to empty.** On update,
  empty/omitted string fields are treated as "no change" rather than "clear".
  Omit the field to leave it untouched; clear it out-of-band if required.
  `createOnly` fields are not updatable at all.
- **Discovery surfaces errors, not empty lists.** `List` returns the
  underlying error on client-resolution failures (e.g. invalid token) and
  per-type API errors, so the drift workflow can distinguish "no resources
  exist" from "the plugin could not enumerate resources". The one exception
  is an unsupported resource type, which yields an empty list (the formae
  agent fans `List` out across every registered type and types this plugin
  does not handle legitimately enumerate as empty).
- **`extractable = false` by default.** The schema package is not yet
  published to the registry, so `formae extract` is disabled until it is. See
  [docs/schema.md](docs/schema.md#extractability-caveat).

## Packaging

Building an orbital `.opkg` and publishing it to the formae registry is
covered in [docs/packaging.md](docs/packaging.md).

```
make pkg       # build bin/formae-plugin-hcloud-<version>.opkg (needs pel/orbital ops)
make publish   # publish the .opkg to repo pel, channel $(CHANNEL)
```

## Documentation

- [docs/development.md](docs/development.md) — local workflow, prerequisites,
  Make targets, env vars.
- [docs/testing.md](docs/testing.md) — unit vs live hcloud smoke vs
  conformance, safety gates, labels, filters, timeouts, cleanup.
- [docs/schema.md](docs/schema.md) — Pkl schema layout, resources, hints,
  fixtures, extractability.
- [docs/schema-data-types.md](docs/schema-data-types.md) — copy-pasteable Pkl
  examples for every schema data shape used by the plugin.
- [docs/packaging.md](docs/packaging.md) — `.opkg` build/publish flow.

## License

Apache-2.0. See [LICENSE](LICENSE).
