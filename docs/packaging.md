# Packaging & publishing

How to build the plugin into an orbital package (`.opkg`) and publish it to
the formae registry so users can install it with
`formae plugin install formae-plugin-hcloud`.

This document is a developer/publisher runbook. There is **no** CI pipeline
in this repo — every step here is run by hand on a machine that has the
pel/orbital toolchain installed.

> **Status:** the packaging flow is validated end-to-end up to the `publish`
> step. `make pkg` produces a correctly-structured `.opkg` whose manifest
> round-trips the `display:kind=plugin` / `plugin:namespace` metadata the
> formae plugin manager classifies on (verifiable with `ops opkg dump`). The
> actual `publish` + signing step requires a registry keypair and credentials
> — see [Publishing](#publishing).

## The `ops` name collision

There are **two different `ops` binaries** in the wild. A bare `ops` on PATH
can resolve to either; only one is correct for formae packaging.

| Binary | Source | `ops opkg build`? | Use |
|--------|--------|-------------------|-----|
| `~/.ops/bin/ops` (Nanos) | Nanos / microvm | `unknown command` | wrong tool |
| `/opt/pel/bin/ops` (orbital) | pel / orbital | works | correct tool |

The Nanos tool reports **"Ops version …, Nanos version …"**. The Makefile
target `check-ops` (a prerequisite of `pkg` and `publish`) probes
`ops opkg build --help` and aborts with an actionable error if it resolves to
the Nanos tool. If you hit that error, install the orbital toolchain (below)
and ensure `/opt/pel/bin` precedes `~/.ops/bin` on PATH, or pass
`OPS=/opt/pel/bin/ops` to `make`.

## Install the pel/orbital toolchain

```bash
bash -c "$(curl -fsSL https://hub.platform.engineering/get/setup.sh)" \
  -- install --yes orbital
```

This installs the orbital `ops` binary (plus `pkl` `0.30.0`+, the minimum
version required by this repo's Makefile) under `/opt/pel/bin/`.
Ensure that dir is on PATH:

```bash
export PATH=/opt/pel/bin:$PATH
ops --help | head     # should list `opkg` and `publish` as subcommands
```

## Build commands

All commands are Make targets; see `make help` for the full list.

| Target | Requires | What it does |
|--------|----------|--------------|
| `make build` | Go | Compiles `./bin/formae-plugin-hcloud`. |
| `make dist` | Go | Stages `dist/pel/plugins/hcloud/v<version>/...` (mirrors `install`'s versioned discovery layout) for packaging. |
| `make pkg` | Go + pel/orbital `ops` | Builds `bin/formae-plugin-hcloud-<version>.opkg` from `dist/pel`. |
| `make publish` | Go + pel `ops` + registry creds | Publishes the `.opkg` to repo `pel`, channel `$(CHANNEL)`. |

### What gets packaged (`dist/pel`)

`make dist` mirrors the `install` target's versioned plugin-discovery
layout into `dist/pel`:

```
dist/pel/
└── plugins/
    ├── Hcloud.pkl                            ← root resolver file (plugins:/Hcloud.pkl)
    └── hcloud/
        └── v0.1.0/                           ← versioned (matches INSTALL_DIR)
            ├── Hcloud.pkl                    ← per-plugin resolver copy
            ├── formae-plugin.pkl             ← manifest
            ├── hcloud                        ← plugin binary, named exactly <name>
            └── schema/
                └── pkl/
                    ├── PklProject
                    ├── PklProject.deps.json
                    ├── VERSION
                    ├── hcloud.pkl
                    ├── compute/
                    │   ├── image.pkl
                    │   ├── placement_group.pkl
                    │   └── server.pkl
                    ├── network/
                    │   ├── floating_ip.pkl
                    │   ├── load_balancer.pkl
                    │   ├── network.pkl
                    │   └── primary_ip.pkl
                    ├── security/
                    │   ├── certificate.pkl
                    │   ├── firewall.pkl
                    │   └── ssh_key.pkl
                    └── storage/
                        └── volume.pkl
```

The versioned layout is required: the formae agent's plugin discovery
walks `<PLUGINS_DIR>/<name>/v<semver>/<name>` and refuses to register the
plugin otherwise (see `Makefile` header for the contract). The earlier
`plugins/HETZNER/...` shape this target produced was silently
undiscoverable after package install even though `make install` worked
locally.

`ops opkg build --target-path dist/pel` walks this tree and packages it. The
resulting manifest carries:

| Metadata | Value | Purpose |
|----------|-------|---------|
| `display:kind` | `plugin` | Plugin manager classifies the package on this. |
| `display:category` | `cloud` | UI filter tag. |
| `plugin:type` | `resource` | Resource plugin (vs auth). |
| `plugin:namespace` | `HETZNER` | Maps to the `HETZNER::` resource-type prefix. The plugin name (`hcloud`) is the on-disk directory name; the namespace is the resource-type prefix. |

All package identity/metadata is derived from `formae-plugin.pkl` via the
`Opkgfile` (name, version, license, summary, description, namespace), so the
package cannot drift from the plugin manifest.

### Inspecting a built package

```bash
ops opkg info     bin/*.opkg     # metadata table
ops opkg dump     bin/*.opkg     # full manifest
ops opkg contents bin/*.opkg     # packaged file tree
```

## Publishing

Publishing = signing the package with the publisher's keypair, then pushing
it to a registry channel. Two prerequisites:

1. **Signing keypair** for publisher `platform.engineering`. Create/register
   one via `ops pki …` (the `ERRO no keypair found … not signing` line from
   `make pkg` indicates this is missing). Packages can be built unsigned for
   local testing, but the registry rejects unsigned publishes.
2. **Registry credentials** — `ops publish` authenticates against
   `hub.platform.engineering` (repo `pel`).

### Typical release flow (for a tag `v0.1.0`)

```bash
export PATH=/opt/pel/bin:$PATH   # orbital ops on PATH
make dist
make pkg
make publish CHANNEL=stable
```

### Overrides

- `OPS=/opt/pel/bin/ops make pkg` — point at an explicit orbital `ops` binary.
- `CHANNEL=dev make publish` — publish to the `dev` channel instead of
  `stable` (formae convention: `X.Y.Z` tags → `stable`, `X.Y.Z-dev[.N]` →
  `dev`).

### Registry target

- Repo: `pel`
- Channel: `stable` for `X.Y.Z` tags, `dev` for `X.Y.Z-dev[.N]` tags.

## Publishing the Pkl schema package (extractability)

`formae extract` and cross-stack import resolve the schema as a remote
`package://` dependency. Until the schema package is published to the
registry, the plugin pins `extractable = false` in
`schema/pkl/hcloud.pkl::ResourceHint` and the conformance harness skips the
extract phase. See [schema.md](schema.md#extractability-caveat).

To publish the schema package (separate from the `.opkg`):

```bash
make pkg-pkl      # pkl project package schema/pkl -> .out/
# publish with pkl per the pkl-package registry flow, then set extractable=true
```
